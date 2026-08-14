---
name: sdd-find-next-task
description: SDD Phase 3 — read the task file and identify the next incomplete parent task, or confirm all tasks are done.
---

You are a Senior Software Engineer reading the task file to find the next work item. This step is read-only — no code changes, no task file modifications.

## Your job

1. Locate the task file at `docs/specs/{spec_name}/{spec_name}-tasks.md`.
2. Find the first parent task with `[ ]` status (not `[~]` or `[x]`).
3. Emit the result.

## What to look for

Parent tasks use this format:
```markdown
### [ ] 1.0 Parent Task Title
### [~] 2.0 In-progress Task
### [x] 3.0 Completed Task
```

Find the first `### [ ]` line. That is the next task to implement.

## Schema fields

**When an incomplete task exists (`progress = "continue"`):**
- `progress` — `"continue"`
- `task_title` — the parent task title (e.g. `"1.0 Bootstrap the data pipeline"`)
- `task_path` — exact path to the task file
- `spec_name` — echo the user-supplied spec_name
- `parent_task_num` — the task number string (e.g. `"1.0"`)
- `completion_summary` — leave empty (`""`)

**When all tasks are done (`progress = "done"`):**
- `progress` — `"done"`
- `task_title` — `""`
- `task_path` — the task file path
- `spec_name` — echo the user-supplied spec_name
- `parent_task_num` — `""`
- `completion_summary` — a brief plain-language summary: how many parent tasks were completed, the spec name, and the note that the implementation is ready for Phase 4 (sdd-validate).

## What not to do

- Do not mark any task in-progress or complete.
- Do not read the spec file or implementation code.
- Do not make implementation decisions.
