use jig_workflow::{FieldKind, SchemaError, parse_json_schema};

#[test]
fn parses_nested_json_schema_into_typed_fields() {
    let schema = parse_json_schema(
        br#"{
          "type": "object",
          "properties": {
            "priority": {"type": "string", "enum": ["low", "high"]},
            "details": {
              "type": "object",
              "properties": {"score": {"type": "number"}}
            }
          }
        }"#,
    )
    .unwrap();

    assert!(matches!(
        schema.lookup(&["priority".into()]).unwrap().kind(),
        FieldKind::Enum(values) if values.contains("high")
    ));
    assert!(matches!(
        schema
            .lookup(&["details".into(), "score".into()])
            .unwrap()
            .kind(),
        FieldKind::Number
    ));

    let rendered: serde_json::Value = serde_json::from_slice(&schema.to_json_schema()).unwrap();
    assert_eq!(rendered["additionalProperties"], false);
}

#[test]
fn malformed_enum_is_a_typed_error_instead_of_a_panic() {
    let error = parse_json_schema(
        br#"{"type":"object","properties":{"status":{"type":"string","enum":[]}}}"#,
    )
    .unwrap_err();

    assert!(matches!(error, SchemaError::InvalidEnum(_, _)));
}
