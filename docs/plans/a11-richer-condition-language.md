# Implementation Plan: Richer condition language (A11)

**Status:** Planned — goal A11 (P1)
**Risk:** **high** — the syntax is small, but conditions participate in workflow
validation, module expansion, scheduler dispatch, check applicability,
agent-input parking, bounded route selection, and chart rendering. A parser or
static-analysis mistake can silently run, skip, or rewind the wrong step.
**Depends on:** Typed scalar verdicts and structured-output fields
(`internal/workflow`); load-time graph validation; bounded and mutually exclusive
`[[step.route]]` semantics; module namespacing/export rewriting.
**Complements:** A8 dynamic fan-out aggregate guards; A12 settled-run reset; the
check/remediation contract in `docs/workflow-schema.md`.
**Breaks:** No documented condition should break. The implementation replaces
the flat `Condition{Step, Field, Op, Value}` representation and its ad hoc parser
with an expression tree; this is an internal pre-v1 API change and gets no
compatibility wrapper.

---

## Summary

Replace the one-predicate guard parser with a typed, bounded expression AST that
supports `&&`, `||`, parentheses, and numeric `<`, `<=`, `>`, `>=` comparisons
in every existing condition-bearing field. The top risk is preserving jig's
load-time guarantees for dependency references and mutually exclusive bounded
routes while allowing a condition to mention more than one value source.

## Approach

Build a dependency-free lexer, recursive-descent parser, formatter, and reference
visitor in `internal/workflow/condition.go`; make validation walk every predicate
and type-check it against `OutputType` or `Step.ReferenceSchema()` before changing
runtime behavior. Next, recursively evaluate the checked AST in
`scheduler.evalGuard`, with short-circuit boolean semantics and type-directed
numeric comparison. Then migrate module reference rewriting and the chart from
their current single-reference assumptions, update the conservative route safety
proofs, and finish with reference docs plus dogfood workflows. The key design
decision is to keep this a deliberately small declarative language: references
compare only to literals, numeric fields alone admit ordering, and no general
expression evaluator or third-party scripting engine enters the deterministic
core.

## Problem

Today `ParseCondition` accepts exactly one of:

```toml
when = "ready"
when = "review == 'approve'"
when = "research.status != 'blocked'"
```

That forces workflow authors either to add artificial aggregation steps or to
weaken a guard when execution depends on several already-typed facts. It also
prevents useful threshold gates over `number` fields and the A8 aggregate fields,
such as requiring at least one successful runtime child.

The current flat representation is assumed in more places than the parser:

- `validator.checkWhen`, `checkCheck`, `checkAgent`, and `checkRoutes` validate
  one `cond.Step` and one comparison;
- route exclusivity, exhaustiveness, and automatic check-remediation recognition
  inspect one `CondOp`/`Value` pair;
- `scheduler.evalGuard` resolves one verdict or structured field;
- module expansion rewrites or prefixes only one reference and only adds one
  implicit dependency after an export is resolved;
- `internal/tui/chart` decorates at most one incoming dependency edge and formats
  one predicate.

A parser-only change would therefore appear to work in trivial workflows while
misvalidating modules, routes, or graph presentation.

## Goals

1. Support conjunction, disjunction, explicit grouping, and numeric equality and
   ordering without permitting arbitrary code execution.
2. Apply the same grammar and type rules to `step.when`, `step.applies_when`,
   `step.block_on`, and `step.route.when`.
3. Discover and validate every reference in a compound expression before a run
   starts.
4. Preserve short-circuit, deterministic runtime evaluation against the same
   scalar verdicts and structured JSON already used today.
5. Preserve the bounded-route invariant: multiple guarded routes must be proven
   pairwise disjoint; a missing fallback is allowed only when finite own-output
   coverage can be proven.
6. Rewrite every reference correctly through nested subworkflow expansion and
   combine module invocation/root guards without losing either predicate.
7. Keep the chart honest when one guard depends on two or more incoming edges.
8. Preserve all currently documented truthy, `==`, and `!=` expressions,
   including quoted boolean spellings already used by `.agents/jig/feature.toml`.

## Non-goals

- Arithmetic, string concatenation, functions, regexes, collection operators,
  membership (`in`), null checks, or environment/file access.
- Reference-to-reference comparisons such as `a.count > b.count`; the right side
  remains a literal, which keeps dependency discovery and type checking simple.
