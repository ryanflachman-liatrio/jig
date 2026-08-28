use std::fs;

use super::{BuildContext, BuildError};
use crate::workflow::output::{base_schema, parse_inline_schema};
use crate::workflow::raw::RawOutputType;
use crate::workflow::{EnumValues, OutputContract, SchemaPath, SchemaSource, parse_json_schema};

pub(crate) fn scalar_contract(
    raw: Option<RawOutputType>,
    context: &str,
) -> Result<OutputContract, BuildError> {
    match raw {
        None => Ok(OutputContract::Text),
        Some(RawOutputType::Kind(kind)) if kind.is_empty() || kind == "text" => {
            Ok(OutputContract::Text)
        }
        Some(RawOutputType::Kind(kind)) if kind == "bool" => Ok(OutputContract::Bool),
        Some(RawOutputType::Kind(kind)) => Err(BuildError::InvalidValue {
            context: context.into(),
            field: "output_type",
            value: kind,
            expected: "text, bool, or an enum table",
        }),
        Some(RawOutputType::Enum { values }) => {
            let values = EnumValues::new(values).map_err(|error| BuildError::Invalid {
                context: context.into(),
                message: format!("invalid output enum: {error}"),
            })?;
            Ok(OutputContract::Enum(values))
        }
    }
}

pub(crate) fn agent_contract(
    output_type: Option<RawOutputType>,
    inline: Option<toml::Table>,
    schema_file: String,
    context: &str,
    build: &BuildContext<'_>,
) -> Result<OutputContract, BuildError> {
    if inline.is_some() && !schema_file.is_empty() {
        return Err(BuildError::Conflict {
            context: context.into(),
            left: "schema",
            right: "schema_file",
        });
    }
    let scalar_is_text = match &output_type {
        None => true,
        Some(RawOutputType::Kind(kind)) => kind.is_empty() || kind == "text",
        Some(RawOutputType::Enum { .. }) => false,
    };
    if (inline.is_some() || !schema_file.is_empty()) && !scalar_is_text {
        return Err(BuildError::Conflict {
            context: context.into(),
            left: "output_type",
            right: "schema or schema_file",
        });
    }
    if let Some(table) = inline {
        let schema = parse_inline_schema(&table).map_err(|source| BuildError::Schema {
            context: context.into(),
            source,
        })?;
        reject_base_fields(&schema, context)?;
        return Ok(OutputContract::Schema(SchemaSource::Inline(schema)));
    }
    if !schema_file.is_empty() {
        let path = SchemaPath::new(schema_file);
        let schema = if let Some(base_dir) = build.base_dir {
            let resolved = base_dir.join(path.as_path());
            let data = fs::read(&resolved).map_err(|_| BuildError::MissingResource {
                context: context.into(),
                path: resolved,
            })?;
            let schema = parse_json_schema(&data).map_err(|source| BuildError::Schema {
                context: context.into(),
                source,
            })?;
            reject_base_fields(&schema, context)?;
            Some(schema)
        } else {
            None
        };
        return Ok(OutputContract::Schema(SchemaSource::File { path, schema }));
    }
    scalar_contract(output_type, context)
}

fn reject_base_fields(schema: &crate::workflow::Schema, context: &str) -> Result<(), BuildError> {
    let reserved = base_schema();
    if let Some(field) = schema.fields().iter().find(|field| {
        reserved
            .fields()
            .iter()
            .any(|base| base.name() == field.name())
    }) {
        return Err(BuildError::Invalid {
            context: context.into(),
            message: format!(
                "schema field {:?} is reserved by the base schema",
                field.name()
            ),
        });
    }
    Ok(())
}
