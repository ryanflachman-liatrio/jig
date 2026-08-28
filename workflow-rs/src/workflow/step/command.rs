use std::path::{Path, PathBuf};

use super::StepCommon;
use crate::workflow::OutputContract;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ScriptPath(PathBuf);

impl ScriptPath {
    pub fn new(path: impl Into<PathBuf>) -> Self {
        Self(path.into())
    }

    pub fn as_path(&self) -> &Path {
        &self.0
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum CommandSource {
    Inline(String),
    Script(ScriptPath),
}

#[derive(Clone, Debug, PartialEq)]
pub struct CommandStep {
    pub(crate) common: StepCommon,
    pub(crate) source: CommandSource,
    pub(crate) output: OutputContract,
}

impl CommandStep {
    pub fn common(&self) -> &StepCommon {
        &self.common
    }

    pub fn source(&self) -> &CommandSource {
        &self.source
    }

    pub fn output_contract(&self) -> &OutputContract {
        &self.output
    }
}
