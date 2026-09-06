# `internal/workflow` object graph & TOML parse path

Maps every in-memory type in `internal/workflow` and the function call chain
that turns a `.toml` file into a validated `*Workflow`. Companion to
[`workflow-schema.md`](workflow-schema.md) (author-facing fields) and
[`ARCHITECTURE.md`](ARCHITECTURE.md) (package seams).

---

## 1. Detailed object dependency graph

Composition (owns / embeds) vs reference (points at by id or path). Unexported
fields are shown with a leading `_` so resume/snapshot state is visible.

```mermaid
classDiagram
  direction TB

  class Workflow {
    Meta Meta
    Defaults Defaults
    Steps []Step
    Module *Module
    index map~string,int~
    profileIndex map~string,*AgentProfile~
    sourcePath string
    sourceTOML string
    publicSteps []Step
    moduleSources []ModuleSource
  }

  class Meta {
    Name string
    Version string
    Description string
  }

  class Defaults {
    Model string
    Effort EffortLevel
    Backend string
    Transport string
    MaxParallel int
    ResourceLimits map
    Security SecurityConfig
    InjectContext *bool
  }

  class SecurityConfig {
    Enabled *bool
    Tier1Enabled *bool
    Tier2Enabled *bool
    OutboundAllowlist []string
    FleetBudgetUSD float64
    ConcurrencyCap int
    BatchSize int
    DebounceMs int
  }

  class StepSecurity {
    Enabled *bool
    Tier1Enabled *bool
    Tier2Enabled *bool
    OutboundAllowlist []string
  }

  class AgentProfile {
    ID string
    Tools []string
    Model string
    Effort EffortLevel
    AskUserQuestion bool
    AppendSystemPrompt string
  }

  class Module {
    SchemaVersion int
    Inputs map~string,ModuleValue~
    Exports map~string,ModuleExport~
  }

  class ModuleValue {
    Type FieldType
    Enum []string
    Required *bool
  }

  class ModuleExport {
    Ref string
    Artifact string
  }

  class ModuleSource {
    Path string
    SHA256 string
    TOML string
  }

  class Step {
    ID string
    Type StepType
    DependsOn []string
    When string
    Output string
    OutputType OutputType
    OnFailure FailurePolicy
    Retry *RetryPolicy
    Inputs []Input
    Schema *Schema
    Validate *Validate
    Routes []Route
    Review []ReviewTarget
    Context *StepContextSpec
    Security StepSecurity
    Findings *CheckFindings
    Profile string
    agentPrompt string
    outputTemplateBody string
    injectContext bool
  }

  class Input {
    Ref string
    RefField []string
    Artifact string
    Path string
    From string
    Label string
    As string
    Once bool
    moduleInput string
  }

  class OutputType {
    Kind OutputKind
    Enum []string
  }

  class Schema {
    Fields []*Field
    File string
  }

  class Field {
    Name string
    Type FieldType
    Enum []string
    Elem *Field
    Fields []*Field
  }

  class Validate {
    Command string
    OutputSchema string
    OutputExists bool
    OutputContains string
  }

  class Route {
    When string
    Goto string
    MaxIterations int
    Feedback string
    Fallback bool
  }

  class ReviewTarget {
    Source string
    File string
    Label string
    resolvedPath string
    moduleInput string
  }

  class StepContextSpec {
    Purpose string
    Notes string
  }

  class RetryPolicy {
    MaxAttempts int
    Backoff string
    Initial Duration
    RetryOn []string
  }

  class CheckFindings {
    SchemaVersion int
    File string
    RequiredTools []string
    Artifacts map~string,string~
  }

  class Condition {
    Raw string
    Step string
    Field []string
    Op CondOp
    Value string
  }

  class Duration {
    time.Duration
  }

  class ValidationError {
    Problems []string
  }

  class validator {
    wf *Workflow
    baseDir string
    problems []string
  }

  class agentFile {
    Name string
    Tools []string
    Model string
    Prompt string
  }

  class moduleExpansion {
    sources map
    locked map
  }

  class expandedModule {
    terminals []string
    exports map~string,Input~
  }

  %% Ownership
  Workflow *-- Meta : workflow
  Workflow *-- Defaults : defaults
  Workflow *-- "0..*" Step : step
  Workflow o-- Module : module?
  Workflow o-- "0..*" AgentProfile : profileIndex
  Workflow o-- "0..*" ModuleSource : moduleSources
  Workflow o-- "0..*" Step : publicSteps

  Defaults *-- SecurityConfig : security

  Module *-- "0..*" ModuleValue : inputs
  Module *-- "0..*" ModuleExport : exports

  Step *-- OutputType : output_type
  Step *-- "0..*" Input : inputs
  Step o-- Schema : schema?
  Step o-- Validate : validate?
  Step *-- "0..*" Route : route
  Step *-- "0..*" ReviewTarget : review
  Step o-- StepContextSpec : context?
  Step *-- StepSecurity : security
  Step o-- RetryPolicy : retry?
  Step o-- CheckFindings : findings?
  Step --> AgentProfile : profile "@id"
  Step --> Duration : timeout

  RetryPolicy --> Duration : initial_backoff

  Schema *-- "0..*" Field : fields
  Field o-- Field : elem (list)
  Field o-- "0..*" Field : fields (object)

  %% Logical / string references (not ownership)
  Step ..> Step : depends_on / route.goto
  Input ..> Step : Ref → step id
  Input ..> Field : RefField path
  Route ..> Condition : when (parsed later)
  Step ..> Condition : when / block_on / applies_when
  ModuleExport ..> Input : export → Input shape

  %% Load-time helpers (not persisted on Workflow)
  validator --> Workflow
  ValidationError ..> validator : accumulates
  agentFile ..> Step : folds tools/model/prompt
  moduleExpansion --> expandedModule
  moduleExpansion ..> Workflow : expands subworkflows
```

