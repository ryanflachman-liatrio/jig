"""Synthetic publication contract tests; never reads real .jig data."""

import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from publish_research import DOCUMENTS, publish


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.run = self.root / "runs" / "synthetic-run"
        self.review = self.run / "steps/review/review/g000-i001"
        self.documents = self.review / "documents"
        self.documents.mkdir(parents=True)
        self.destination = self.root / "research-output"
        self.approval = {"kind": "review_submitted", "data": {
            "StepID": "review", "RunID": self.run.name,
            "RoundID": "g000-i001", "Verdict": "approve"}}
        self.write_journal([self.approval])
        self.submission = {"schema_version": 1, "step_id": "review",
                           "round_id": "g000-i001", "verdict": "approve",
                           "documents": []}
        for index, source in enumerate(DOCUMENTS):
            doc_id = f"document-{index}"
            content = f"# Synthetic {source}\n\nEvidence and proposed next steps.\n".encode()
            (self.documents / (doc_id + ".md")).write_bytes(content)
            self.submission["documents"].append({"id": doc_id, "source": source,
                "format": "markdown", "sha256": hashlib.sha256(content).hexdigest()})
        self.write_submission()

    def write_journal(self, events):
        (self.run / "journal.jsonl").write_text(
            "\n".join(json.dumps(event) for event in events) + "\n", encoding="utf-8")

    def write_submission(self):
        (self.review / "submission.json").write_text(
            json.dumps(self.submission), encoding="utf-8")

    def test_publishes_exact_approved_bytes_and_provenance(self):
        output = publish(str(self.run), str(self.destination))
        manifest = json.loads((output / "manifest.json").read_text())
        self.assertEqual(manifest["review_round"], "g000-i001")
        self.assertEqual(len(manifest["documents"]), 8)
        for document in self.submission["documents"]:
            original = (self.documents / (document["id"] + ".md")).read_bytes()
            self.assertEqual((output / DOCUMENTS[document["source"]]).read_bytes(), original)
        self.assertIn("plan.md", (output / "README.md").read_text())
        # The deliverable remains after the synthetic run's source is removed.
        import shutil
        shutil.rmtree(self.run)
        self.assertTrue((output / "report.md").is_file())

    def test_latest_review_must_be_approved(self):
        for kind, verdict in (("review_submitted", "revise"), ("review_request", "approve")):
            with self.subTest(kind=kind):
                later = {"kind": kind, "data": dict(self.approval["data"], Verdict=verdict)}
                self.write_journal([self.approval, later])
                with self.assertRaises(ValueError):
                    publish(str(self.run), str(self.destination))
                self.assertFalse(self.destination.exists())

    def test_altered_document_produces_no_bundle(self):
        (self.documents / "document-0.md").write_text("Unapproved replacement")
        with self.assertRaisesRegex(ValueError, "digest mismatch"):
            publish(str(self.run), str(self.destination))
        self.assertFalse(self.destination.exists())

    def test_missing_plan_is_rejected(self):
        self.submission["documents"] = [doc for doc in self.submission["documents"]
                                         if doc["source"] != "@edit.plan"]
        self.write_submission()
        with self.assertRaisesRegex(ValueError, "missing a required document"):
            publish(str(self.run), str(self.destination))
        self.assertFalse(self.destination.exists())

    def test_snapshot_paths_cannot_escape(self):
        self.submission["documents"][0]["id"] = "../../private"
        self.write_submission()
        with self.assertRaises(ValueError):
            publish(str(self.run), str(self.destination))

    def test_symlink_document_is_rejected(self):
        path = self.documents / "document-0.md"
        content = path.read_bytes()
        path.unlink()
        outside = self.root / "outside.md"
        outside.write_bytes(content)
        path.symlink_to(outside)
        with self.assertRaisesRegex(ValueError, "symlink"):
            publish(str(self.run), str(self.destination))

    def test_empty_persistence_is_rejected_without_relative_writes(self):
        with self.assertRaisesRegex(ValueError, "persisted run"):
            publish("", str(self.destination))
        self.assertFalse(self.destination.exists())

    def test_existing_bundle_is_not_overwritten(self):
        output = publish(str(self.run), str(self.destination))
        (output / "report.md").write_text("local annotation")
        with self.assertRaisesRegex(ValueError, "already exists"):
            publish(str(self.run), str(self.destination))
        self.assertEqual((output / "report.md").read_text(), "local annotation")


if __name__ == "__main__":
    unittest.main()
