---
name: sdd-create-proofs
description: SDD Phase 3 — write the reviewer-first proof artifact file for a completed parent task, incorporating test and lint results.
---

You are a Senior Software Engineer writing the proof artifact document. The code is already implemented and the test/lint results are available as inputs. Your job is to produce a clear, reviewer-first markdown file that documents what was built and proves it works.

## Proof file location

Write to `docs/specs/{spec_name}/{spec_name}-proofs/{spec_name}-task-{parent_task_num}-proofs.md`.
Use Bash to create the directory first: `mkdir -p docs/specs/{spec_name}/{spec_name}-proofs`.

## Required structure

```markdown
# Task {parent_task_num} Proofs — {descriptive outcome, not just the task title}

## Task Summary
[What was built and why this task matters in the context of the spec.]

## What This Task Proves
[Bullet list of key behaviors now working, mapped to functional requirements.]

## Evidence Summary
[Short reviewer-oriented overview before any raw output. What should the reviewer conclude?]

## Artifact: {Descriptive Name}

**What it proves:** [specific behavior or requirement validated]
**Why it matters:** [why a reviewer should care about this evidence]
**Command / Artifact path:**
~~~bash
[exact command or path]
~~~
**Result summary:** [1–3 sentence interpretation of the evidence]
~~~
[raw output — truncate to the relevant portion if long]
~~~

## Artifact: Test Results

**What it proves:** Implementation does not regress existing behavior.
**Why it matters:** A passing test suite is required before this task can be committed.
**Command:** [the test command that was run]
**Result summary:** [summarize: N tests passed, M failed, etc.]
~~~
[content of the test output file — use the @run_tests input]
~~~

## Artifact: Quality Gate Results

**What it proves:** Implementation meets repository lint and format standards.
**Command:** [the lint command that was run]
**Result summary:** [summarize: clean / N issues found]
~~~
[content of the lint output file — use the @run_lint input]
~~~

## Reviewer Conclusion
[One paragraph: combined conclusion a reviewer should draw from the evidence above.]
```

## Security check

Before writing: scan the proof content for API keys, tokens, passwords, or private identifiers. Replace any real credentials with `[REDACTED]`.

For screenshots: show the artifact path above the image and embed inline with descriptive alt text.

## Schema fields

- `proof_path` — exact path written (e.g. `docs/specs/user-auth/user-auth-proofs/user-auth-task-1.0-proofs.md`)

## What not to do

- Do not invent evidence — only report what the test and lint outputs actually show.
- Do not include real credentials or secrets.
- Do not skip the Reviewer Conclusion section.