### Ownership summary

| Owner | Owns / embeds | References by id / path |
|-------|---------------|-------------------------|
| `Workflow` | `Meta`, `Defaults`, `[]Step`, optional `Module`; runtime indexes | profiles by `"@id"`; module files via `ModuleSource` |
| `Defaults` | `SecurityConfig` | — |
| `Step` | `OutputType`, `[]Input`, `[]Route`, `[]ReviewTarget`, `StepSecurity`; optional `Schema`, `Validate`, `RetryPolicy`, `CheckFindings`, `StepContextSpec` | other steps via `DependsOn` / `Routes[].Goto`; profile via `Profile`; files via `Skill` / `AgentFile` / `SchemaFile` / `Script` / `OutputTemplate` / `Module` |
| `Schema` | tree of `Field` | — |
| `Field` | nested `Elem` / `Fields` | — |
| `Input` | — | producer step id + optional field path / artifact name |
| `Condition` | — | step id + optional field path (parsed from `when` strings) |

### Enums & named constants (leaf types)

```mermaid
flowchart LR
  subgraph StepType
    agent
    command
    review
    check
    subworkflow
  end

  subgraph FailurePolicy
    abort
    retry
    continue
  end

  subgraph Isolation
    worktree
    none
  end

  subgraph EffortLevel
    low
    medium
    high
    xhigh
    max
  end

  subgraph OutputKind
    text
    bool
    enum
  end

  subgraph FieldType
    text_f["text"]
    number
    bool_f["bool"]
    enum_f["enum"]
    list
    object
    unknown
    artifact
  end

  subgraph CondOp
    truthy
    eq["=="]
    neq["!="]
  end

  subgraph ReviewTargetKind
    source
    file
  end

  Step -->|Type| StepType
  Step -->|OnFailure| FailurePolicy
  Step -->|Isolation| Isolation
  Step -->|Effort| EffortLevel
  OutputType -->|Kind| OutputKind
  Field -->|Type| FieldType
  Condition -->|Op| CondOp
  ReviewTarget -->|Kind| ReviewTargetKind
```

### Per-step-type field clusters

Which nested objects matter for each `Step.Type` (validator enforces exclusivity):

