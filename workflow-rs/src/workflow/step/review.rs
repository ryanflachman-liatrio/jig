use std::path::{Path, PathBuf};

use super::{StepCommon, StepId};
use crate::workflow::{EnumValues, FieldPath};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ReviewFile {
    authored: PathBuf,
    resolved: Option<PathBuf>,
}

impl ReviewFile {
    pub(crate) fn new(authored: PathBuf, resolved: Option<PathBuf>) -> Self {
        Self { authored, resolved }
    }

    pub fn authored(&self) -> &Path {
        &self.authored
    }

    pub fn resolved(&self) -> Option<&Path> {
        self.resolved.as_deref()
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ReviewSource {
    Diff,
    Step { step: StepId, field: FieldPath },
    File(ReviewFile),
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ReviewTarget {
    pub(crate) source: ReviewSource,
    pub(crate) label: String,
}

impl ReviewTarget {
    pub fn source(&self) -> &ReviewSource {
        &self.source
    }

    pub fn label(&self) -> &str {
        &self.label
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ReviewVerdict {
    Bool,
    Enum(EnumValues),
}

#[derive(Clone, Debug, PartialEq)]
pub struct ReviewStep {
    pub(crate) common: StepCommon,
    pub(crate) targets: Box<[ReviewTarget]>,
    pub(crate) verdict: ReviewVerdict,
}

impl ReviewStep {
    pub fn common(&self) -> &StepCommon {
        &self.common
    }

    pub fn targets(&self) -> &[ReviewTarget] {
        &self.targets
    }

    pub fn verdict(&self) -> &ReviewVerdict {
        &self.verdict
    }
}
