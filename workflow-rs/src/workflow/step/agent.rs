use std::path::{Path, PathBuf};

use thiserror::Error;

use super::{SecurityOverride, StepCommon};
use crate::workflow::{OutputContract, ProfileId};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SkillPath(PathBuf);

impl SkillPath {
    pub fn new(path: impl Into<PathBuf>) -> Self {
        Self(path.into())
    }

    pub fn as_path(&self) -> &Path {
        &self.0
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AgentFilePath(PathBuf);

impl AgentFilePath {
    pub fn new(path: impl Into<PathBuf>) -> Self {
        Self(path.into())
    }

    pub fn as_path(&self) -> &Path {
        &self.0
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum InstructionBody {
    Unresolved,
    Resolved(String),
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum AgentInstructions {
    Skill {
        path: SkillPath,
        body: InstructionBody,
    },
    AgentFile {
        path: AgentFilePath,
        body: InstructionBody,
    },
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ClaudeTransport {
    Sdk,
    Acp,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AgentBackend {
    Claude(ClaudeTransport),
    Cursor,
    Codex,
}

impl Default for AgentBackend {
    fn default() -> Self {
        Self::Claude(ClaudeTransport::Sdk)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Isolation {
    Worktree,
    None,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EffortLevel {
    Low,
    Medium,
    High,
    ExtraHigh,
    Max,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum PermissionMode {
    Default,
    AcceptEdits,
    Plan,
    BypassPermissions,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct BudgetUsd(f64);

#[derive(Clone, Copy, Debug, Error, PartialEq)]
#[error("budget must be greater than zero and finite")]
pub struct BudgetError;

impl TryFrom<f64> for BudgetUsd {
    type Error = BudgetError;

    fn try_from(value: f64) -> Result<Self, Self::Error> {
        if value.is_finite() && value > 0.0 {
            Ok(Self(value))
        } else {
            Err(BudgetError)
        }
    }
}

impl BudgetUsd {
    pub fn get(self) -> f64 {
        self.0
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct ModelConfig {
    pub(crate) model: Option<String>,
    pub(crate) fallback_model: Option<String>,
    pub(crate) effort: Option<EffortLevel>,
    pub(crate) max_turns: Option<std::num::NonZeroUsize>,
    pub(crate) max_thinking_tokens: Option<std::num::NonZeroUsize>,
    pub(crate) max_budget: Option<BudgetUsd>,
    pub(crate) permission_mode: Option<PermissionMode>,
}

impl ModelConfig {
    pub fn model(&self) -> Option<&str> {
        self.model.as_deref()
    }

    pub fn effort(&self) -> Option<EffortLevel> {
        self.effort
    }

    pub fn fallback_model(&self) -> Option<&str> {
        self.fallback_model.as_deref()
    }

    pub fn max_turns(&self) -> Option<std::num::NonZeroUsize> {
        self.max_turns
    }

    pub fn max_thinking_tokens(&self) -> Option<std::num::NonZeroUsize> {
        self.max_thinking_tokens
    }

    pub fn max_budget(&self) -> Option<BudgetUsd> {
        self.max_budget
    }

    pub fn permission_mode(&self) -> Option<PermissionMode> {
        self.permission_mode
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct ToolPolicy {
    pub(crate) allowed: Vec<String>,
    pub(crate) disallowed: Vec<String>,
}

impl ToolPolicy {
    pub fn allowed(&self) -> &[String] {
        &self.allowed
    }

    pub fn disallowed(&self) -> &[String] {
        &self.disallowed
    }

    pub(crate) fn is_mutating(&self) -> bool {
        self.allowed.iter().any(|tool| {
            matches!(
                tool.as_str(),
                "Edit" | "MultiEdit" | "Write" | "Bash" | "NotebookEdit"
            )
        })
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct AgentContext {
    pub(crate) purpose: Option<String>,
    pub(crate) notes: Option<String>,
}

impl AgentContext {
    pub fn purpose(&self) -> Option<&str> {
        self.purpose.as_deref()
    }

    pub fn notes(&self) -> Option<&str> {
        self.notes.as_deref()
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OutputTemplate {
    pub(crate) path: PathBuf,
    pub(crate) body: InstructionBody,
}

impl OutputTemplate {
    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn body(&self) -> &InstructionBody {
        &self.body
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct AgentStep {
    pub(crate) common: StepCommon,
    pub(crate) instructions: AgentInstructions,
    pub(crate) profile: Option<ProfileId>,
    pub(crate) isolation: Isolation,
    pub(crate) inject_context: bool,
    pub(crate) context: Option<AgentContext>,
    pub(crate) tools: ToolPolicy,
    pub(crate) model: ModelConfig,
    pub(crate) backend: AgentBackend,
    pub(crate) output: OutputContract,
    pub(crate) output_template: Option<OutputTemplate>,
    pub(crate) append_system_prompt: Option<String>,
    pub(crate) block_on: Option<crate::workflow::Condition>,
    pub(crate) security: SecurityOverride,
}

impl AgentStep {
    pub fn common(&self) -> &StepCommon {
        &self.common
    }

    pub fn instructions(&self) -> &AgentInstructions {
        &self.instructions
    }

    pub fn profile(&self) -> Option<&ProfileId> {
        self.profile.as_ref()
    }

    pub fn isolation(&self) -> Isolation {
        self.isolation
    }

    pub fn backend(&self) -> AgentBackend {
        self.backend
    }

    pub fn output_contract(&self) -> &OutputContract {
        &self.output
    }

    pub fn inject_context(&self) -> bool {
        self.inject_context
    }

    pub fn context(&self) -> Option<&AgentContext> {
        self.context.as_ref()
    }

    pub fn tools(&self) -> &ToolPolicy {
        &self.tools
    }

    pub fn model(&self) -> &ModelConfig {
        &self.model
    }

    pub fn output_template(&self) -> Option<&OutputTemplate> {
        self.output_template.as_ref()
    }

    pub fn append_system_prompt(&self) -> Option<&str> {
        self.append_system_prompt.as_deref()
    }

    pub fn block_on(&self) -> Option<&crate::workflow::Condition> {
        self.block_on.as_ref()
    }

    pub fn security(&self) -> &SecurityOverride {
        &self.security
    }
}
