mod condition;
mod graph;
mod references;

use std::fmt;

use thiserror::Error;

use super::{StepId, Workflow};

#[derive(Clone, Debug, Eq, Error, PartialEq)]
pub enum ValidationProblem {
    #[error("workflow has no steps")]
    EmptyWorkflow,
    #[error("duplicate step id {0}")]
    DuplicateStep(StepId),
    #[error("step {step} depends on itself")]
    SelfDependency { step: StepId },
    #[error("step {step} references unknown dependency {dependency}")]
    UnknownDependency { step: StepId, dependency: StepId },
    #[error("dependency cycle through steps {from} and {to}")]
    DependencyCycle { from: StepId, to: StepId },
    #[error("step {step} {relation} references unknown step {target}")]
    UnknownReference {
        step: StepId,
        relation: &'static str,
        target: StepId,
    },
    #[error("step {step} {relation} references {target}, which must be in depends_on")]
    MissingDependency {
        step: StepId,
        relation: &'static str,
        target: StepId,
    },
    #[error("step {step} {relation} must reference that step's own output")]
    NotSelfReference {
        step: StepId,
        relation: &'static str,
    },
    #[error("step {step} {relation} references missing field {field:?} on {target}")]
    MissingField {
        step: StepId,
        relation: &'static str,
        target: StepId,
        field: String,
    },
    #[error("step {step} {relation}: {message}")]
    InvalidComparison {
        step: StepId,
        relation: &'static str,
        message: String,
    },
    #[error("step {step} validation checks output but no output path is declared")]
    MissingOutput { step: StepId },
    #[error("review step {step} field target must resolve to text")]
    ReviewFieldNotText { step: StepId },
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ValidationError {
    pub problems: Vec<ValidationProblem>,
}

impl fmt::Display for ValidationError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        let noun = if self.problems.len() == 1 {
            "problem"
        } else {
            "problems"
        };
        writeln!(
            formatter,
            "invalid workflow ({} {noun}):",
            self.problems.len()
        )?;
        for problem in &self.problems {
            writeln!(formatter, "  - {problem}")?;
        }
        Ok(())
    }
}

impl std::error::Error for ValidationError {}

pub(crate) fn validate(workflow: &mut Workflow) -> Result<(), ValidationError> {
    let mut problems = graph::validate(workflow);
    workflow.set_index(graph::index(workflow.steps()));
    references::validate(workflow, &mut problems);
    if problems.is_empty() {
        Ok(())
    } else {
        Err(ValidationError { problems })
    }
}
