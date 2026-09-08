# Workflow object graph

This diagram describes the in-memory model produced by `internal/workflow`.
Solid diamonds indicate owned values. Dashed arrows indicate references encoded
as identifiers or strings rather than Go pointers.

```mermaid
classDiagram
direction TB

class Workflow {
    +Meta Meta
    +Defaults Defaults
    +Step[] Steps
    +Module? Module
    -map~string,int~ index
    -map~string,AgentProfile*~ profileIndex
    -string sourcePath
    -string sourceTOML
    -Step[] publicSteps
    -ModuleSource[] moduleSources
    +Source() path, toml
    +PublicSteps() Step[]
    +ModuleSources() ModuleSource[]
}

class Meta {
    +string Name
    +string Version
    +string Description
}

class Defaults {
    +string Model
    +string FallbackModel
    +EffortLevel Effort
    +int MaxTurns
    +int MaxThinkingTokens
    +float64 MaxBudgetUSD
    +string Cwd
    +string PermissionMode
    +int MaxParallel
    +string ArtifactsDir
    +map~string,int~ ResourceLimits
    +int MaxReadOnly
    +int MaxMutating
    +float64 MaxCostUSD
    +int MaxSecurityFindings
    +int MaxNetworkRequests
    +bool? InjectContext
    +string Backend
    +string Transport
    +SecurityConfig Security
}

class SecurityConfig {
    +bool? Enabled
    +bool? Tier1Enabled
    +bool? Tier2Enabled
    +string[] OutboundAllowlist
    +float64 FleetBudgetUSD
    +int ConcurrencyCap
    +int BatchSize
    +int DebounceMs
}

class Step {
    +string ID
    +StepType Type
    +string[] DependsOn
    +string When
    +string Output
    +OutputType OutputType
    +FailurePolicy OnFailure
    +int MaxRetries
    +Duration Timeout
    +RetryPolicy? Retry
    +bool? Idempotent
    +string ResourceClass
    +string[] MutationPaths
    +string[] Secrets
    +string Skill
    +string AgentFile
    +string Profile
    +Input[] Inputs
    +Isolation Isolation
    +bool? InjectContext
    -bool injectContext
    +StepContextSpec? Context
    +string[] AllowedTools
    +string[] DisallowedTools
    +string Model
    +string FallbackModel
    +EffortLevel Effort
    +int MaxTurns
    +int MaxThinkingTokens
    +float64 MaxBudgetUSD
    +string PermissionMode
    +string Backend
    +string Transport
    +string OutputTemplate
    -string outputTemplateBody
    +string SnapshotOutputTemplate
    +string AppendSystemPrompt
    -string agentPrompt
    +string SnapshotAgentPrompt
    +Schema? Schema
    +string SchemaFile
    +string Run
    +string Script
    +string AppliesWhen
    +CheckFindings? Findings
    +string Module
    +map~string,string~ With
    +ReviewTarget[] Review
    +string BlockOn
    +Validate? Validate
    +Route[] Routes
    +StepSecurity Security
    +AgentPrompt() string
    +OutputTemplateBody() string
    +InjectContextEnabled() bool
}

class OutputType {
    +OutputKind Kind
    +string[] Enum
    +UnmarshalTOML(any)
    -allows(string) bool
}

class Duration {
    +time.Duration Duration
    +UnmarshalTOML(any)
}

class RetryPolicy {
    +int MaxAttempts
    +string Backoff
    +Duration Initial
    +string[] RetryOn
}

class Input {
    +string Ref
    +string[] RefField
    +string Artifact
    +string Path
    +bool Inline
    +string From
    +string Label
    +string As
    +bool Once
    -string moduleInput
    +UnmarshalTOML(any)
    +String() string
}

class StepContextSpec {
    +string Purpose
    +string Notes
}

class StepSecurity {
    +bool? Enabled
    +bool? Tier1Enabled
    +bool? Tier2Enabled
    +string[] OutboundAllowlist
}

class Schema {
    +Field[] Fields
    +string File
    +UnmarshalTOML(any)
    +JSONSchema() bytes
    -lookup(path) Field
}

class Field {
    +string Name
    +FieldType Type
    +string[] Enum
    +Field? Elem
    +Field[] Fields
}

class CheckFindings {
    +int SchemaVersion
    +string File
    +string[] RequiredTools
    +map~string,string~ Artifacts
}

class ReviewTarget {
    +string Source
    +string File
    +string Label
    -string resolvedPath
    -string moduleInput
    +Kind() ReviewTargetKind
    +Reference() string
    +ResolvedPath() string
}

class Validate {
    +string Command
    +string OutputSchema
    +bool OutputExists
    +string OutputContains
}

class Route {
    +string When
    +string Goto
    +int MaxIterations
    +string Feedback
    +bool Fallback
}

class Condition {
    +string Raw
    +string Step
    +string[] Field
    +CondOp Op
    +string Value
}

class Module {
    +int SchemaVersion
    +map~string,ModuleValue~ Inputs
    +map~string,ModuleExport~ Exports
}

class ModuleValue {
    +FieldType Type
    +string[] Enum
    +bool? Required
}

class ModuleExport {
    +string Ref
    +string Artifact
}

class ModuleSource {
    +string Path
    +string SHA256
    +string TOML
}

class AgentProfile {
    +string ID
    +string[] Tools
    +string[] DisallowedTools
    +string Model
    +string FallbackModel
    +EffortLevel Effort
    +int MaxTurns
    +int MaxThinkingTokens
    +float64 MaxBudgetUSD
    +string PermissionMode
    +bool AskUserQuestion
    +string AppendSystemPrompt
}

class StepType {
    <<enumeration>>
    agent
    command
    review
    check
    subworkflow
}

class FailurePolicy {
    <<enumeration>>
    abort
    retry
    continue
}

class Isolation {
    <<enumeration>>
    worktree
    none
}

class OutputKind {
    <<enumeration>>
    text
    bool
    enum
}

class FieldType {
    <<enumeration>>
    text
    number
    bool
    enum
    list
    object
    unknown
    artifact
}

class CondOp {
    <<enumeration>>
    truthy
    equals
    not-equals
}

Workflow "1" *-- "1" Meta : workflow
Workflow "1" *-- "1" Defaults : defaults
Workflow "1" *-- "0..*" Step : executable Steps
Workflow "1" o-- "0..*" Step : publicSteps snapshot
Workflow "1" o-- "0..1" Module : module declaration
Workflow "1" o-- "0..*" ModuleSource : expansion provenance
Workflow "1" o-- "0..*" AgentProfile : profileIndex

Defaults "1" *-- "1" SecurityConfig : Security

Step "1" *-- "1" OutputType
Step "1" *-- "1" Duration : Timeout
Step "1" *-- "0..1" RetryPolicy
RetryPolicy "1" *-- "1" Duration : Initial
Step "1" *-- "0..*" Input
Step "1" *-- "0..1" StepContextSpec
Step "1" *-- "1" StepSecurity
Step "1" *-- "0..1" Schema
Step "1" *-- "0..1" CheckFindings
Step "1" *-- "0..*" ReviewTarget
Step "1" *-- "0..1" Validate
Step "1" *-- "0..*" Route

Schema "1" *-- "0..*" Field
Field "1" *-- "0..1" Field : list element
Field "1" *-- "0..*" Field : object properties

Module "1" *-- "0..*" ModuleValue : Inputs
Module "1" *-- "0..*" ModuleExport : Exports

Step ..> StepType
Step ..> FailurePolicy
Step ..> Isolation
OutputType ..> OutputKind
Field ..> FieldType
ModuleValue ..> FieldType
Condition ..> CondOp

Step ..> AgentProfile : Profile resolves by ID
Step ..> Condition : parses When, AppliesWhen, BlockOn
Route ..> Condition : parses When
Step ..> Step : DependsOn and route Goto
Input ..> Step : Ref and RefField
ReviewTarget ..> Step : source or file reference
ModuleExport ..> Step : ref or artifact reference
CheckFindings ..> Input : named artifact exports
```

## Reading the graph

- `Workflow.Steps` is the executable graph. When modules are expanded,
  `publicSteps` retains the original author-facing graph.
- `index` and `profileIndex` are derived lookup structures and are not decoded
  directly from TOML.
- `When`, `AppliesWhen`, `BlockOn`, and `Route.When` remain strings in the
  stored object. Validation parses them into temporary `Condition` values.
- Agent prompts and output templates are loaded eagerly and copied into
  snapshot fields so a resumed run does not depend on changed authoring files.
- An agent's effective output schema is `BaseSchema + Step.Schema`. The base
  always supplies `assumptions`, `confidence`, `issues`, `status`, and
  `summary`.
- A `subworkflow` step exists only in the author graph. Module expansion
  replaces it with namespaced executable steps such as
  `moduleID__internalStepID`.

## Sources

- [`internal/workflow/schema.go`](../internal/workflow/schema.go)
- [`internal/workflow/base_schema.go`](../internal/workflow/base_schema.go)
- [`internal/workflow/condition.go`](../internal/workflow/condition.go)
- [`internal/workflow/module.go`](../internal/workflow/module.go)
