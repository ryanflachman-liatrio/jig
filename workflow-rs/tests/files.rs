use std::fs;

use jig_workflow::{AgentInstructions, InstructionBody, SchemaSource, Step, StepId, decode};
use tempfile::tempdir;

#[test]
fn resolves_skill_and_schema_files_at_the_boundary() {
    let directory = tempdir().unwrap();
    let skill_dir = directory.path().join("skills/triage");
    let schema_dir = directory.path().join("schemas");
    fs::create_dir_all(&skill_dir).unwrap();
    fs::create_dir_all(&schema_dir).unwrap();
    fs::write(
        skill_dir.join("SKILL.md"),
        "---\nname: triage\ndescription: Triage issues\n---\nClassify the issue.",
    )
    .unwrap();
    fs::write(
        schema_dir.join("triage.json"),
        r#"{"type":"object","properties":{"priority":{"type":"string","enum":["low","high"]}}}"#,
    )
    .unwrap();

    let workflow = decode(
        r#"
[workflow]
name = "files"
version = "1"
[[step]]
id = "triage"
type = "agent"
skill = "skills/triage"
schema_file = "schemas/triage.json"
"#,
        Some(directory.path()),
    )
    .unwrap();

    let Step::Agent(step) = workflow.step(&StepId::new("triage").unwrap()).unwrap() else {
        panic!("triage should be an agent step")
    };
    assert!(matches!(
        step.instructions(),
        AgentInstructions::Skill {
            body: InstructionBody::Resolved(body),
            ..
        } if body == "Classify the issue."
    ));
    assert!(matches!(
        step.output_contract(),
        jig_workflow::OutputContract::Schema(SchemaSource::File {
            schema: Some(_),
            ..
        })
    ));
}

#[test]
fn structural_decode_keeps_file_resources_explicitly_unresolved() {
    let workflow = decode(
        r#"
[workflow]
name = "structural"
version = "1"
[[step]]
id = "draft"
type = "agent"
skill = "skills/missing"
"#,
        None,
    )
    .unwrap();

    let Step::Agent(step) = &workflow.steps()[0] else {
        panic!("draft should be an agent step")
    };
    assert!(matches!(
        step.instructions(),
        AgentInstructions::Skill {
            body: InstructionBody::Unresolved,
            ..
        }
    ));
}

#[test]
fn checks_validation_schema_files_when_resolving_resources() {
    let directory = tempdir().unwrap();
    let error = decode(
        r#"
[workflow]
name = "validation"
version = "1"
[[step]]
id = "build"
type = "command"
run = "true"
output = "result.json"
[step.validate]
output_schema = "schemas/missing.json"
"#,
        Some(directory.path()),
    )
    .unwrap_err()
    .to_string();

    assert!(error.contains("schemas/missing.json"), "{error}");
}
