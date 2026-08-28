mod agent;
mod command;
mod common;
mod failure;
mod id;
mod input;
mod review;
mod security;

pub use agent::{
    AgentBackend, AgentContext, AgentFilePath, AgentInstructions, AgentStep, BudgetError,
    BudgetUsd, ClaudeTransport, EffortLevel, InstructionBody, Isolation, ModelConfig,
    OutputTemplate, PermissionMode, SkillPath, ToolPolicy,
};
pub use command::{CommandSource, CommandStep, ScriptPath};
pub use common::{LoopConfig, StepCommon, StepValidation};
pub use failure::FailureBehavior;
pub use id::{StepId, StepIdError};
pub use input::{Input, InputName, InputNameError};
pub use review::{ReviewFile, ReviewSource, ReviewStep, ReviewTarget, ReviewVerdict};
pub use security::{SecurityConfig, SecurityOverride};

#[derive(Clone, Debug, PartialEq)]
pub enum Step {
    Agent(Box<AgentStep>),
    Command(CommandStep),
    Review(ReviewStep),
}

impl Step {
    pub fn common(&self) -> &StepCommon {
        match self {
            Self::Agent(step) => step.common(),
            Self::Command(step) => step.common(),
            Self::Review(step) => step.common(),
        }
    }

    pub fn id(&self) -> &StepId {
        self.common().id()
    }

    pub fn kind(&self) -> StepKind {
        match self {
            Self::Agent(_) => StepKind::Agent,
            Self::Command(_) => StepKind::Command,
            Self::Review(_) => StepKind::Review,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum StepKind {
    Agent,
    Command,
    Review,
}