```mermaid
flowchart TB
  Step --> Agent["type=agent"]
  Step --> Command["type=command"]
  Step --> Review["type=review"]
  Step --> Check["type=check"]
  Step --> Sub["type=subworkflow"]

  Agent --> A1["Skill xor AgentFile"]
  Agent --> A2["Inputs / Schema / Profile"]
  Agent --> A3["Tools · Model · Backend/Transport"]
  Agent --> A4["Context · BlockOn · OutputTemplate"]
  Agent --> A5["Validate · Routes · Security"]

  Command --> C1["Run xor Script"]
  Command --> C2["Validate · Routes"]

  Review --> R1["[]ReviewTarget"]
  Review --> R2["OutputType enum typically"]

  Check --> K1["Run/Script + AppliesWhen"]
  Check --> K2["CheckFindings"]
  Check --> K3["Routes for pass/fail"]

  Sub --> S1["Module path + With bindings"]
  Sub -.->|"loader expands away"| Exec["namespaced agent/command/check/review steps"]
```

---

## 2. TOML → `*Workflow` function diagram

Entry points and the ordered pipeline. Custom `UnmarshalTOML` methods fire
*inside* `toml.Decode`, before any of the resolve/default/validate stages.

### Top-level call graph

```mermaid
flowchart TD
  Load["Load(path)"] -->|ReadFile + Abs| DW["decodeWorkflow(data, baseDir, sourcePath)"]
  Decode["Decode(data, baseDir)"] --> DW
  DecodeLocked["DecodeLocked(data, baseDir, sourcePath, sources)"] -->|build locked map| DWL["decodeWorkflowLocked(...)"]
  DW --> DWL

  DWL --> DP["decodePrepared(data, baseDir)"]
  DP -->|success| HasSub{"hasSubworkflow?"}
  HasSub -->|yes| EM["expandModules(wf, baseDir, sourcePath, locked)"]
  HasSub -->|no| ModCheck
  EM --> ModCheck{"Module != nil && sourcePath == ''?"}
  ModCheck -->|yes| ErrMod["error: [module] only in subworkflow file"]
  ModCheck -->|no| Val["wf.validate(baseDir)"]
  Val --> Stamp["set sourcePath + sourceTOML"]
  Stamp --> Out["*Workflow"]

  LoadMeta["LoadMeta(path)"] --> DecodeMeta["DecodeMeta(data)"]
  DecodeMeta -->|"tolerant peek"| MetaOnly["Meta only — skips full pipeline"]
```

### `decodePrepared` — parse, resolve assets, inherit

```mermaid
flowchart TD
  Start["decodePrepared"] --> TomlDecode["toml.Decode(data, &Workflow)"]
  TomlDecode --> Custom["Custom UnmarshalTOML fires:\nInput · OutputType · Schema · Duration"]
  Custom --> Undecoded{"md.Undecoded() empty?"}
  Undecoded -->|no| ErrKeys["error: unknown key(s)"]
  Undecoded -->|yes| Skills["wf.resolveSkills(baseDir)"]
  Skills --> AgentFiles["wf.resolveAgentFiles(baseDir)"]
  AgentFiles --> OutTpl["wf.resolveOutputTemplates(baseDir)"]
  OutTpl --> Review["wf.resolveReviewTargets(baseDir)"]
  Review --> Profiles["loadProfiles(baseDir)"]
  Profiles --> Builtins["builtinProfiles() + local"]
  Builtins --> Idx["buildProfileIndex → wf.profileIndex"]
  Idx --> ApplyProf["wf.applyProfiles()"]
  ApplyProf --> ApplyDef["wf.applyDefaults()"]
  ApplyDef --> Ready["*Workflow prepared, not yet validated"]
```

**Inheritance precedence** (strongest → weakest) for agent knobs:

1. Explicit step TOML fields  
2. Values folded from `agent_file` / skill (tools, model, prompt)  
3. Referenced `AgentProfile` (`applyProfiles`)  
4. `[defaults]` then engine constants (`applyDefaults`)

`AskUserQuestion` from a profile is additive (always appends the tool).

### Custom TOML unmarshaling (during `toml.Decode`)

