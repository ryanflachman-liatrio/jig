use jig_workflow::decode;

#[test]
fn rejects_invalid_graphs_and_references() {
    let cases = [
        (
            "unknown dependency",
            command_workflow("depends_on = [\"ghost\"]"),
            "unknown dependency",
        ),
        (
            "dependency cycle",
            r#"
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
depends_on = ["b"]
[[step]]
id = "b"
type = "command"
run = "true"
depends_on = ["a"]
"#
            .to_owned(),
            "cycle",
        ),
        (
            "unlisted input dependency",
            r#"
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
[[step]]
id = "b"
type = "command"
run = "true"
inputs = ["@a"]
"#
            .to_owned(),
            "depends_on",
        ),
    ];

    for (name, source, expected) in cases {
        let error = decode(&source, None).unwrap_err().to_string();
        assert!(error.contains(expected), "{name}: {error}");
    }
}

#[test]
fn rejects_values_that_do_not_fit_producer_types() {
    let source = r#"
[workflow]
name = "x"
version = "1"
[[step]]
id = "gate"
type = "review"
output_type = { enum = ["yes", "no"] }
[[step.review]]
source = "diff"
label = "Changes"
[[step]]
id = "next"
type = "command"
run = "true"
depends_on = ["gate"]
when = "gate == 'maybe'"
"#;

    let error = decode(source, None).unwrap_err().to_string();
    assert!(error.contains("not in the output enum"), "{error}");
}

#[test]
fn rejects_fields_from_the_wrong_step_variant() {
    let source = command_workflow("append_system_prompt = \"be terse\"");
    let error = decode(&source, None).unwrap_err().to_string();
    assert!(error.contains("agent"), "{error}");
}

#[test]
fn serde_rejects_unknown_keys() {
    let source = command_workflow("runn = \"typo\"");
    let error = decode(&source, None).unwrap_err().to_string();
    assert!(error.contains("unknown field"), "{error}");
}

fn command_workflow(extra: &str) -> String {
    format!(
        r#"
[workflow]
name = "x"
version = "1"
[[step]]
id = "a"
type = "command"
run = "true"
{extra}
"#
    )
}