- Unary `!`. Authors can use `field == false` or `field != true`; it can be added
  later without conflating it with `!=` in the first lexer revision.
- Ordering strings or enums. `<`, `<=`, `>`, and `>=` are number-only.
- Indexing lists or dynamically addressing A8 children. Existing dotted object
  fields and the family's scalar aggregate fields remain the addressable surface.
- Conditional `depends_on` edges. All dependencies are still satisfied before
  the complete guard is evaluated.
- A general SAT/SMT solver. Route safety uses sound, intentionally conservative
  proofs and rejects shapes it cannot prove safe.
- Schema, journal, snapshot, or transcript format changes. Workflow snapshots
  already persist the raw condition strings and are reparsed on load/resume.

## Authoring contract

### Grammar

```ebnf
expression  = or-expression ;
or-expression = and-expression, { "||", and-expression } ;
and-expression = primary, { "&&", primary } ;
primary     = predicate | "(", expression, ")" ;
predicate   = reference, [ comparison, literal ] ;
comparison  = "==" | "!=" | "<" | "<=" | ">" | ">=" ;
reference   = identifier, { ".", identifier } ;
literal     = quoted-string | bare-token ;
```

Precedence is comparison/truthiness first, then `&&`, then `||`. Both boolean
operators associate left-to-right; parentheses override precedence.

Examples:

```toml
# Both predecessors must authorize the step.
when = "qa.passed && plan_review == 'approve'"

# Either terminal review verdict allows notification.
when = "final_review == 'ship' || final_review == 'hold'"

# A8 aggregate threshold with explicit grouping.
when = "analyze.all_succeeded && (analyze.count >= 1 || allow_empty)"

# A self-only compound pause condition.
block_on = "research.status == 'blocked' && research.needs_input"

# One remediation route with an explicit fallback.
when = "quality == 'fail' || quality == 'error'"
```

Identifiers retain the existing letters/digits/underscore/hyphen rules. A
single- or double-quoted string may contain whitespace or operator characters;
the lexer must not split `&&`, `||`, or comparison text inside it. Bare tokens
remain accepted for existing expressions, but documentation should prefer
quoted text/enum values and bare `true`, `false`, and numbers.

The parser reports an offset and nearby token for malformed input: missing
operands, dangling operators, mismatched parentheses/quotes, unsupported `&` or
`|`, unknown operators, or trailing tokens. Cap nesting and total syntax nodes
at explicit package constants (proposed: 32 levels and 256 nodes) so validation
cannot be driven into unbounded recursion by an authored TOML string.

### Typed comparison rules

Every predicate is type-checked independently against its referenced source:

| Referenced value | Bare/truthy | `==`, `!=` | `<`, `<=`, `>`, `>=` |
|---|---|---|---|
| scalar `output_type = "bool"` | yes | `true` / `false` | no |
| scalar enum verdict | no | declared enum members | no |
| schema `bool` field | yes | `true` / `false` | no |
| schema enum field | no | declared enum members | no |
| schema `text` field | no | string literal | no |
| schema `number` field | no | finite numeric literal | finite numeric literal |
| opaque `FieldAny` scalar | no | `==` / `!=` only | no |
| list/object/artifact | no | no | no |

Quoted `"true"`/`'false'` for bool and quoted numeric spellings for number remain
accepted because the existing validator permits them; validation normalizes them
according to the left-hand type. Enum members that happen to be named `"true"`
remain strings and should be quoted to avoid ambiguity. Numeric parsing uses
`strconv.ParseFloat` semantics restricted to finite JSON-compatible forms; NaN,
infinities, empty values, and partially parsed numbers fail validation.

Comparisons do not perform cross-type coercion at runtime. The validator records
enough type information on each parsed predicate, or the evaluator resolves it
from the referenced workflow schema, so a JSON number is compared numerically,
not lexicographically (`10 > 2`, never `"10" < "2"`). Missing results, invalid
structured JSON, missing field paths, or non-scalar runtime values evaluate false
as the existing fail-closed path does; valid constrained outputs should not reach
those cases.

### Proposed AST and helpers

Replace the flat fields on `workflow.Condition` with a small expression tree.
Exact private field layout can follow Go ergonomics, but the public package
surface should provide these concepts:

