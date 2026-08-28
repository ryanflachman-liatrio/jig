use std::fs;
use std::path::Path;

use serde::Deserialize;

use super::raw::RawMeta;
use super::{LoadError, WorkflowDescription, WorkflowName, WorkflowVersion};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct WorkflowMeta {
    pub name: WorkflowName,
    pub version: Option<WorkflowVersion>,
    pub description: Option<WorkflowDescription>,
}

pub fn load_meta(path: impl AsRef<Path>) -> Result<Option<WorkflowMeta>, LoadError> {
    let path = path.as_ref();
    let data = fs::read_to_string(path).map_err(|source| LoadError::Io {
        path: path.to_owned(),
        source,
    })?;
    decode_meta(&data)
}

pub fn decode_meta(data: &str) -> Result<Option<WorkflowMeta>, LoadError> {
    #[derive(Default, Deserialize)]
    #[serde(default)]
    struct Document {
        workflow: RawMeta,
    }

    let document: Document = toml::from_str(data).map_err(LoadError::Toml)?;
    let Ok(name) = WorkflowName::try_from(document.workflow.name) else {
        return Ok(None);
    };
    Ok(Some(WorkflowMeta {
        name,
        version: WorkflowVersion::try_from(document.workflow.version).ok(),
        description: WorkflowDescription::from_raw(document.workflow.description),
    }))
}
