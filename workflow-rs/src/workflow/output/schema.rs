use std::collections::{BTreeMap, HashSet};

use thiserror::Error;
use toml::Value;

use super::{EnumValues, EnumValuesError};

#[derive(Clone, Debug, PartialEq)]
pub struct Field {
    name: String,
    kind: FieldKind,
}

impl Field {
    pub(crate) fn new(name: impl Into<String>, kind: FieldKind) -> Self {
        Self {
            name: name.into(),
            kind,
        }
    }

    pub fn name(&self) -> &str {
        &self.name
    }

    pub fn kind(&self) -> &FieldKind {
        &self.kind
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum FieldKind {
    Text,
    Number,
    Bool,
    Enum(EnumValues),
    List(Box<FieldKind>),
    Object(Schema),
    Any,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Schema {
    fields: Box<[Field]>,
}

#[derive(Clone, Debug, Error, PartialEq)]
pub enum SchemaError {
    #[error("schema declares no fields")]
    Empty,
    #[error("field {field:?} has unknown type {kind:?}")]
    UnknownType { field: String, kind: String },
    #[error("field {0:?} has an invalid enum: {1}")]
    InvalidEnum(String, EnumValuesError),
    #[error("field {0:?} is an empty object")]
    EmptyObject(String),
    #[error("field {0:?} must be a string or table")]
    InvalidSpec(String),
    #[error("schema contains duplicate field {0:?}")]
    DuplicateField(String),
}

impl Schema {
    pub fn new(fields: Vec<Field>) -> Result<Self, SchemaError> {
        if fields.is_empty() {
            return Err(SchemaError::Empty);
        }
        let mut seen = HashSet::with_capacity(fields.len());
        for field in &fields {
            if !seen.insert(field.name.clone()) {
                return Err(SchemaError::DuplicateField(field.name.clone()));
            }
        }
        Ok(Self {
            fields: fields.into_boxed_slice(),
        })
    }

    pub(crate) fn from_fields_unchecked(fields: Vec<Field>) -> Self {
        Self {
            fields: fields.into_boxed_slice(),
        }
    }

    pub fn fields(&self) -> &[Field] {
        &self.fields
    }

    pub fn lookup<'a>(&'a self, path: &[String]) -> Option<&'a Field> {
        let mut fields = self.fields();
        let mut current = None;
        for segment in path {
            current = fields.iter().find(|field| field.name == *segment);
            fields = match current?.kind() {
                FieldKind::Object(schema) => schema.fields(),
                _ => &[],
            };
        }
        current
    }

    pub fn to_json_schema(&self) -> Vec<u8> {
        serde_json::to_vec(&super::json::object_node(self.fields()))
            .expect("schema contains only serializable values")
    }
}

pub(crate) fn parse_inline_schema(table: &toml::Table) -> Result<Schema, SchemaError> {
    let fields = sorted_entries(table)
        .map(|(name, value)| parse_field(name, value))
        .collect::<Result<Vec<_>, _>>()?;
    Schema::new(fields)
}

fn parse_field(name: &str, value: &Value) -> Result<Field, SchemaError> {
    if let Some(kind) = value.as_str() {
        return Ok(Field::new(
            name,
            match kind {
                "text" => FieldKind::Text,
                "number" => FieldKind::Number,
                "bool" => FieldKind::Bool,
                _ => {
                    return Err(SchemaError::UnknownType {
                        field: name.into(),
                        kind: kind.into(),
                    });
                }
            },
        ));
    }

    let table = value
        .as_table()
        .ok_or_else(|| SchemaError::InvalidSpec(name.into()))?;
    if let Some(values) = table.get("enum") {
        let values = values
            .as_array()
            .ok_or_else(|| SchemaError::InvalidSpec(name.into()))?;
        let values = values
            .iter()
            .map(|value| {
                value
                    .as_str()
                    .map(str::to_owned)
                    .ok_or_else(|| SchemaError::InvalidSpec(name.into()))
            })
            .collect::<Result<Vec<_>, _>>()?;
        let values = EnumValues::new(values)
            .map_err(|error| SchemaError::InvalidEnum(name.into(), error))?;
        return Ok(Field::new(name, FieldKind::Enum(values)));
    }
    if let Some(element) = table.get("list") {
        return Ok(Field::new(
            name,
            FieldKind::List(Box::new(parse_field(&format!("{name}[]"), element)?.kind)),
        ));
    }

    let object = parse_inline_schema(table).map_err(|error| match error {
        SchemaError::Empty => SchemaError::EmptyObject(name.into()),
        other => other,
    })?;
    Ok(Field::new(name, FieldKind::Object(object)))
}

fn sorted_entries(table: &toml::Table) -> impl Iterator<Item = (&str, &Value)> {
    let ordered = table
        .iter()
        .map(|(name, value)| (name.as_str(), value))
        .collect::<BTreeMap<_, _>>();
    ordered.into_iter()
}
