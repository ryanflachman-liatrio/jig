use std::collections::HashMap;
use std::num::NonZeroUsize;
use std::path::{Path, PathBuf};

use thiserror::Error;

use super::{AgentBackend, ModelConfig, SecurityConfig, Step, StepId, ValidationError};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct WorkflowName(String);

#[derive(Clone, Debug, Eq, Error, PartialEq)]
#[error("workflow name is empty")]
pub struct WorkflowNameError;

impl TryFrom<String> for WorkflowName {
    type Error = WorkflowNameError;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        if value.trim().is_empty() {
            Err(WorkflowNameError)
        } else {
            Ok(Self(value))
        }
    }
}

impl WorkflowName {
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct WorkflowVersion(String);

#[derive(Clone, Debug, Eq, Error, PartialEq)]
#[error("workflow version is empty")]
pub struct WorkflowVersionError;

impl TryFrom<String> for WorkflowVersion {
    type Error = WorkflowVersionError;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        if value.trim().is_empty() {
            Err(WorkflowVersionError)
        } else {
            Ok(Self(value))
        }
    }
}

impl WorkflowVersion {
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct WorkflowDescription(String);

impl WorkflowDescription {
    pub(crate) fn from_raw(value: String) -> Option<Self> {
        (!value.trim().is_empty()).then_some(Self(value))
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct WorkflowDefaults {
    pub(crate) model: ModelConfig,
    pub(crate) backend: AgentBackend,
    pub(crate) cwd: Option<PathBuf>,
    pub(crate) max_parallel: NonZeroUsize,
    pub(crate) artifacts_dir: PathBuf,
    pub(crate) inject_context: bool,
    pub(crate) security: SecurityConfig,
}

impl WorkflowDefaults {
    pub fn model(&self) -> &ModelConfig {
        &self.model
    }

    pub fn backend(&self) -> AgentBackend {
        self.backend
    }

    pub fn cwd(&self) -> Option<&Path> {
        self.cwd.as_deref()
    }

    pub fn max_parallel(&self) -> NonZeroUsize {
        self.max_parallel
    }

    pub fn artifacts_dir(&self) -> &Path {
        &self.artifacts_dir
    }

    pub fn inject_context(&self) -> bool {
        self.inject_context
    }

    pub fn security(&self) -> &SecurityConfig {
        &self.security
    }
}

#[derive(Clone, Debug)]
pub struct Workflow {
    name: WorkflowName,
    version: WorkflowVersion,
    description: Option<WorkflowDescription>,
    defaults: WorkflowDefaults,
    steps: Box<[Step]>,
    index: HashMap<StepId, usize>,
    source_path: Option<PathBuf>,
    source_toml: String,
}

#[derive(Debug, Error)]
pub enum WorkflowBuildError {
    #[error(transparent)]
    Invalid(#[from] ValidationError),
}

impl Workflow {
    pub(crate) fn new(
        name: WorkflowName,
        version: WorkflowVersion,
        description: Option<WorkflowDescription>,
        defaults: WorkflowDefaults,
        steps: Vec<Step>,
        source_toml: String,
    ) -> Result<Self, WorkflowBuildError> {
        let mut workflow = Self {
            name,
            version,
            description,
            defaults,
            steps: steps.into_boxed_slice(),
            index: HashMap::new(),
            source_path: None,
            source_toml,
        };
        super::validation::validate(&mut workflow)?;
        Ok(workflow)
    }

    pub fn name(&self) -> &WorkflowName {
        &self.name
    }

    pub fn version(&self) -> &WorkflowVersion {
        &self.version
    }

    pub fn description(&self) -> Option<&WorkflowDescription> {
        self.description.as_ref()
    }

    pub fn defaults(&self) -> &WorkflowDefaults {
        &self.defaults
    }

    pub fn steps(&self) -> &[Step] {
        &self.steps
    }

    pub fn step(&self, id: &StepId) -> Option<&Step> {
        self.index.get(id).map(|index| &self.steps[*index])
    }

    pub fn source(&self) -> (Option<&Path>, &str) {
        (self.source_path.as_deref(), &self.source_toml)
    }

    pub(crate) fn set_source_path(&mut self, path: PathBuf) {
        self.source_path = Some(path);
    }

    pub(crate) fn set_index(&mut self, index: HashMap<StepId, usize>) {
        self.index = index;
    }
}
