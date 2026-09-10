# 22 Questions Round 1 — `jig init` / workflow scaffold (A20)

Source goal: [`docs/plans/open-goals.md`](../../plans/open-goals.md) — **A20** (P2, Kind **C**):
“`jig init` / workflow scaffold — Hand-authored TOML.”

Please answer each question below (tick one or more options, or write your own
note under the question). Anything you leave blank I will treat as “take the
recommendation.”

---

## 1. What does `jig init` actually create?

The goal line says “workflow scaffold,” which could mean three materially
different products. This choice drives every other section of the spec.

- [ ] (A) **Project bootstrap** — create the whole jig-shaped layout in a repo that has none: `.agents/jig/` with one runnable starter workflow, `.agents/skills/<name>/SKILL.md` stubs the workflow references, and `.jig/` housekeeping (`.gitignore` entry / `tui.json`). One command, cold start to `jig validate` passing.
- [ ] (B) **Single workflow scaffold** — assume `.agents/jig/` may or may not exist; write exactly one new `<name>.toml` from a named template into `.agents/jig/`, and nothing else. Skills are the author’s problem.
- [ ] (C) **Both, as one command with a mode** — `jig init` (bootstrap, A) and `jig init workflow <name>` (single file, B) as two subcommands sharing one template engine.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- The stated pain in A20 is *cold-start friction* (“Hand-authored TOML”), and a
  workflow alone is not runnable — `skill = "skills/plan"` resolves to a file
  that must exist, so `(B)` produces a scaffold that fails `jig validate`
  immediately unless the template is agent-free. `(A)` is the only option that
  guarantees “init → validate → run” works on a bare repo.
- `(C)` is attractive but doubles the surface (two command shapes, two
  idempotency stories, two proof sets) for a P2 goal. If you want `(C)`
  eventually, `(A)` is the strictly better first slice and `init workflow` can
  be a follow-on spec.
- `(B)` is the smallest, and is the right answer *only* if you expect the
  typical user to already have `.agents/` from cloning this repo or another
  jig project.

---

## 2. Interactive or non-interactive?

jig already has a Bubble Tea TUI and a headless CLI split (`jig run --ci`,
`docs/headless.md`). Where does `init` sit?

- [ ] (A) **Flag-driven only, never prompts** — `jig init [--template NAME] [--name NAME] [--force] [--dry-run]`. Safe in CI and in agent hands; no TTY assumptions.
- [ ] (B) **Prompting when a TTY is present, flag-driven otherwise** — ask for workflow name and template interactively, fall back to flags/defaults when piped or `--ci`.
- [ ] (C) **A TUI screen** — a new “New workflow” affordance on the Home screen that writes the scaffold, plus a thin CLI wrapper.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- Every existing jig subcommand (`validate`, `run`, `status`, `logs`, `doctor`,
  `resume`, `reset`, `prune`) is flag-driven, stdout-for-machines, and exits
  with the documented 0/1/2 code table in [`docs/operations.md`](../../operations.md).
  `(A)` inherits that contract for free and keeps the proof artifacts to plain
  captured terminal output.
- `(B)` requires a TTY-detection path plus a second set of tests for the
  non-TTY branch, and it is the classic source of “hangs in CI” bugs.
- `(C)` is a real product idea (it pairs with B-section TUI goals) but it is a
  different spec: it touches `internal/tui/home.go` and the selector, and its
  proof artifacts are screenshots rather than CLI output.

---

## 3. Where do the templates live and how many ship?

- [ ] (A) **Embedded in the binary via `go:embed`, one template: `starter`** — a minimal agent → review → command chain exercising the constructs a beginner needs. `jig init` with no flags emits it.
- [ ] (B) **Embedded, a small named set** — e.g. `minimal` (command-only, no model calls, like `.agents/jig/golden-path.toml`), `starter` (agent + review + gate), `sdd`-shaped (multi-phase). `--template NAME`, `--list-templates`.
- [ ] (C) **Read from the repo tree** — copy from `.agents/jig/` / `.agents/jig/templates/` at runtime rather than embedding.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(B), with `minimal` as the default]

**Why these are recommended:**

- Embedding (`go:embed`) is required for a real `go install`/brew binary to work
  outside this repo — `(C)` only works when you are standing inside a jig
  checkout, which defeats the purpose of `init`. Note `.agents/jig/templates/`
  today holds one *prompt* file (`context-assess.md`), not workflow templates,
  so `(C)` has no existing source to copy from anyway.
- Defaulting to a **`minimal`, command-only** template means `jig init && jig
  validate && jig run` succeeds with **no API key and no token spend** — that is
  a far stronger first-run experience and a far cleaner proof artifact than a
  template that immediately needs a Claude credential.
- Two or three templates is cheap once the embed + render machinery exists; a
  single template `(A)` would likely get a follow-up request within a week.
- If you disagree on the default, the substantive question is: should the very
  first `jig run` after `init` spend tokens? I recommend no.

---

## 4. Does the scaffold include skill files, and do templated agent steps have real prompts?

Only relevant if you picked (A) or (C) in Q1.

- [ ] (A) **Yes — emit `SKILL.md` stubs** for every `skill = …` the template references, with front-matter matching the repo convention (`name:`, `description:`, `disable-model-invocation: true`) and a TODO body.
- [ ] (B) **No skills; templates use `agent_file` / inline prompts only**, so nothing outside `.agents/jig/` is written.
- [ ] (C) **No agent steps in the default template at all** — the default scaffold is command/review only, and agent templates (which do emit skill stubs) are opt-in via `--template`.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A) combined with (C)]

