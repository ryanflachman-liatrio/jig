# Organizing many Rust domain newtypes

Research date: 2026-08-28

## Recommendation in one sentence

Organize newtypes by **domain concept and invariant**, not mechanically one file per type: keep a few closely related, small wrappers together; give a type its own file once its validation, errors, or behavior become substantial; keep fields and implementation modules private; and expose a small, explicit API through the parent module.

There is no Rust language rule or official guideline that says “one newtype per file.” Rust's module system is the unit of privacy and API design; files are one way to lay out that module tree. The Rust Book recommends moving modules into files as they grow and describes `foo.rs` plus `foo/bar.rs` as the current style, while `foo/mod.rs` remains supported but is described as the older style ([Rust Book: separating modules into files](https://doc.rust-lang.org/stable/book/ch07-05-separating-modules-into-different-files.html)).

## Suggested structure for `jig-workflow`

```text
workflow-rs/src/
├── lib.rs
├── domain.rs                 # private facade for the validated model
├── domain/
│   ├── workflow.rs           # Workflow, WorkflowName, WorkflowVersion
│   ├── step_id.rs            # cross-cutting ID, validation, error, tests
│   ├── step.rs               # Step and genuinely shared step data
│   ├── agent.rs              # AgentStep and agent-owned reference types
│   ├── command.rs            # CommandStep, CommandSource, ScriptPath
│   ├── review.rs             # ReviewStep and review-owned types
│   ├── output.rs             # OutputContract, OutputPath, SchemaPath
│   └── profile.rs            # ProfileId and profile-owned types
├── raw.rs                    # facade for TOML-shaped input types
├── raw/
│   ├── schema.rs             # RawWorkflow, RawStep, Serde attributes
│   └── conversion.rs         # TryFrom<Raw...> into validated domain types
└── load.rs                   # filesystem + TOML orchestration and LoadError
```

`domain.rs` should declare private implementation modules and explicitly re-export the intended surface:

```rust
mod agent;
mod command;
mod output;
mod profile;
mod review;
mod step;
mod step_id;
mod workflow;

pub use agent::AgentStep;
pub use command::{CommandSource, CommandStep, ScriptPath};
pub use output::{OutputContract, OutputPath, SchemaPath};
pub use profile::ProfileId;
pub use review::ReviewStep;
pub use step::Step;
pub use step_id::{StepId, StepIdError};
pub use workflow::{Workflow, WorkflowBuildError, WorkflowName, WorkflowVersion};
```

