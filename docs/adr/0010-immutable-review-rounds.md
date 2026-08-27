# ADR 0010: Immutable review rounds and sidecar feedback

## Status

Accepted

## Context

Review targets can be generated artifacts, dependency diffs, or literal files.
Reviewers need stable line anchors while the workflow continues to run, and a
review may contain several documents that must be acknowledged together.

## Decision

At dispatch, jig writes each target into an immutable, round-specific snapshot
and records its digest in the review descriptor. The review workspace is
read-only: comments, suggestions, view state, and the summary are persisted as
a draft sidecar, while the final verdict and all annotations are submitted as
one structured batch. The engine verifies the round ID, document set, digests,
anchors, and acknowledgements before accepting it.

## Consequences

Source files are never edited by the reviewer and historical rounds remain
inspectable. Feedback can be rendered deterministically for loop consumers,
stale or incomplete submissions fail closed, and persistence-off runs retain
the same in-memory semantics without pretending a file was written.
