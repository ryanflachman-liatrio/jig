use std::path::PathBuf;

use super::{BuildError, step_context};
use crate::workflow::raw::{RawInput, RawInputTable};
use crate::workflow::{FieldPath, Input, InputName, StepId};

pub(crate) fn convert(
    raw: Vec<RawInput>,
    step_id: &str,
    allow_user: bool,
) -> Result<Vec<Input>, BuildError> {
    raw.into_iter()
        .map(|input| convert_one(input, step_id, allow_user))
        .collect()
}

fn convert_one(raw: RawInput, step_id: &str, allow_user: bool) -> Result<Input, BuildError> {
    match raw {
        RawInput::String(value) => {
            if let Some(reference) = value.strip_prefix('@') {
                step_input(reference, false, step_id)
            } else {
                Ok(Input::Path {
                    path: PathBuf::from(value),
                    inline: false,
                })
            }
        }
        RawInput::Table(table) => table_input(table, step_id, allow_user),
    }
}

fn table_input(raw: RawInputTable, step_id: &str, allow_user: bool) -> Result<Input, BuildError> {
    let context = step_context(step_id);
    let choices = usize::from(raw.reference.is_some())
        + usize::from(raw.path.is_some())
        + usize::from(raw.from.is_some());
    if choices == 0 {
        return Err(BuildError::Missing {
            context,
            field: "inputs.ref, inputs.path, or inputs.from",
        });
    }
    if choices > 1 {
        return Err(BuildError::Invalid {
            context,
            message: "an input must select exactly one of ref, path, or from".into(),
        });
    }
    if let Some(reference) = raw.reference {
        return step_input(reference.trim_start_matches('@'), raw.inline, step_id);
    }
    if let Some(path) = raw.path {
        return Ok(Input::Path {
            path: PathBuf::from(path),
            inline: raw.inline,
        });
    }
    if !allow_user {
        return Err(BuildError::Invalid {
            context,
            message: "from=\"user\" input is only valid on agent steps".into(),
        });
    }
    if raw.inline {
        return Err(BuildError::Invalid {
            context,
            message: "from=\"user\" input cannot be inline".into(),
        });
    }
    if raw.from.as_deref() != Some("user") {
        return Err(BuildError::InvalidValue {
            context,
            field: "inputs.from",
            value: raw.from.unwrap_or_default(),
            expected: "user",
        });
    }
    let label = raw
        .label
        .filter(|value| !value.trim().is_empty())
        .ok_or_else(|| BuildError::Missing {
            context: step_context(step_id),
            field: "inputs.label",
        })?;
    let name = raw.name.ok_or_else(|| BuildError::Missing {
        context: step_context(step_id),
        field: "inputs.as",
    })?;
    let name = InputName::try_from(name).map_err(|error| BuildError::Invalid {
        context: step_context(step_id),
        message: error.to_string(),
    })?;
    Ok(Input::User { label, name })
}

fn step_input(reference: &str, inline: bool, owner: &str) -> Result<Input, BuildError> {
    let mut segments = reference.split('.');
    let raw_step = segments.next().unwrap_or_default();
    let step = StepId::try_from(raw_step).map_err(|source| BuildError::StepId {
        context: format!("{} input reference", step_context(owner)),
        source,
    })?;
    let fields = segments.map(str::to_owned).collect::<Vec<_>>();
    if fields
        .iter()
        .any(|field| StepId::try_from(field.as_str()).is_err())
    {
        return Err(BuildError::Invalid {
            context: step_context(owner),
            message: format!("input reference {reference:?} has an invalid field path"),
        });
    }
    Ok(Input::Step {
        step,
        field: FieldPath::from_segments(fields),
        inline,
    })
}