**Why these are recommended:**

- These compose: default to a command/review template that needs no skills
  `(C)`, and when the user asks for an agent template, emit the matching
  `SKILL.md` stubs `(A)` so the result still passes `jig validate`.
- A scaffold that emits a dangling `skill = "skills/plan"` reference is worse
  than no scaffold: it fails validation on first contact and teaches the user
  that jig is broken rather than that their skill is missing.
- `(B)` avoids writing outside `.agents/jig/` but pushes the author toward
  inline prompts, which is not the pattern the rest of this repo demonstrates.

---

## 5. What happens when files already exist?

- [ ] (A) **Refuse and exit non-zero on any collision**, listing every path that would be overwritten; `--force` overwrites; `--dry-run` prints the plan and writes nothing.
- [ ] (B) **Merge/skip** — create only the missing files, silently leave existing ones alone, report a per-path created/skipped summary, exit 0.
- [ ] (C) **Always write; back up collisions** to `<path>.bak` before overwriting.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `jig reset` already establishes the repo’s house style for destructive ops:
  preview is the default and read-only, and applying requires explicit signals.
  Fail-closed-plus-`--force` is the same instinct and will not surprise anyone.
- `(B)` is friendlier but silently ambiguous — a partially-scaffolded directory
  is exactly the state where “skipped” and “created” get conflated and the user
  ends up with a workflow referencing a skill that init decided not to write.
- `(C)` litters the tree with `.bak` files that git will then want to track.

---

## 6. Is the scaffold self-verifying?

- [ ] (A) **Yes — `init` runs the same load+validate path as `jig validate` on what it just wrote**, and fails loudly (non-zero, with the paths it created) if the output does not validate. A test asserts every embedded template validates.
- [ ] (B) **No runtime check; a unit test asserts each embedded template validates**, and `init` just prints “now run `jig validate <path>`”.
- [ ] (C) **Also run `jig doctor`** after scaffolding to report backend/tooling readiness.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- CLAUDE.md’s standing rule is “fail at parse time, not run time,” and
  validating in-process costs one `workflow.Load` call. It also makes the proof
  artifact trivially strong: the command’s own output proves the scaffold is
  valid.
- `(B)`’s test-only check cannot catch a bug in the *rendering* (name
  substitution, path joining) that only manifests on real user input.
- `(C)` is a nice extra but couples `init` to backend/executable discovery and
  would make `init` fail on a machine with no Claude CLI installed — wrong
  signal for a scaffolding command. Better as a printed hint: “next: `jig
  doctor`”.

---

## 7. Anything explicitly out of scope you want named?

Tick anything you want me to write into **Non-Goals** so it does not creep in:

- [ ] Git initialization / first commit / `.gitignore` authoring beyond a single `.jig/` line
- [ ] Interactive template *authoring* (a wizard that builds a custom DAG step by step)
- [ ] Remote / registry templates (`jig init --from github.com/…`)
- [ ] Migrating or upgrading an existing workflow to a newer schema version
- [ ] Installing backends, credentials, or `mise`/Go toolchain setup
- [ ] Other (describe)

**Recommended answer(s):** [all five]

**Why these are recommended:**

- Each is a plausible reading of “scaffold” that would balloon a P2 item, and
  naming them costs nothing. Remote templates in particular are a supply-chain
  surface (arbitrary TOML + shell `run =` strings from the network) that
  deserves its own security review, not a footnote in this spec.

---

## Notes on process

- **Scope assessment:** with the recommended answers this is **just right** for
  SDD — one new CLI subcommand with embedded assets, validation, and a
  documented contract, comparable in size to the `prune`/`validate` surface. If
  you pick Q2 `(C)` (a TUI screen), it becomes **two** specs and I will split it.
- **Latest-standards research:** none was material. The feature is entirely
  repo-internal — Go 1.25 `embed` for template assets and this repo’s own TOML
  schema and CLI exit-code table. No external vendor guidance changes the
  design. I will say so explicitly in the spec.

When you have filled this in, reply **“answers are in”** (or just
`Continue SDD`) and I will re-run the sufficiency check and write
`22-spec-jig-init-scaffold.md`.

---

## Answers (confirmed 2026-09-10)

The user accepted **every recommendation as written**:

1. **(A)** Project bootstrap — `.agents/jig/` workflow + skill stubs + `.jig/` housekeeping.
2. **(A)** Flag-driven only; never prompts; no TTY assumptions.
3. **(B)** Embedded via `go:embed`, named set, **`minimal` is the default**.
4. **(A)+(C)** Default template is command/review (no skills); agent templates emit `SKILL.md` stubs.
5. **(A)** Refuse on collision + `--force` + `--dry-run`.
6. **(A)** `init` self-validates through the real `workflow.Load` path.
7. **All five** named as Non-Goals.

One bounded interpretation made while writing the spec, flagged for the record:
Q3(B) listed three example templates. The spec ships **exactly two**
(`minimal`, `starter`) and makes the registry a data-only extension point; the
multi-phase `sdd`-shaped template is recorded as a Non-Goal / follow-on rather
than authored here, because it is a large asset to write and keep valid.