Then `lib.rs` can keep `domain` private and explicitly re-export its public types at the crate root, or make `domain` public if `jig_workflow::domain::StepId` is deliberately part of the public API. Do not make a child module public merely because it lives in another file. Rust items are private by default, and `pub(crate)`, `pub(super)`, and `pub(in ...)` support narrower internal seams ([Rust Reference: visibility and privacy](https://doc.rust-lang.org/reference/visibility-and-privacy.html)).

This private-child-module plus explicit-re-export pattern is visible in the established `http` crate: `header/mod.rs` keeps `map`, `name`, and `value` private and explicitly re-exports `HeaderName`, `HeaderValue`, and their errors ([`http::header` source](https://docs.rs/http/latest/src/http/header/mod.rs.html)). The Rust Book describes re-exporting selected items as a way to present a convenient public API independently of the internal hierarchy ([Rust Book: exporting a convenient public API](https://doc.rust-lang.org/book/ch14-02-publishing-to-crates-io.html#exporting-a-convenient-public-api)). The `semver` crate similarly separates parsing, errors, identifier internals, implementations, and optional Serde support into modules rather than putting every public type in its own file ([`semver` source](https://docs.rs/semver/latest/src/semver/lib.rs.html)). These crate layouts are examples, not language requirements.

### When to split a newtype into its own file

Keep a tiny newtype beside the domain concept that owns it: `WorkflowName` beside `Workflow`, `ProfileId` beside profiles, and `ScriptPath` beside command construction. Promote a type to its own file when it is cross-cutting or gains enough of the following that its owner's file becomes harder to navigate. `StepId` already qualifies because steps, dependencies, conditions, workflow indexing, graph validation, and errors all use it.

- a nontrivial grammar or parser;
- a substantial error enum;
- normalization rules;
- many domain operations;
- extensive tests or documentation;
- independent feature gates or dependencies.

Do not automatically group `ScriptPath`, `OutputPath`, and `SchemaPath` merely because they all wrap path-like storage. If their rules are owned by different features, keeping them with `command` and `output` makes those rules easier to locate. A shared `path.rs` becomes useful only if there is a real shared path policy and API. Avoid a generic `types.rs` or `newtypes.rs`: those names describe the Rust mechanism but not where a future reader should look for a domain concept.

## Shape of a validated string newtype

```rust
use std::borrow::Borrow;
use std::fmt;
use std::str::FromStr;

use thiserror::Error;

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub struct StepId(String);

#[derive(Clone, Debug, Eq, Error, PartialEq)]
pub enum StepIdError {
    #[error("step id is empty")]
    Empty,

    #[error("step id contains invalid character {character:?} at byte {index}")]
    InvalidCharacter { index: usize, character: char },
}

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

impl TryFrom<&str> for StepId {
    type Error = StepIdError;

    fn try_from(value: &str) -> Result<Self, Self::Error> {
        validate_step_id(value)?;
        Ok(Self(value.to_owned()))
    }
}

impl FromStr for StepId {
    type Err = StepIdError;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        Self::try_from(value)
    }
}

impl AsRef<str> for StepId {
    fn as_ref(&self) -> &str {
        self.as_str()
    }
}

// Useful for HashMap<StepId, _>::get("step-name"). This is correct only
// while StepId's Eq, Ord, and Hash behavior is identical to str's behavior.
impl Borrow<str> for StepId {
    fn borrow(&self) -> &str {
        self.as_str()
    }
}

impl fmt::Display for StepId {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.as_str())
    }
}

fn validate_step_id(value: &str) -> Result<(), StepIdError> {
    if value.is_empty() {
        return Err(StepIdError::Empty);
    }

    if let Some((index, character)) = value
        .char_indices()
        .find(|(_, character)| {
            !(character.is_ascii_alphanumeric() || matches!(character, '_' | '-'))
        })
    {
        return Err(StepIdError::InvalidCharacter { index, character });
    }

    Ok(())
}
```

The important property is the private inner field. Callers cannot bypass validation with `StepId(String::new())`. Rust's API Guidelines recommend newtypes for static distinctions, validating inputs at the boundary, and keeping representation details private ([C-NEWTYPE](https://rust-lang.github.io/api-guidelines/type-safety.html), [C-VALIDATE](https://rust-lang.github.io/api-guidelines/dependability.html), [C-NEWTYPE-HIDE](https://rust-lang.github.io/api-guidelines/future-proofing.html)). Mature validated types follow the same broad pattern: `http::HeaderValue` has private fields and offers fallible conversions from `&str`, `String`, bytes, and vectors ([`HeaderValue` API](https://docs.rs/http/latest/http/header/struct.HeaderValue.html), [`HeaderValue` conversion source](https://docs.rs/http/latest/src/http/header/value.rs.html)).

### Constructors and conversion traits

- Make one path the source of truth for validation. In the example, `TryFrom<String>` and `TryFrom<&str>` validate; `new` and `FromStr` delegate.
- Implement `TryFrom`, not `From`, when an input can be invalid. The standard library describes `From` as infallible and `TryFrom` as the controlled fallible conversion ([`From`](https://doc.rust-lang.org/std/convert/trait.From.html), [`TryFrom`](https://doc.rust-lang.org/std/convert/trait.TryFrom.html)). Implement `From`/`TryFrom`, not `Into`/`TryInto`; blanket implementations provide the latter ([`std::convert`](https://doc.rust-lang.org/std/convert/)).
- Implement `FromStr` when the type has a canonical textual grammar and `.parse::<StepId>()` is natural. Its associated error is explicitly intended to describe ill-formed input ([`FromStr`](https://doc.rust-lang.org/std/str/trait.FromStr.html)).
- Provide `as_str()` for the obvious borrowed view and an owned extraction method only when callers need it. Rust getter naming omits `get_` ([C-GETTER](https://rust-lang.github.io/api-guidelines/naming.html)).
- Implement `AsRef<str>` for cheap generic borrowing. Implement `Borrow<str>` only when the wrapper's `Eq`, `Ord`, and `Hash` semantics exactly match `str`; that stronger contract enables borrowed `HashMap` lookup ([`AsRef`](https://doc.rust-lang.org/std/convert/trait.AsRef.html), [`Borrow`](https://doc.rust-lang.org/std/borrow/trait.Borrow.html)). If an ID is ever case-insensitive or normalized in comparisons, remove `Borrow<str>` or supply a matching borrowed key type.
- Do not implement `Deref<Target = str>` merely to save `.as_str()`. `Deref` participates implicitly in coercion and method resolution; the API Guidelines reserve it for smart-pointer behavior, and the standard library warns that it has far-reaching public-API consequences ([C-DEREF](https://rust-lang.github.io/api-guidelines/predictability.html), [`Deref` guidance](https://doc.rust-lang.org/std/ops/trait.Deref.html)).
- Do not add `#[repr(transparent)]` as a general “newtype marker.” It is an ABI/layout guarantee and is useful when that guarantee is actually required, such as FFI ([Rust Reference: transparent representation](https://doc.rust-lang.org/reference/type-layout.html#the-transparent-representation)). Ordinary domain wrappers do not need it.

## Trait derives: semantic, not automatic

Rust's API Guidelines say crates should eagerly implement applicable common traits because downstream crates cannot add them under the orphan rules ([C-COMMON-TRAITS](https://rust-lang.github.io/api-guidelines/interoperability.html)). “Applicable” matters:

| Trait | Use for a domain newtype when… |
|---|---|
| `Debug` | Nearly always; the guidelines expect public types to support debugging. |
| `Clone` | The owned value is reasonably cloneable. `String` wrappers are not `Copy`. |
| `Eq`, `PartialEq` | Domain equality is well-defined. Usually yes for identifiers. |
| `Hash` | The type is a map/set key and hashing matches equality. Deriving both preserves the required relationship ([`Hash`](https://doc.rust-lang.org/std/hash/trait.Hash.html)). |
| `Ord`, `PartialOrd` | The ordering is meaningful or deterministic lexical ordering is explicitly desired. Do not invent semantic ordering merely because `String` has it. |
| `Display` | There is one obvious user-facing textual representation. For IDs, usually yes. |
| `Default` | A real, valid domain default exists. Do not derive it for a non-empty string type or create an “unset” sentinel. |
| `Copy` | The representation is cheaply copied and copy semantics are appropriate. Not for `String`/`PathBuf` wrappers. |

Derive only traits whose semantics should remain part of the type's API. In particular, a derived `Ord` exposes the inner representation's ordering as domain behavior; a future representation change must preserve it.

## Keep Serde at the boundary

For this conversion, use separate TOML-shaped types:

```rust
#[derive(serde::Deserialize)]
#[serde(deny_unknown_fields)]
struct RawStep {
    id: String,
    // Optional and mutually incompatible authoring fields are allowed here.
}

impl TryFrom<RawStep> for Step {
    type Error = StepBuildError;

    fn try_from(raw: RawStep) -> Result<Self, Self::Error> {
        let id = StepId::try_from(raw.id)?;
        // Resolve authoring alternatives into domain enums here.
        todo!()
    }
}
```

This separation is a design recommendation for jig, not a universal Rust mandate. It keeps TOML compatibility rules (`rename`, aliases, defaults, omitted fields, untagged alternatives) out of the validated model and provides one explicit transition from “parsed” to “valid.” Serde directly supports this pattern with `#[serde(try_from = "RawType")]`, which deserializes a raw type and invokes `TryFrom`; it also supports `#[serde(transparent)]` for wire-transparent single-field wrappers ([Serde container attributes](https://serde.rs/container-attrs.html)). However, putting those attributes on the domain type still couples that type to Serde. For jig's stated goal—domain types first, parsing second—call `TryFrom<RawWorkflow>` explicitly in the loader instead.

The `semver` crate is a useful established example of keeping optional Serde code in a dedicated, feature-gated module rather than decorating its core types throughout the domain implementation ([`semver` module layout](https://docs.rs/semver/latest/src/semver/lib.rs.html)). The `url` crate takes another valid approach: it provides manual Serde implementations behind an optional feature while keeping `Url`'s representation private and parsing through its validated API ([`url` source](https://docs.rs/url/latest/src/url/lib.rs.html)). These examples show that Serde integration can be isolated even when domain types ultimately implement Serde traits.

## Error organization

Keep an error next to the operation or type that owns the invariant:

- `StepIdError` beside `StepId` in `identifier.rs`;
- `WorkflowBuildError` beside `Workflow::new` in `workflow.rs`;
- `LoadError` beside filesystem/TOML orchestration in `load.rs`.

Create a shared `error.rs` only when it represents a genuine public error facade or when several modules jointly own the variants. A single catch-all error enum for every newtype makes dependencies and ownership less clear. The API Guidelines require public errors to be meaningful and to implement `Error` and `Display`, rather than using `()` or bare strings ([C-GOOD-ERR](https://rust-lang.github.io/api-guidelines/interoperability.html#error-types-are-meaningful-and-well-behaved-c-good-err)). `thiserror`, already used by this crate, generates the same public `std::error::Error` implementation that handwritten code would and does not become part of the public API ([`thiserror` documentation](https://docs.rs/thiserror/latest/thiserror/)).

If this crate's public error enums are expected to gain variants after stabilization, consider `#[non_exhaustive]`; outside the defining crate it requires wildcard matching and permits future variants ([Rust Reference: `non_exhaustive`](https://doc.rust-lang.org/reference/attributes/type_system.html#the-non_exhaustive-attribute)). Jig is pre-v1, so this is not necessary merely as ceremony.

## Macros

Start by writing the first few newtypes by hand. Their apparent boilerplate often hides real semantic differences: allowed characters, trimming, case sensitivity, normalization, whether empty is valid, which traits are meaningful, and which error data matters.

Introduce a private `macro_rules!` macro only after several types have an **identical invariant and trait surface**. Keep validation in ordinary functions when possible, so tests and diagnostics remain straightforward. Avoid a macro with many boolean switches such as `hash = true`, `empty = false`, `serde = true`; that recreates a type-definition language that is harder to read than the implementations it replaces.

If an item-generating macro is ever public, Rust's API Guidelines say its syntax should resemble the Rust items it creates and it should accept normal visibility specifiers ([Rust API Guidelines: macros](https://rust-lang.github.io/api-guidelines/macros.html)). For this learning-oriented conversion, a macro or procedural-macro dependency would also hide the exact trait and invariant work the exercise is intended to practice, so handwritten implementations are the better starting point.

## Bottom line for this crate

1. Create a validated `domain` module and a separate Serde-backed `raw` module.
2. Keep small wrappers beside their owning concepts; do not make one file per one-line tuple struct or collect them in a mechanism-oriented `newtypes.rs`.
3. Give behaviorally substantial types (`Step`, `Workflow`, `OutputContract`) their own files.
4. Keep tuple fields private and expose explicit construction plus borrowed views.
5. Use `TryFrom`/`FromStr` for validation, semantic derives, and local meaningful errors.
6. Explicitly re-export the public surface from the parent module.
7. Do not use `Deref`, `Default`, `repr(transparent)`, Serde derives, or a boilerplate macro by reflex; each carries an API promise.
