use super::{FailureBehavior, Input, StepId};
use crate::workflow::{Condition, OutputPath, SchemaPath};

#[derive(Clone, Debug, PartialEq)]
pub struct StepCommon {
    pub(crate) id: StepId,
    pub(crate) depends_on: Vec<StepId>,
    pub(crate) when: Option<Condition>,
    pub(crate) output: Option<OutputPath>,
    pub(crate) failure: FailureBehavior,
    pub(crate) inputs: Vec<Input>,
    pub(crate) validation: Option<StepValidation>,
    pub(crate) loop_config: Option<LoopConfig>,
}

impl StepCommon {
    pub fn id(&self) -> &StepId {
        &self.id
    }

    pub fn dependencies(&self) -> &[StepId] {
        &self.depends_on
    }

    pub fn when(&self) -> Option<&Condition> {
        self.when.as_ref()
    }

    pub fn output(&self) -> Option<&OutputPath> {
        self.output.as_ref()
    }

    pub fn failure(&self) -> FailureBehavior {
        self.failure
    }

    pub fn inputs(&self) -> &[Input] {
        &self.inputs
    }

    pub fn validation(&self) -> Option<&StepValidation> {
        self.validation.as_ref()
    }

    pub fn loop_config(&self) -> Option<&LoopConfig> {
        self.loop_config.as_ref()
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct StepValidation {
    pub(crate) command: Option<String>,
    pub(crate) output_schema: Option<SchemaPath>,
    pub(crate) output_exists: bool,
    pub(crate) output_contains: Option<String>,
}

impl StepValidation {
    pub fn command(&self) -> Option<&str> {
        self.command.as_deref()
    }

    pub fn output_schema(&self) -> Option<&SchemaPath> {
        self.output_schema.as_ref()
    }

    pub fn requires_output(&self) -> bool {
        self.output_schema.is_some() || self.output_exists || self.output_contains.is_some()
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct LoopConfig {
    pub(crate) condition: Condition,
    pub(crate) goto: StepId,
    pub(crate) max_iterations: std::num::NonZeroUsize,
    pub(crate) feedback: Option<StepId>,
}

impl LoopConfig {
    pub fn condition(&self) -> &Condition {
        &self.condition
    }

    pub fn goto(&self) -> &StepId {
        &self.goto
    }

    pub fn max_iterations(&self) -> std::num::NonZeroUsize {
        self.max_iterations
    }

    pub fn feedback(&self) -> Option<&StepId> {
        self.feedback.as_ref()
    }
}
