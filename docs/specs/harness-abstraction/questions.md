# Clarifying Questions — harness-abstraction

**Status: BLOCKED — clarification insufficient.** Answer inline under each question
(edit this file), then resume. Recommendations are biased toward the smallest,
most reviewable slice.

Two decisions are genuinely blocking (Q1, Q2). Q3 and Q4 only matter depending on
how Q1 is answered — answer them only if their scope is pulled into this spec.

---

## Q1 — Which slice does THIS spec cover?

`scope_assess` sized the full effort as **too_large** and recommended a 3-way split.
`write_spec` produces exactly one spec, so we must pick which one.

- **(A) Foundational seam extraction only — RECOMMENDED.** Define the jig-owned
  `Harness` interface + capability/degradation model, refactor the 757-LOC
  `AgentExecutor` behind it, and normalize the SDK shapes leaking into
  `step.Result` / `engine.StepRequest` — while keeping Claude as the *sole*
  implementation. **No `harness` TOML selector, no second backend.** Zero
  user-visible behavior change; smallest reviewable slice; de-risks B and C.
- **(B) A + selector + second backend.** Adds the `harness` field to the TOML
  precedence chain and builds a concrete second backend. Larger; forces committing
  to the second harness now (see Q2).
- **(C) A + B + tail.** Also migrates the Tier-2 `MonitorAdapter` security path
  through the Harness and resolves the dead frontend chat. Largest.

**Recommendation:** **(A).** Matches the scope split and gives a clean,
behavior-preserving foundation to review before any backend or schema change.

**Your answer:**

---

## Q2 — What is the concrete second harness?

Named "the biggest open ambiguity" by both context and research steps. Even for
slice (A), we need a *validation target* to prove the interface isn't Claude-shaped
(design against it; don't build it). For (B)/(C) we actually build it.

- **Claude Code CLI (`claude -p`) — RECOMMENDED.** Same vendor, different transport
  (subprocess + stream-json vs in-process SDK). Lowest risk; still exercises the
  degradation model (MCP over stdio, tool approval as allow-list not synchronous
  callback) without a new vendor's semantics.
- **OpenAI Agents SDK Go port** (nlpodyssey / MitulShah1). True vendor-agnostic
  proof, but more unknowns: community-port maturity, different message model,
  structured-output semantics.
- **Defer — design-only target.** Design the interface against the capability
  matrix without committing to a backend. Acceptable *only* if Q1 = (A); accepts
  re-validation risk when the real backend lands.

**Recommendation:** **Claude Code CLI** as the design/validation target (and the
first real backend if Q1 ≥ B).

**Your answer:**

---

## Q3 — Harness selection surface in TOML *(only if Q1 includes B)*

- **Full precedence chain — RECOMMENDED.** `harness` resolvable at
  step > agent_file > profile > `[defaults]`, defaulting to `claude` for backward
  compat. Matches the existing field-resolution model; existing TOMLs keep working.
- **`[defaults]` only** (whole-workflow harness). Simplest, but can't mix backends
  per step.
- **Per-step only.** Most flexible, loses the global-default ergonomics.

**Recommendation:** **Full precedence chain**, default `claude`.

**Your answer:**

---

## Q4 — Dead frontend chat: `tui/client.go` + `tui/chat.go` *(only if Q1 includes C)*

Unreachable dead code today (only `chat_test.go` references it; the live entry path
never touches it).

- **Delete — RECOMMENDED.** Free coupling removal; two of the four SDK-coupled
  files disappear.
- **Port behind Harness** as a second consumer. Only if streaming chat is a planned
  product surface.

**Recommendation:** **Delete**, unless streaming chat is on the roadmap.

**Your answer:**