```mermaid
flowchart LR
  subgraph DuringDecode["Inside toml.Decode"]
    InRaw["inputs entry"] --> InUM["Input.UnmarshalTOML"]
    InUM --> InStr["string: @ref / path"]
    InUM --> InTbl["table: ref | artifact | path | from=user"]

    OTRaw["output_type"] --> OTUM["OutputType.UnmarshalTOML"]
    OTUM --> OTStr["string → Kind"]
    OTUM --> OTEnum["table → Kind=enum + Enum"]

    ScRaw["[step.schema]"] --> ScUM["Schema.UnmarshalTOML"]
    ScUM --> PF["parseFields"]
    PF --> PFS["parseFieldSpec"]
    PFS --> FieldTree["Field tree"]

    DurRaw["timeout / backoff"] --> DurUM["Duration.UnmarshalTOML"]
    DurUM --> ParseDur["time.ParseDuration"]
  end
```

### Module expansion (when any step is `type = "subworkflow"`)

```mermaid
flowchart TD
  EM["expandModules"] --> Pub["wf.publicSteps = clone(Steps)"]
  EM --> Exp["moduleExpansion.expand"]
  Exp --> Each{"for each step"}
  Each -->|not subworkflow| Keep["append clone"]
  Each -->|subworkflow| VSub["validateSubworkflowStep"]
  VSub --> Read["moduleData(path) — disk or locked"]
  Read --> Child["decodePrepared(child TOML)"]
  Child --> Decl["validateModuleDeclaration"]
  Decl --> Rebase["rebaseModuleAssets"]
  Rebase --> Recurse["expand(child) recursive"]
  Recurse --> Exports["resolveModuleExports"]
  Exports --> Inst["instantiateModule → prefix IDs"]
  Inst --> Splice["splice namespaced steps into parent"]
  Splice --> Each
  Each -->|done| Sources["wf.moduleSources = captured SHA256+TOML"]
```

Nested modules call `decodePrepared` again (same asset/profile/default path),
then recurse `expand` before the parent finishes.

### `validate` — static checks after expansion

```mermaid
flowchart TD
  V["wf.validate(baseDir)"] --> NewV["validator{wf, baseDir}"]
  NewV --> RS["resolveSchemas — schema_file → Schema"]
  RS --> Meta["checkMeta"]
  Meta --> RL["checkResourceLimits"]
  RL --> Sec["checkSecurityConfig"]
  Sec --> IDs["checkIDs"]
  IDs --> Loop["for each Step: checkStep"]
  Loop --> Agent["checkAgent / Command / Review / Check"]
  Loop --> Shared["checkInputs · OutputType · Schema\nTuning · ExecutionControls · Failure\nWhen · Validate · Routes · Context · Security"]
  Shared --> Acyc["checkAcyclic — DAG + bounded routes"]
  Acyc --> Problems{"problems?"}
  Problems -->|yes| VE["ValidationError"]
  Problems -->|no| OK["nil"]
```

Guards (`when`, `route.when`, `applies_when`, `block_on`) are parsed with
`ParseCondition` inside the validator (and related checks), not during TOML
decode — so the grammar stays tiny and type-checked against the resolved graph.

### Side paths used by load / resume

| Function | Role |
|----------|------|
| `RestoreExpanded` | Rebuild a locked execution graph from a run snapshot (no module re-read); calls `applyDefaults` only |
| `ParseJSONSchema` / `Schema.JSONSchema` | Bidirectional bridge between `schema_file` JSON and internal `Field` trees |
| `MergedSchema` / `BaseSchema` | Overlay producer fields onto the engine base schema |
| `RepoRoot` / `ScriptPath` / `ExecutionPath` | Path anchors for command scripts and runtime resolution |
| `ParseAgentFileContent` | Public peek at agent-file model+prompt without a full workflow load |

### Ordered pipeline (one page)

```
.toml bytes
    │
    ▼
toml.Decode ──► UnmarshalTOML (Input, OutputType, Schema, Duration)
    │            reject undecoded keys
    ▼
resolveSkills / resolveAgentFiles / resolveOutputTemplates / resolveReviewTargets
    │
    ▼
loadProfiles + builtinProfiles → applyProfiles
    │
    ▼
applyDefaults (index, inheritance, isolation, inject_context, security fold)
    │
    ▼
[optional] expandModules → recursive decodePrepared + instantiate
    │
    ▼
validate (schemas, meta, ids, per-step, acyclic)
    │
    ▼
*Workflow { sourcePath, sourceTOML, Steps, publicSteps, moduleSources }
```
