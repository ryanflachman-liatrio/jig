mod json;
mod schema;

use std::collections::HashSet;
use std::path::{Path, PathBuf};

use thiserror::Error;

pub use json::parse_json_schema;
pub(crate) use schema::parse_inline_schema;
pub use schema::{Field, FieldKind, Schema, SchemaError};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EnumValues(Box<[String]>);

#[derive(Clone, Debug, Eq, Error, PartialEq)]
pub enum EnumValuesError {
    #[error("enum must contain at least one value")]
    Empty,
    #[error("enum contains an empty value")]
    EmptyValue,
    #[error("enum contains duplicate value {0:?}")]
    Duplicate(String),
}

impl EnumValues {
    pub fn new(values: Vec<String>) -> Result<Self, EnumValuesError> {
        if values.is_empty() {
            return Err(EnumValuesError::Empty);
        }
        let mut seen = HashSet::with_capacity(values.len());
        for value in &values {
            if value.is_empty() {
                return Err(EnumValuesError::EmptyValue);
            }
            if !seen.insert(value.clone()) {
                return Err(EnumValuesError::Duplicate(value.clone()));
            }
        }
        Ok(Self(values.into_boxed_slice()))
    }

    pub fn as_slice(&self) -> &[String] {
        &self.0
    }

    pub fn contains(&self, value: &str) -> bool {
        self.0.iter().any(|candidate| candidate == value)
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OutputPath(PathBuf);

impl OutputPath {
    pub fn new(path: impl Into<PathBuf>) -> Self {
        Self(path.into())
    }

    pub fn as_path(&self) -> &Path {
        &self.0
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SchemaPath(PathBuf);

impl SchemaPath {
    pub fn new(path: impl Into<PathBuf>) -> Self {
        Self(path.into())
    }

    pub fn as_path(&self) -> &Path {
        &self.0
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum SchemaSource {
    Inline(Schema),
    File {
        path: SchemaPath,
        schema: Option<Schema>,
    },
}

impl SchemaSource {
    pub fn schema(&self) -> Option<&Schema> {
        match self {
            Self::Inline(schema) => Some(schema),
            Self::File { schema, .. } => schema.as_ref(),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum OutputContract {
    Text,
    Bool,
    Enum(EnumValues),
    Schema(SchemaSource),
}

pub fn merged_schema(declared: Option<&Schema>) -> Schema {
    let mut fields = base_schema().fields().to_vec();
    if let Some(declared) = declared {
        fields.extend_from_slice(declared.fields());
    }
    Schema::from_fields_unchecked(fields)
}

pub(crate) fn base_schema() -> Schema {
    let list_of_text = || FieldKind::List(Box::new(FieldKind::Text));
    Schema::from_fields_unchecked(vec![
        Field::new("assumptions", list_of_text()),
        Field::new(
            "confidence",
            FieldKind::Enum(EnumValues::new(strings(&["high", "medium", "low"])).unwrap()),
        ),
        Field::new("issues", list_of_text()),
        Field::new(
            "status",
            FieldKind::Enum(
                EnumValues::new(strings(&["succeeded", "partial", "failed", "blocked"])).unwrap(),
            ),
        ),
        Field::new("summary", FieldKind::Text),
    ])
}

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}
