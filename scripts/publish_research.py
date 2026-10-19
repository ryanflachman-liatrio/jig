"""Publish the research workflow's approved Markdown snapshots, without rewriting."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sys
import tempfile


DOCUMENTS = {
    "@edit.synthesis": "report.md",
    "@edit.plan": "plan.md",
    "@edit.codebase": "appendices/codebase.md",
    "@edit.ecosystem": "appendices/ecosystem.md",
    "@edit.contracts": "appendices/contracts.md",
    "@edit.risks": "appendices/risks.md",
    "@edit.validation": "appendices/validation.md",
    "@edit.responses": "appendices/responses.md",
}
SAFE_ID = re.compile(r"[A-Za-z0-9_-]+\Z")
MAX_DOCUMENT = 16 * 1024 * 1024
MAX_TOTAL = 64 * 1024 * 1024


def read_bounded(path, limit):
    if path.is_symlink() or not path.is_file():
        raise ValueError("required research source is missing or is a symlink")
    with path.open("rb") as stream:
        data = stream.read(limit + 1)
    if len(data) > limit:
        raise ValueError("research source exceeds publication limit")
    return data


def publish(run_dir, destination):
    if not run_dir or not destination:
        raise ValueError("publication requires a persisted run and destination")
    run = Path(run_dir).absolute()
    if not SAFE_ID.fullmatch(run.name):
        raise ValueError("invalid run identifier")
    # Select by journal order, never directory mtime or lexical round ordering.
    latest = None
    for line in read_bounded(run / "journal.jsonl", 32 * 1024 * 1024).splitlines():
        event = json.loads(line)
        data = event.get("data", {})
        if data.get("StepID") == "review" and event.get("kind") in (
            "review_request", "review_submitted"
        ):
            latest = event
    if latest is None or latest["kind"] != "review_submitted":
        raise ValueError("research has no completed review")
    approval = latest["data"]
    if approval.get("Verdict") != "approve" or approval.get("RunID") != run.name:
        raise ValueError("latest research review is not an approval for this run")
    round_id = approval.get("RoundID", "")
    if not SAFE_ID.fullmatch(round_id):
        raise ValueError("invalid review round identifier")
    review = run / "steps" / "review" / "review" / round_id
    # Reject directory symlinks too, so approved sources stay inside this run.
    for path in (run, run / "steps", run / "steps/review",
                 run / "steps/review/review", review, review / "documents"):
        if path.is_symlink():
            raise ValueError("research source directories must not be symlinks")
    submission = json.loads(read_bounded(review / "submission.json", 1024 * 1024))
    if (submission.get("schema_version") != 1
            or submission.get("step_id") != "review"
            or submission.get("round_id") != round_id
            or submission.get("verdict") != "approve"):
        raise ValueError("review submission does not match the approval")

    contents = {}
    records = []
    seen_ids = set()
    total = 0
    for document in submission["documents"]:
        source, doc_id = document["source"], document["id"]
        if source not in DOCUMENTS or not SAFE_ID.fullmatch(doc_id) or doc_id in seen_ids:
            raise ValueError("unexpected or duplicate review document")
        seen_ids.add(doc_id)
        target = DOCUMENTS[source]
        if target in contents or document.get("format") != "markdown":
            raise ValueError("duplicate source or unsupported review document format")
        content = read_bounded(review / "documents" / (doc_id + ".md"), MAX_DOCUMENT)
        total += len(content)
        if total > MAX_TOTAL:
            raise ValueError("research bundle exceeds publication limit")
        digest = hashlib.sha256(content).hexdigest()
        if digest != document["sha256"]:
            raise ValueError("approved document digest mismatch")
        if not content.decode("utf-8").strip():
            raise ValueError("approved document is empty")
        contents[target] = content
        records.append({"path": target, "source": source, "sha256": digest})
    if set(contents) != set(DOCUMENTS.values()):
        raise ValueError("approved research is missing a required document")

    parent = Path(destination).absolute()
    final = parent / run.name
    if final.exists() or final.is_symlink():
        raise ValueError("research output already exists; refusing to overwrite")
    parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=".research-", dir=parent))
    reserved = False
    try:
        for name, content in contents.items():
            path = staging / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
        manifest = {"format_version": 1, "run_id": run.name, "review_round": round_id,
                    "verdict": "approve", "documents": records}
        (staging / "manifest.json").write_text(
            json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        links = "\n".join(f"- [{name}]({name})" for name in DOCUMENTS.values())
        (staging / "README.md").write_text(
            f"# Approved research\n\nRun: `{run.name}`  \nReview round: `{round_id}`\n\n"
            "These documents reproduce the approved Markdown snapshots exactly.\n"
            "The plan is proposed; research approval does not authorize implementation.\n\n"
            f"{links}\n\nSource-relative links retain their original context; "
            "consult the researched project when following repository links.\n",
            encoding="utf-8")
        # Reserve the name exclusively, then replace only our own empty directory.
        final.mkdir()
        reserved = True
        os.replace(staging, final)
        reserved = False
    finally:
        if reserved:
            final.rmdir()
        if staging.exists():
            shutil.rmtree(staging)
    return final


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-dir", required=True)
    parser.add_argument("--destination", required=True)
    args = parser.parse_args()
    try:
        result = publish(args.run_dir, args.destination)
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f"Research publication failed: {error}", file=sys.stderr)
        return 1
    print(f"Published approved research: {result / 'README.md'}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
