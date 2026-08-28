# Rust implementation guidance

This crate is a Rust interpretation of `internal/workflow`, not a line-for-line
translation of the Go package. Preserve workflow behavior and the author-facing
TOML contract while designing the in-memory model as idiomatic Rust.

The workflow schema remains documented in [`../docs/workflow-schema.md`](../docs/workflow-schema.md).
Research supporting these conventions is in
[`../docs/research/rust-newtype-organization.md`](../docs/research/rust-newtype-organization.md).

## Design direction

Use Rust's type system to make invalid states difficult or impossible to
construct. Parse permissive authoring data at the boundary, then convert it
into a validated domain model.

```text
TOML -> RawWorkflow -> TryFrom -> Workflow
```

The `Raw*` types may reflect TOML's optional and mutually exclusive fields.
The domain types must express resolved states directly.

Prefer:

```rust
pub enum Step {
    Agent(AgentStep),
    Command(CommandStep),
    Review(ReviewStep),
}
```

over a single struct containing every field for every step kind. Each variant
owns only the fields valid for that kind.

Represent mutually exclusive fields with enums:

```rust
pub enum AgentInstructions {
    Skill(SkillReference),
    AgentFile(AgentFileReference),
}

pub enum CommandSource {
    Inline(String),
    Script(ScriptPath),
}

pub enum FailureBehavior {
    Abort,
    Continue,
    Retry { max_retries: NonZeroUsize },
}

pub enum AgentBackend {
    Claude(ClaudeTransport),
    Cursor,
    Codex,
}
```

This is preferable to carrying multiple `Option` fields and validating their
combinations repeatedly. Cursor and Codex, for example, should not be capable
of containing an SDK transport.

Use `Option<T>` for genuine absence, not for partially constructed or invalid
states. Do not use empty strings, zeroes, or `Unknown("")` as unset sentinels.
Implement `Default` only when the domain has a real valid default.

## File and module structure

Organize files by domain ownership, not by the fact that a type is a newtype.
Avoid a global `newtypes.rs` or `types.rs`. Avoid flat prefixed filenames such
as `step_id.rs`, `step_failure.rs`, and `step_input.rs` when a `step` module
provides the namespace.

Use the modern `step.rs` plus `step/` layout:

```text
src/workflow/
├── mod.rs
├── definition.rs       # Workflow and graph-wide construction
├── step.rs             # Step, StepCommon, and step re-exports
├── step/
│   ├── id.rs           # StepId and StepIdError
│   ├── agent.rs        # AgentStep and agent-owned values
│   ├── command.rs      # CommandStep, CommandSource, ScriptPath
│   ├── review.rs       # ReviewStep and ReviewTarget
│   ├── failure.rs      # FailureBehavior
│   └── input.rs        # Input and input references
├── output.rs           # OutputContract and schema-related values
├── profile.rs          # AgentProfile and ProfileId
├── raw.rs              # Raw configuration facade
├── raw/
│   ├── workflow.rs
│   └── step.rs
├── convert.rs          # Raw-to-domain TryFrom implementations
└── load.rs             # Filesystem and TOML orchestration
```

Keep `Workflow`, `WorkflowName`, `WorkflowVersion`, and their construction
error together in `definition.rs` while they remain cohesive. Create smaller
`definition/` submodules if those value types develop substantial parsing,
validation, or behavior.

Prefer smaller files. Aim for roughly 100-250 lines of focused implementation
per file. At about 300 lines, look actively for a cohesive submodule to extract;
files beyond 400 lines require a clear reason. These are soft limits: preserve
cohesion instead of creating one trivial file per tuple struct. Move large test
modules into sibling test files when they obscure the implementation.

A newtype earns its own file when it has one or more of:

- a nontrivial grammar or normalization rule;
- a meaningful error enum;
- several conversions or domain operations;
- broad use across sibling modules;
- extensive tests or documentation;
- independent dependencies or feature gates.

Small newtypes stay beside the aggregate that gives them meaning. A shared
representation does not imply shared ownership: `ScriptPath` belongs to
commands and `SchemaPath` belongs to output contracts even though both may wrap
`PathBuf`.

Keep implementation modules private and explicitly re-export the intended API:

```rust
// step.rs
mod agent;
mod command;
mod failure;
mod id;
mod input;
mod review;

pub use agent::AgentStep;
pub use command::{CommandSource, CommandStep};
pub use failure::FailureBehavior;
pub use id::{StepId, StepIdError};
pub use input::Input;
pub use review::{ReviewStep, ReviewTarget};
```

The outer `workflow` module should re-export public domain types so callers use
stable paths such as `crate::workflow::StepId`. The public API must not depend
on the current file layout.