```go
type Condition struct {
    Raw  string
    Root *ConditionExpr
}

type ConditionExpr struct {
    Op          CondOp
    Left, Right *ConditionExpr // CondAnd / CondOr
    Ref         ConditionRef   // truthy/comparison leaf
    Literal     ConditionLiteral
}

type ConditionRef struct {
    Step  string
    Field []string
}
```

Extend `CondOp` with `CondLT`, `CondLTE`, `CondGT`, `CondGTE`, `CondAnd`, and
`CondOr`. Keep `CondTruthy`, `CondEq`, and `CondNeq` so existing terminology and
tests remain clear.

Add focused helpers instead of letting consumers inspect tree layout ad hoc:

- `Condition.Predicates()` returns leaves in source order for validation.
- `Condition.ReferencedSteps()` returns stable, de-duplicated step IDs for
  dependency checks and chart decoration.
- `Condition.RewriteRefs(fn)` rewrites each `ConditionRef` recursively and
  returns a canonical, precedence-preserving string for module expansion.
- `Condition.String()` formats the AST with only the parentheses necessary to
  preserve semantics and safely requotes literals.

`ParseCondition` remains the entry point so callers do not acquire lexer/parser
details. Parsing validates syntax only; workflow-aware reference/type validation
stays in `validate.go`.

## Validation by condition surface

### `step.when`

- Every unique referenced step must exist and appear explicitly in the
  consumer's `depends_on`.
- Every leaf field path and literal/operator pair is checked.
- The entire expression must produce a boolean; there are no value-producing
  logical subexpressions.
- False skips the consumer once, preserving existing skip/cascade behavior.

### `step.applies_when`

- Apply the same all-references-in-`depends_on` rule.
- False still produces the check's successful `skip` verdict without launching
  its command.
- Keep the existing rule that `applies_when` is required for a `check` step.

### `step.block_on`

- Every predicate must reference the current agent step's own scalar/structured
  output; one self reference is not enough if another leaf names a different
  step.
- For an A8 child, resolve fields against `EffectiveSchema()` exactly as today,
  never the family's aggregate `ReferenceSchema()`.
- True still parks the completed agent session at `StatusNeedsInput`; resume
  re-evaluates the whole expression after the next structured response.

### `step.route.when`

- Every reference must be either the route-owning step or one of its declared
  dependencies.
- Duplicate detection uses the canonical formatted expression so whitespace and
  quote-style differences cannot create identical adjacent branches unnoticed.
- Route order remains presentation order, not precedence: pairwise exclusivity
  ensures at most one guarded route is true, then an optional final fallback is
  selected when none match.

## Route safety analysis

Richer expressions must not weaken the current "no declaration-order accident"
rule. Replace the single-leaf helpers with sound conservative analysis over the
AST.

### Pairwise disjointness

`conditionsDisjoint(a, b)` proves two guards cannot both hold:

- Atomic predicates on the same typed reference are contradictory for distinct
  equalities, equality versus inequality of the same value, and non-overlapping
  numeric points/ranges (`x < 5` versus `x >= 5`, `x == 3` versus `x > 4`).
- `A || B` is disjoint from `C` only if both `A` and `B` are disjoint from `C`.
- `A && B` is disjoint from `C` if either conjunct is provably disjoint from
  `C`; apply the symmetric rule when the compound expression is on the right.
- Anything not established by those rules returns "unknown/possibly overlap",
  never "disjoint".

Check every guarded-route pair. If any pair cannot be proven disjoint, retain the
existing `route guards are not mutually exclusive` load error and suggest using
one combined `||` route plus a fallback when that expresses the author's intent.
This accepts useful compound routing without importing a solver or pretending
unknown predicates are safe.

### Exhaustiveness and fallback reachability

For a bool or enum verdict of the route-owning step, enumerate its finite declared
domain and evaluate guards symbolically when every predicate references that one
verdict. This recognizes expressions such as
`gate == 'a' || gate == 'b'` plus `gate == 'c'` as exhaustive. If a guard refers
to another step, a structured field, a number/text value, or an opaque value,
coverage is not provable and multiple routes still require an explicit fallback.

Use the same finite-domain evaluator to reject a fallback proven unreachable.
Unknown coverage is allowed with a fallback because the fallback makes selection
total.

### Automatic check remediation

