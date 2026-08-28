mod agent;
mod common;
mod defaults;
mod input;
mod output;
pub(super) mod profile;
mod review;

use std::collections::HashMap;
use std::path::{Path, PathBuf};

use thiserror::Error;

use super::raw::RawWorkflow;
use super::{
    AgentProfile, BudgetError, ConditionParseError, ProfileId, SchemaError, Step, StepIdError,
    Workflow, WorkflowDefaults, WorkflowDescription, WorkflowName, WorkflowVersion,
};

pub(crate) use defaults::{ModelFields, backend, convert_defaults, model_config, validate_hosts};

pub(crate) struct BuildContext<'a> {
    pub base_dir: Option<&'a Path>,
    pub profiles: HashMap<ProfileId, AgentProfile>,
    pub defaults: &'a WorkflowDefaults,
}

#[derive(Debug, Error)]
pub enum BuildError {
    #[error("{context}: required field {field:?} is missing")]
    Missing {
        context: String,
        field: &'static str,
    },
    #[error("{context}: fields {left:?} and {right:?} cannot be combined")]
    Conflict {
        context: String,
        left: &'static str,
        right: &'static str,
    },
    #[error("{context}: invalid {field} value {value:?}; expected {expected}")]
    InvalidValue {
        context: String,
        field: &'static str,
        value: String,
        expected: &'static str,
    },
    #[error("{context}: {message}")]
    Invalid { context: String, message: String },
    #[error("{context}: {path:?} was not found")]
    MissingResource { context: String, path: PathBuf },
    #[error("{context}: {source}")]
    StepId {
        context: String,
        #[source]
        source: StepIdError,
    },
    #[error("{context}: {source}")]
    Condition {
        context: String,
        #[source]
        source: ConditionParseError,
    },
    #[error("{context}: {source}")]
    Schema {
        context: String,
        #[source]
        source: SchemaError,
    },
    #[error("{context}: {source}")]
    Budget {
        context: String,
        #[source]
        source: BudgetError,
    },
    #[error(transparent)]
    Workflow(#[from] super::WorkflowBuildError),
}

pub(crate) fn build_workflow(
    raw: RawWorkflow,
    base_dir: Option<&Path>,
    profiles: Vec<AgentProfile>,
    source_toml: String,
) -> Result<Workflow, BuildError> {
    let name = WorkflowName::try_from(raw.meta.name).map_err(|error| BuildError::Invalid {
        context: "[workflow]".into(),
        message: error.to_string(),
    })?;
    let version =
        WorkflowVersion::try_from(raw.meta.version).map_err(|error| BuildError::Invalid {
            context: "[workflow]".into(),
            message: error.to_string(),
        })?;
    let description = WorkflowDescription::from_raw(raw.meta.description);
    let defaults = convert_defaults(raw.defaults)?;
    let context = BuildContext {
        base_dir,
        profiles: profiles
            .into_iter()
            .map(|profile| (profile.id().clone(), profile))
            .collect(),
        defaults: &defaults,
    };
    let steps = raw
        .steps
        .into_iter()
        .map(|step| convert_step(step, &context))
        .collect::<Result<Vec<_>, _>>()?;
    Workflow::new(name, version, description, defaults, steps, source_toml).map_err(Into::into)
}

fn convert_step(raw: super::raw::RawStep, context: &BuildContext<'_>) -> Result<Step, BuildError> {
    match raw.step_type.as_str() {
        "agent" => agent::convert(raw, context).map(|step| Step::Agent(Box::new(step))),
        "command" => common::convert_command(raw, context).map(Step::Command),
        "review" => review::convert(raw, context).map(Step::Review),
        "" => Err(BuildError::Missing {
            context: step_context(&raw.id),
            field: "type",
        }),
        value => Err(BuildError::InvalidValue {
            context: step_context(&raw.id),
            field: "type",
            value: value.into(),
            expected: "agent, command, or review",
        }),
    }
}

pub(crate) fn nonblank(value: String) -> Option<String> {
    (!value.trim().is_empty()).then_some(value)
}

pub(crate) fn step_context(id: &str) -> String {
    if id.is_empty() {
        "step".into()
    } else {
        format!("step {id:?}")
    }
}
