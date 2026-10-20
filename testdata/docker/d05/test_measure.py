import contextlib
import io
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import measure


class MeasurementSafetyTests(unittest.TestCase):
    def test_archive_limit_precedes_unbounded_read(self):
        stream = io.BytesIO(b"12345")
        reader = measure.BoundedArchive(stream)
        reader.remaining = 3
        with self.assertRaisesRegex(RuntimeError, "archive exceeds"):
            reader.read(1000000)
        self.assertEqual(stream.tell(), 4)

    def test_lost_create_response_reconciles_or_retains_staging(self):
        for inspect_fails in (False, True):
            with self.subTest(inspect_fails=inspect_fails), tempfile.TemporaryDirectory() as parent:
                root = Path(parent) / "staging"
                root.mkdir()
                calls = []

                def command(args, **kwargs):
                    calls.append(args)
                    if args[1:3] == ["image", "inspect"]:
                        return subprocess.CompletedProcess(args, 0, stdout="sha256:fixture\n")
                    if args[1] == "create":
                        raise subprocess.TimeoutExpired(args, 30)
                    if args[1] == "inspect":
                        if inspect_fails:
                            raise subprocess.TimeoutExpired(args, 30)
                        return subprocess.CompletedProcess(args, 0, stdout="a" * 64 + " " + measure.NAME)
                    if args[1] == "rm":
                        self.assertEqual(args[-1], "a" * 64)
                        return subprocess.CompletedProcess(args, 0)
                    self.fail("unexpected command")

                with patch.object(measure, "command", command), patch.object(measure, "source_archive", lambda: contextlib.nullcontext([])), patch.object(measure.tempfile, "mkdtemp", return_value=str(root)), contextlib.redirect_stdout(io.StringIO()):
                    expected = RuntimeError if inspect_fails else subprocess.TimeoutExpired
                    with self.assertRaises(expected):
                        measure.main()
                self.assertEqual(root.exists(), inspect_fails)
                self.assertEqual(any(args[1] == "rm" for args in calls), not inspect_fails)


if __name__ == "__main__":
    unittest.main()
