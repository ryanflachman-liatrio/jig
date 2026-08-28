use std::fmt;
use std::path::{Path, PathBuf};

use thiserror::Error;

use super::StepId;
use crate::workflow::FieldPath;

#[derive(Clone, Debug, PartialEq)]
pub enum Input {
    Step {
        step: StepId,
        field: FieldPath,
        inline: bool,
    },
    Path {
        path: PathBuf,
        inline: bool,
    },
    User {
        label: String,
        name: InputName,
    },
}

impl Input {
    pub fn referenced_step(&self) -> Option<&StepId> {
        match self {
            Self::Step { step, .. } => Some(step),
            Self::Path { .. } | Self::User { .. } => None,
        }
    }

    pub fn path(&self) -> Option<&Path> {
        match self {
            Self::Path { path, .. } => Some(path),
            Self::Step { .. } | Self::User { .. } => None,
        }
    }
}

#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct InputName(String);

#[derive(Clone, Debug, Eq, Error, PartialEq)]
#[error("input name is empty")]
pub struct InputNameError;

impl TryFrom<String> for InputName {
    type Error = InputNameError;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        if value.trim().is_empty() {
            Err(InputNameError)
        } else {
            Ok(Self(value))
        }
    }
}

impl InputName {
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for InputName {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.as_str())
    }
}
