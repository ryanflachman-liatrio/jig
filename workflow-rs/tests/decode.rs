use jig_workflow::{
    AgentBackend, ClaudeTransport, CommandSource, Input, Isolation, Step, StepId, decode,
};

const WORKFLOW: &str = r#"
[workflow]
name = "bugfix"
version = "1"

[defaults]
permission_mode = "acceptEdits"

[[step]]
id = "triage"
type = "agent"
skill = "skills/triage"
inputs = ["reports/bug.md", { path = "conventions.md", inline = true }]
allowed_tools = ["Read", "Grep"]

[[step]]
id = "fix"
type = "agent"
depends_on = ["triage"]
skill = "skills/fix"
inputs = ["@triage"]
allowed_tools = ["Read", "Edit", "Bash"]

[[step]]
id = "approve"
type = "review"
depends_on = ["fix"]
output_type = { enum = ["approve", "revise"] }

[[step.review]]
source = "diff"
label = "Code changes"

[[step]]
id = "merge"
type = "command"
depends_on = ["approve"]
when = "approve == 'approve'"
run = "git merge --no-ff jig/bugfix/fix"
"#;

#[test]
fn builds_typed_steps_and_applies_defaults() {
    let workflow = decode(WORKFLOW, None).unwrap();

    assert_eq!(workflow.name().as_str(), "bugfix");
    assert_eq!(workflow.steps().len(), 4);
    assert_eq!(workflow.defaults().max_parallel().get(), 4);

    let triage = agent(&workflow, "triage");
    assert_eq!(triage.backend(), AgentBackend::Claude(ClaudeTransport::Sdk));
    assert_eq!(triage.isolation(), Isolation::None);
    assert!(matches!(triage.common().inputs()[0], Input::Path { .. }));

    let fix = agent(&workflow, "fix");
    assert_eq!(fix.isolation(), Isolation::Worktree);
    assert!(matches!(fix.common().inputs()[0], Input::Step { .. }));

    let merge = workflow.step(&StepId::new("merge").unwrap()).unwrap();
    let Step::Command(merge) = merge else {
        panic!("merge should be a command step")
    };
    assert!(matches!(merge.source(), CommandSource::Inline(_)));
}

#[test]
fn step_backend_overrides_are_typed() {
    let source = r#"
[workflow]
name = "backends"
version = "1"

[[step]]
id = "claude"
type = "agent"
skill = "skills/a"
transport = "acp"

[[step]]
id = "cursor"
type = "agent"
skill = "skills/a"
backend = "cursor"

[[step]]
id = "codex"
type = "agent"
skill = "skills/a"
backend = "codex"
"#;
    let workflow = decode(source, None).unwrap();

    assert_eq!(
        agent(&workflow, "claude").backend(),
        AgentBackend::Claude(ClaudeTransport::Acp)
    );
    assert_eq!(agent(&workflow, "cursor").backend(), AgentBackend::Cursor);
    assert_eq!(agent(&workflow, "codex").backend(), AgentBackend::Codex);
}

fn agent<'a>(workflow: &'a jig_workflow::Workflow, id: &str) -> &'a jig_workflow::AgentStep {
    match workflow.step(&StepId::new(id).unwrap()).unwrap() {
        Step::Agent(step) => step,
        _ => panic!("{id} should be an agent step"),
    }
}
