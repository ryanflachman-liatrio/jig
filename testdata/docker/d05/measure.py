#!/usr/bin/env python3
"""Explicit, bounded development measurement; not jig runtime conformance."""
import hashlib
from contextlib import contextmanager
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import subprocess
import tarfile
import tempfile
import threading
import uuid

REPO = Path(__file__).resolve().parents[3]
NAME = "jig-d05-measure-" + uuid.uuid4().hex


def command(args, **kwargs):
    kwargs.setdefault("timeout", 30)
    return subprocess.run(args, check=True, **kwargs)


class BoundedArchive:
    def __init__(self, stream):
        self.stream = stream
        self.remaining = 272 * 1024 * 1024

    def read(self, size):
        data = self.stream.read(min(size, self.remaining + 1))
        self.remaining -= len(data)
        if self.remaining < 0:
            raise RuntimeError("probe archive exceeds 272 MiB")
        return data


@contextmanager
def source_archive():
    process = subprocess.Popen(["git", "-C", str(REPO), "archive", "HEAD"], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    timer = threading.Timer(60, process.kill)
    timer.start()
    try:
        reader = BoundedArchive(process.stdout)
        with tarfile.open(fileobj=reader, mode="r|") as source:
            yield source
        while reader.read(65536):
            pass
        if process.wait(timeout=5) != 0:
            raise RuntimeError("source archive failed or timed out")
    finally:
        timer.cancel()
        if process.poll() is None:
            process.kill()
        process.stdout.close()
        process.wait(timeout=5)
        timer.join()


def main():
    image = command(["docker", "image", "inspect", "jig-d05-go:local", "--format", "{{.Id}}"], capture_output=True, text=True).stdout.strip()
    root = Path(tempfile.mkdtemp(prefix="jig-d05-measure-"))
    create_attempted = False
    try:
        workspace = root / "workspace"
        workspace.mkdir()
        scratch = root / "scratch"
        scratch.mkdir()
        # Only committed regular source files; never traverse ambient checkout
        # directories or extract archive links. The original .git is not copied.
        count = size = 0
        with source_archive() as source:
            for index, entry in enumerate(source):
                if index >= 40000 or len(entry.name) > 4096:
                    raise RuntimeError("probe archive entry limit exceeded")
                path = PurePosixPath(entry.name)
                if path.is_absolute() or ".." in path.parts:
                    raise RuntimeError("unsafe archive path")
                if any(part in {".git", ".jig", ".aws", ".ssh", ".docker"} or part.startswith(".env") for part in path.parts):
                    continue
                if entry.isdir():
                    continue
                if not entry.isfile():
                    raise RuntimeError("probe source requires regular files")
                count += 1
                size += entry.size
                if count > 20000 or size > 256 * 1024 * 1024 or entry.size > 32 * 1024 * 1024:
                    raise RuntimeError("probe source limit exceeded")
                target = workspace / entry.name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(source.extractfile(entry).read())
                target.chmod(0o755 if entry.mode & 0o111 else 0o644)
        print(json.dumps({"source_files": count, "source_bytes": size, "image_id": image}), flush=True)
        script = """set -eu
go version
printf 'cold_start='; date +%s
mkdir -p out
mkdir -p /scratch/go-tmp
export GOTMPDIR=/scratch/go-tmp
go build -o out/jig ./cmd/jig
printf 'cold_end='; date +%s
go build -o out/jig ./cmd/jig
printf 'warm_end='; date +%s
sha256sum out/jig
wc -c < out/jig
du -sk /scratch /workspace
for f in memory.peak pids.peak cpu.max memory.max memory.swap.max pids.max; do
  printf '%s=' "$f"; cat "/sys/fs/cgroup/$f"
done
"""
        create_attempted = True
        command(["docker", "create", "--name", NAME, "--label", "jig.d05.probe=" + NAME,
                 "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL",
                 "--security-opt=no-new-privileges", "--no-healthcheck", "--restart=no",
                 "--cpus=4", "--memory=4g", "--memory-swap=4g", "--pids-limit=512",
                 "--user", f"{os.getuid()}:{os.getgid()}", "--workdir=/workspace",
                 "--mount", f"type=bind,src={workspace},dst=/workspace",
                 "--mount", f"type=bind,src={scratch},dst=/scratch",
                 "--tmpfs", "/tmp:rw,nosuid,nodev,size=256m,mode=1777",
                 "--tmpfs", f"/home/jig:rw,nosuid,nodev,size=16m,uid={os.getuid()},gid={os.getgid()}",
                 "--entrypoint=/bin/sh", image, "-c", script], capture_output=True)
        command(["docker", "start", "--attach", NAME], timeout=900)
        exit_code = command(["docker", "inspect", "--format", "{{.State.ExitCode}}", NAME], capture_output=True, text=True).stdout.strip()
        if exit_code != "0":
            raise RuntimeError("measurement workload failed: " + exit_code)
        artifact = workspace / "out/jig"
        with artifact.open("rb") as binary:
            digest = hashlib.file_digest(binary, "sha256").hexdigest()
        print(json.dumps({"host_sha256": digest, "binary_bytes": artifact.stat().st_size}), flush=True)
    finally:
        # A failed create response is ambiguous. Inspect the exact unique name
        # and verify ownership before removal; never delete staging on doubt.
        if create_attempted:
            try:
                identity = command(["docker", "inspect", "--format", '{{.Id}} {{index .Config.Labels "jig.d05.probe"}}', NAME], capture_output=True, text=True).stdout.strip().split()
                if len(identity) != 2 or identity[1] != NAME or len(identity[0]) != 64 or any(c not in "0123456789abcdef" for c in identity[0]):
                    raise RuntimeError("probe ownership mismatch")
                command(["docker", "rm", "--force", identity[0]], capture_output=True)
            except Exception as error:
                raise RuntimeError(f"probe cleanup uncertain; retained staging: {root}; exact container name: {NAME}") from error
        shutil.rmtree(root)


if __name__ == "__main__":
    main()