Replace `hasAutomaticCheckRemediation`'s direct `CondOp` inspection with a helper
that proves the guarded back-routes cover both `quality == 'fail'` and
`quality == 'error'` for the owning check. Accept at least:

- `quality != 'pass'` (the current documented shorthand; dispatched checks do
  not produce `skip`),
- `quality == 'fail' || quality == 'error'`, and
- two disjoint equality routes, one for each verdict.

A dependency-sensitive expression that cannot guarantee remediation for both
verdicts fails validation rather than making remediation conditional on unrelated
state.

## Runtime evaluation

`scheduler.evalGuard` becomes a recursive interpreter:

1. `CondAnd` evaluates left, returns false without resolving right when left is
   false, then evaluates right.
2. `CondOr` evaluates left, returns true without resolving right when left is
   true, then evaluates right.
3. A leaf resolves `Result.Verdict` for a scalar reference or walks the existing
   `scheduler.structured` cache for a field reference.
4. Truthy leaves require actual boolean true. Equality/inequality use the
   validated source type. Relational leaves parse the literal once and compare
   the JSON numeric value.
5. Any unavailable or malformed operand fails closed to false.

Keep parsing outside hot recursive branches: each call site parses one complete
condition before evaluation, as today. No condition AST needs to be journaled or
added to `workflow.Step`; raw TOML strings remain the durable authoring form.

The four call paths retain their current outcomes:

| Surface | False | True |
|---|---|---|
| `when` | transition step to `skipped` | make step eligible to dispatch |
| `applies_when` | succeed check with verdict `skip` | dispatch check command |
| `block_on` | finish agent step | park at `needs_input` |
| `route.when` | inspect next guarded route/fallback | record exactly one bounded route intent |

## Module and namespacing behavior

Module support must operate on every expression leaf:

- `rewriteModuleCondition` rewrites each public `module.export` reference to the
  internal producer/field bound by that export. One expression may resolve two
  exports to two different internal producers.
- After rewriting `when` and `applies_when`, module expansion adds every newly
  exposed producer to `depends_on` before replacing module dependencies with
  terminal nodes.
- `prefixCondition` prefixes every internal step reference while leaving parent
  references unchanged, including mixed expressions.
- `instSteps` combines a module invocation guard and an existing root guard as
  `(invocation) && (root)` instead of rejecting the combination. Formatting
  preserves the grouping even when either side contains `||`.
- `block_on` rewriting still must finish as a self-only expression after step ID
  prefixing; normal validation proves that invariant.
- Artifact exports remain illegal in predicates, leaf by leaf.

Add round-trip tests that parse the rewritten canonical text again. This catches
lost parentheses and quoting bugs before an expanded workflow reaches the
scheduler.

## Chart and operator presentation

The static chart remains a graph of unconditional `depends_on` edges with
conditional decoration:

- Mark every incoming edge whose dependency ID occurs anywhere in `step.when` as
  conditional. A two-source conjunction therefore shows both causal edges as
  guarded rather than arbitrarily marking the first.
- Use the canonical full expression as the edge label. Existing chart truncation
  remains responsible for long labels; do not invent a second abbreviation
  language.
- Format route back-edge labels from the full AST plus the existing `<=N` bound.
- Preserve authored raw condition text in the agent context preamble; it is useful
  operator evidence and requires no parser-specific changes in `internal/step`.

No new color or style is needed, so `internal/tui/shared/styles.go` should remain
unchanged.

## Ordered implementation tasks

Each task is confined to one package or file. Test work follows the substantive
change it proves.

