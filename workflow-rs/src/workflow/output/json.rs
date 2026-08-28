use serde_json::{Map, Value, json};

use super::{EnumValues, Field, FieldKind, Schema, SchemaError};

pub fn parse_json_schema(data: &[u8]) -> Result<Schema, SchemaError> {
    let root: Value =
        serde_json::from_slice(data).map_err(|_| SchemaError::InvalidSpec("$".into()))?;
    let object = root.as_object().ok_or_else(|| SchemaError::UnknownType {
        field: "$".into(),
        kind: String::new(),
    })?;
    if schema_type(object) != "object" {
        return Err(SchemaError::UnknownType {
            field: "$".into(),
            kind: schema_type(object).into(),
        });
    }
    Schema::new(object_fields(object)?)
}

pub(crate) fn object_node(fields: &[Field]) -> Value {
    let properties = fields
        .iter()
        .map(|field| (field.name().to_owned(), field_node(field.kind())))
        .collect::<Map<_, _>>();
    let mut required = fields.iter().map(|field| field.name()).collect::<Vec<_>>();
    required.sort_unstable();
    json!({
        "type": "object",
        "properties": properties,
        "required": required,
        "additionalProperties": false
    })
}

fn field_node(kind: &FieldKind) -> Value {
    match kind {
        FieldKind::Text => json!({"type": "string"}),
        FieldKind::Number => json!({"type": "number"}),
        FieldKind::Bool => json!({"type": "boolean"}),
        FieldKind::Enum(values) => json!({"type": "string", "enum": values.as_slice()}),
        FieldKind::List(element) => json!({"type": "array", "items": field_node(element)}),
        FieldKind::Object(schema) => object_node(schema.fields()),
        FieldKind::Any => json!({}),
    }
}

fn object_fields(node: &Map<String, Value>) -> Result<Vec<Field>, SchemaError> {
    let Some(properties) = node.get("properties").and_then(Value::as_object) else {
        return Ok(Vec::new());
    };
    let mut names = properties.keys().collect::<Vec<_>>();
    names.sort_unstable();
    names
        .into_iter()
        .map(|name| json_field(name, properties[name].as_object()))
        .collect()
}

fn json_field(name: &str, node: Option<&Map<String, Value>>) -> Result<Field, SchemaError> {
    let Some(node) = node else {
        return Ok(Field::new(name, FieldKind::Any));
    };
    if let Some(values) = node.get("enum").and_then(Value::as_array) {
        let values = values
            .iter()
            .map(|value| {
                value
                    .as_str()
                    .map(str::to_owned)
                    .unwrap_or_else(|| value.to_string())
            })
            .collect();
        let values = EnumValues::new(values)
            .map_err(|error| SchemaError::InvalidEnum(name.to_owned(), error))?;
        return Ok(Field::new(name, FieldKind::Enum(values)));
    }
    let kind = match schema_type(node) {
        "string" => FieldKind::Text,
        "number" | "integer" => FieldKind::Number,
        "boolean" => FieldKind::Bool,
        "array" => FieldKind::List(Box::new(
            json_field(
                &format!("{name}[]"),
                node.get("items").and_then(Value::as_object),
            )?
            .kind()
            .clone(),
        )),
        "object" => FieldKind::Object(Schema::from_fields_unchecked(object_fields(node)?)),
        _ => FieldKind::Any,
    };
    Ok(Field::new(name, kind))
}

fn schema_type(node: &Map<String, Value>) -> &str {
    match node.get("type") {
        Some(Value::String(value)) => value,
        Some(Value::Array(values)) => values
            .iter()
            .find_map(|value| value.as_str().filter(|value| *value != "null"))
            .unwrap_or(""),
        _ => "",
    }
}