## Newtypes

Use a newtype when it establishes a semantic distinction, owns an invariant,
or carries domain behavior. A field being a `String` is not sufficient reason
on its own.

Keep the inner value private:

```rust
#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct StepId(String);
```

All construction paths must preserve the invariant. Put validation in one
place and delegate to it:

```rust
impl StepId {
    pub fn new(value: impl Into<String>) -> Result<Self, StepIdError> {
        Self::try_from(value.into())
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }

    pub fn into_string(self) -> String {
        self.0
    }
}

impl TryFrom<String> for StepId {
    type Error = StepIdError;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        validate_step_id(&value)?;
        Ok(Self(value))
    }
}
```

Implement traits according to domain semantics:

- `TryFrom<String>` and `TryFrom<&str>` for fallible construction;
- `FromStr` when `.parse()` is natural;
- `Display` when there is one obvious textual representation;
- `AsRef<str>` or `AsRef<Path>` for cheap borrowed access;
- `Borrow<str>` only when equality and hashing exactly match `str`;
- `Clone`, `Eq`, `Hash`, or `Ord` only when their meaning is intentional.

Do not implement `From<String>` for validated input because `From` promises an
infallible conversion. Do not implement `Deref<Target = str>` merely to avoid
calling `as_str()`. Do not add `repr(transparent)` unless an ABI or FFI contract
requires it. Do not derive `Ord` merely because the wrapped value supports it.

Write the first newtypes by hand. Introduce a private macro only after multiple
types prove that they have identical invariants, errors, and trait surfaces.
Avoid a configurable macro that becomes a second type-definition language.

## Parsing and Serde

Keep Serde and TOML concerns in `raw`. Raw types may use `Option`, defaults,
aliases, untagged enums, and `deny_unknown_fields` to accurately represent the
authoring format.

Convert raw values explicitly:

```rust
impl TryFrom<RawStep> for Step {
    type Error = StepBuildError;

    fn try_from(raw: RawStep) -> Result<Self, Self::Error> {
        let id = StepId::try_from(raw.id)?;
        // Resolve raw field combinations into one valid Step variant.
        todo!()
    }
}
```

Domain types should not derive `Deserialize` by default. If a domain newtype
must deserialize directly, use Serde's fallible `try_from` path so decoding
cannot bypass validation. `#[serde(transparent)]` alone describes wire shape;
it does not replace invariant-preserving construction.

Separate errors by ownership:

- `StepIdError` lives beside `StepId`;
- `StepBuildError` lives beside raw-to-step conversion;
- `WorkflowBuildError` lives beside graph construction;
- `LoadError` lives beside filesystem and TOML orchestration.

Use concrete error enums implementing `Display` and `Error`. Use `thiserror`
where it removes mechanical boilerplate without hiding domain decisions.

## Rust over Go-shaped code

Preserve behavior, not Go's representation choices.

- Replace string enums with Rust enums.
- Replace `field == ""` checks with validated types and `Option`.
- Replace combinations of booleans with descriptive enums.
- Replace parallel optional fields with enum variants carrying their data.
- Resolve defaults once at the raw-to-domain boundary.
- Construct a workflow through a fallible constructor that establishes indexes
  and validates graph-wide invariants.
- Keep domain fields private when mutation could violate an invariant.
- Prefer borrowed inputs such as `&str` and `&Path` when ownership is not needed.
- Clone because ownership semantics require an owned value, not merely to silence
  the borrow checker.
- Use iterators where they clarify transformations; use ordinary loops where
  early exits or stateful validation are clearer.

Avoid reproducing Go's zero-value-driven lifecycle in Rust. Once a `Workflow`
exists, it should already have valid IDs, resolved step variants, valid backend
and transport combinations, and a checked dependency graph.

## Validation layers

Validate at the narrowest layer that owns the rule:

1. A newtype constructor validates its local invariant.
2. A step conversion validates combinations within one step.
3. `Workflow::try_from` or `Workflow::new` validates cross-step references,
   duplicate IDs, cycles, and graph-wide rules.
4. Loading resolves filesystem-backed authoring resources and reports I/O
   context.

Do not repeat a local invariant in later layers. Later code may rely on the
guarantees established by earlier types.

## Tests

Keep tests beside the behavior they prove. Newtype tests cover every validation
branch and important trait behavior. Step-conversion tests cover every raw field
combination. Workflow tests cover graph-wide invariants. Loader tests cover
filesystem resolution and error context.

Prefer tests that construct valid domain values through public constructors.
If tests repeatedly need invalid domain instances, the production API is
probably exposing a way to bypass its invariants.

Before considering a change complete, run:

```bash
cargo fmt --all -- --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-targets --all-features
```