| # | Task | Area | Estimate |
|---:|---|---|---:|
| 1 | Replace the flat condition parser with the bounded lexer/AST, precedence-aware formatter, ordered predicate/reference visitors, ref rewriter, and the new logical/relational operators | `internal/workflow/condition.go` | 45 min |
| 2 | Add table-driven parser tests for legacy forms, precedence, grouping, all operators, quoted operator text, hyphenated/dotted refs, canonical formatting, rewrite round-trips, malformed tokens, and depth/node limits | `internal/workflow/condition_test.go` | 40 min |
| 3 | Refactor `checkWhen`, `checkAgent`, `checkCheck`, and `checkRoutes` to validate every predicate/reference and enforce the surface-specific dependency/self rules with type-directed literals | `internal/workflow/validate.go` | 45 min |
| 4 | Implement conservative AST route disjointness, finite bool/enum exhaustiveness, fallback reachability, and fail/error remediation coverage in the validation layer | `internal/workflow/validate.go` | 40 min |
| 5 | Add valid/invalid workflow tests across `when`, `applies_when`, `block_on`, and `route.when`, including multi-dependency refs, number thresholds, illegal type/operator pairs, overlapping compound routes, exhaustive enum disjunctions, and remediation coverage | `internal/workflow/workflow_test.go` | 45 min |
| 6 | Rewrite/prefix all condition leaves during module expansion, add all exposed dependencies, and combine invocation/root guards with grouping | `internal/workflow/module.go` | 35 min |
| 7 | Add module tests for multiple exports/producers, mixed internal/external refs, nested modules, artifact rejection, combined root guards, and parse-format-parse semantic preservation | `internal/workflow/workflow_test.go` | 35 min |
| 8 | Make `scheduler.evalGuard` recursively short-circuit the AST and perform fail-closed typed bool/string/number comparisons through the existing verdict and structured-output cache paths | `internal/engine/engine.go` | 35 min |
| 9 | Add focused engine tests proving compound dispatch/skip, `applies_when`, self-only `block_on` resume, compound route selection, short-circuit behavior, number boundaries, fan-out aggregate thresholds, and persistence-off execution | `internal/engine/condition_test.go` | 40 min |
| 10 | Mark all referenced dependency edges as conditional and format compound forward/back-edge labels with correct precedence | `internal/tui/chart/layout.go` | 25 min |
| 11 | Add chart layout/render tests for multi-source guards, grouped labels, truncation, and compound route captions | `internal/tui/chart` | 25 min |
| 12 | Document the full grammar, precedence, literal/type table, examples, route-proof limits, and all four condition surfaces | `docs/workflow-schema.md` | 30 min |
| 13 | Update the condition data model from a flat predicate to the expression AST | `docs/workflow-object-graph.md` | 15 min |
| 14 | Update parse, validate, route-proof, and recursive runtime-evaluation descriptions | `docs/engine-design.md` | 15 min |
| 15 | Update the workflow package map from the legacy `truthy | == | !=` parser description | `docs/ARCHITECTURE.md` | 10 min |
| 16 | Exercise a compound boolean plus numeric A8 aggregate guard in the dynamic fan-out example | `examples/dynamic-fanout.toml` | 15 min |
| 17 | Add a representative compound condition to the kitchen-sink workflow without changing backend selection and revalidate it | `.agents/jig/feature.toml` | 15 min |
| 18 | Mark A11 done and retain the implementation-plan link only after all acceptance checks pass | `docs/plans/open-goals.md` | 10 min |
| 19 | Run focused parser/validator, engine, and chart tests; full tests and race checks; vet/build; both reference workflow validations; and the headless dynamic-fanout example | repository-wide verification | 30 min |

Estimated focused implementation time: **8–10 hours**, best split into the
delivery phases below.

## Delivery phases and exit criteria

### Phase 1 — expression contract and load-time safety

Tasks 1–5. Exit when all legacy conditions still parse, the new grammar has one
canonical AST representation, every leaf is type/dependency checked, and route
analysis rejects every unproven overlap before execution.

### Phase 2 — modules and runtime behavior

Tasks 6–9. Exit when expanded/nested modules preserve every reference and
grouping, and engine tests prove identical compound semantics for all four guard
surfaces, including the no-run-dir path.

### Phase 3 — presentation, docs, and dogfood

Tasks 10–19. Exit when the chart marks every contributing dependency, reference
documentation exactly matches parser behavior, both repository workflows
validate, the headless example exercises numeric/logical evaluation, and the full
verification suite passes.

## Test and proof matrix

| Contract | Required proof |
|---|---|
| Legacy compatibility | Bare bool, dotted bool, quoted/bare bool values, enum/text equality, and inequality parse and evaluate exactly as before |
| Parser correctness | `&&` binds tighter than `||`; parentheses override; operators inside quotes stay literal; formatting reparses to an equivalent AST |
| Parser bounds | Excess nesting/node count and every dangling/mismatched token return actionable errors without panic |
| Type safety | Bool truthiness only; enum membership; number-only ordering; list/object/artifact rejection; invalid literal forms fail at load time |
| Dependency safety | Every compound `when`/`applies_when`/route reference is declared and available; every `block_on` leaf is self-only |
| Route determinism | Pairwise overlap rejects; numeric contradictions prove disjoint; finite own-output coverage permits no fallback; unknown coverage requires fallback |
| Check remediation | Both fail/error outcomes always have a proven bounded back-route; unrelated dependency predicates cannot accidentally suppress remediation |
| Module integrity | Multiple exports rewrite to their real producers, internal refs prefix, root/invocation guards combine, and formatted output reparses |
| Runtime semantics | Logical short-circuit, typed number comparison, fail-closed missing data, and exactly one route intent |
| A8 integration | `all_succeeded && count >= N` validates against `AggregateForEachSchema()` and evaluates from aggregate structured output |
| Presentation | Every referenced incoming edge is marked conditional and forward/back-edge labels preserve grouping and iteration caps |
| Regression | `go test ./...`, selected `go test -race ./internal/engine`, `go vet ./...`, `go build ./cmd/jig`, workflow validation, and headless dogfood all pass |

## Verification commands

```bash
go test ./internal/workflow
go test ./internal/engine
go test ./internal/tui/chart
go test -race ./internal/engine
go test ./...
go vet ./...
go build ./cmd/jig
go run ./cmd/jig validate .agents/jig/feature.toml
go run ./cmd/jig validate examples/dynamic-fanout.toml
go run ./cmd/jig run examples/dynamic-fanout.toml --ci
```

If the dynamic-fanout example requires a configured agent backend to execute,
its validation is mandatory and the headless execution proof should use the
repository's existing stubbed/fake executor harness rather than weakening the
example or introducing backend environment selection.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Token splitting mishandles operators inside strings | Use a real rune/byte lexer with quote state and source offsets; parser golden cases include every operator inside both quote styles. |
| Boolean precedence surprises authors | Use conventional `comparison > && > ||`, canonical formatting, explicit docs, and grouping tests. |
| Numeric values compare as strings | Validate against `FieldNumber` and route runtime values through a numeric comparator; cover `10 > 2`, equality, negatives, decimals, and boundaries. |
| One undeclared reference bypasses readiness | Validate and collect every predicate leaf, not merely the first unique step; module expansion repeats the check after rewriting. |
| Compound routes can both fire | Preserve pairwise mutual-exclusion validation with sound conservative proofs; reject unknown overlap instead of relying on first-match order. |
| Exhaustiveness analysis grows into a solver | Enumerate only one finite bool/enum own-output domain; require fallback for all other cases. |
| Module rewriting loses parentheses or quote meaning | Centralize AST ref rewriting and canonical formatting; require parse-format-parse tests for nested and mixed expressions. |
| Chart shows only one causal input | Decorate every `depends_on` edge named by the AST and label from the whole canonical expression. |
| A large authored expression causes deep recursion | Enforce explicit nesting and node caps in the parser before validation/evaluation. |
| Old persisted runs become unreadable | Preserve the documented grammar and parse raw condition strings from captured `workflow.json`; add a resume-style regression fixture using legacy guards. |

## Acceptance checklist

- [ ] `jig validate` accepts documented `&&`, `||`, grouped, equality, and
      numeric relational examples on every supported condition field.
- [ ] Invalid syntax, unknown references, missing dependencies, invalid fields,
      enum typos, nonnumeric thresholds, and illegal operator/type pairs all fail
      before an executor runs.
- [ ] Existing truthy/`==`/`!=` workflows and captured legacy workflow snapshots
      keep parsing and evaluating with unchanged results.
- [ ] Runtime evaluation short-circuits, compares numbers numerically, and fails
      closed on unavailable/corrupt data.
- [ ] A compound route set cannot pass validation unless guarded branches are
      provably disjoint and selection is total through finite coverage or a final
      fallback.
- [ ] Check remediation remains guaranteed for both `fail` and `error`.
- [ ] Nested module expansion rewrites every expression reference, preserves
      grouping, and can combine invocation and root guards.
- [ ] The chart marks every dependency that contributes to a forward guard and
      renders compound route labels with their iteration bounds.
- [ ] A8 aggregate conditions such as
      `analyze.all_succeeded && analyze.count >= 1` validate and execute.
- [ ] Persistence-off engine tests, full tests, race checks, vet, build, workflow
      validation, and headless proof all pass.
