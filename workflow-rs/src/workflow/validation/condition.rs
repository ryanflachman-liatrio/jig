use super::ValidationProblem;
use crate::workflow::{
    Comparison, Condition, Field, FieldKind, OutputContract, ReviewVerdict, SchemaSource, Step,
    StepId, Workflow, merged_schema,
};

pub(crate) fn validate(
    workflow: &Workflow,
    owner: &StepId,
    relation: &'static str,
    condition: &Condition,
    problems: &mut Vec<ValidationProblem>,
) {
    let Some(target) = workflow.step(condition.step()) else {
        problems.push(ValidationProblem::UnknownReference {
            step: owner.clone(),
            relation,
            target: condition.step().clone(),
        });
        return;
    };
    if !condition.field().is_empty() {
        let Some(field) = field(workflow, target.id(), condition.field().segments()) else {
            problems.push(ValidationProblem::MissingField {
                step: owner.clone(),
                relation,
                target: target.id().clone(),
                field: condition.field().to_string(),
            });
            return;
        };
        validate_field_comparison(owner, relation, condition, &field, problems);
        return;
    }
    validate_scalar_comparison(owner, relation, condition, target, problems);
}

pub(crate) fn field(workflow: &Workflow, target: &StepId, path: &[String]) -> Option<Field> {
    let step = workflow.step(target)?;
    match step {
        Step::Agent(agent) => match agent.output_contract() {
            OutputContract::Schema(SchemaSource::File { schema: None, .. }) => merged_schema(None)
                .lookup(path)
                .cloned()
                .or_else(|| Some(Field::new(path.join("."), FieldKind::Any))),
            OutputContract::Schema(source) => merged_schema(source.schema()).lookup(path).cloned(),
            _ => merged_schema(None).lookup(path).cloned(),
        },
        Step::Command(_) | Step::Review(_) => None,
    }
}

fn validate_scalar_comparison(
    owner: &StepId,
    relation: &'static str,
    condition: &Condition,
    target: &Step,
    problems: &mut Vec<ValidationProblem>,
) {
    let contract = match target {
        Step::Agent(step) => step.output_contract(),
        Step::Command(step) => step.output_contract(),
        Step::Review(step) => match step.verdict() {
            ReviewVerdict::Bool => {
                if condition.comparison() != Comparison::Truthy
                    && !matches!(condition.value(), Some("true" | "false"))
                {
                    invalid(
                        owner,
                        relation,
                        "review bool verdict accepts only true or false",
                        problems,
                    );
                }
                return;
            }
            ReviewVerdict::Enum(values) => {
                validate_enum(owner, relation, condition, values, problems);
                return;
            }
        },
    };
    match contract {
        OutputContract::Bool => {
            if condition.comparison() != Comparison::Truthy
                && !matches!(condition.value(), Some("true" | "false"))
            {
                invalid(
                    owner,
                    relation,
                    "bool output accepts only true or false",
                    problems,
                );
            }
        }
        OutputContract::Enum(values) => validate_enum(owner, relation, condition, values, problems),
        OutputContract::Text | OutputContract::Schema(_) => {
            invalid(
                owner,
                relation,
                "target has no scalar typed verdict",
                problems,
            );
        }
    }
}

fn validate_enum(
    owner: &StepId,
    relation: &'static str,
    condition: &Condition,
    values: &crate::workflow::EnumValues,
    problems: &mut Vec<ValidationProblem>,
) {
    match (condition.comparison(), condition.value()) {
        (Comparison::Truthy, _) => invalid(
            owner,
            relation,
            "a bare condition requires a bool output",
            problems,
        ),
        (_, Some(value)) if values.contains(value) => {}
        _ => invalid(
            owner,
            relation,
            "comparison value is not in the output enum",
            problems,
        ),
    }
}

fn validate_field_comparison(
    owner: &StepId,
    relation: &'static str,
    condition: &Condition,
    field: &Field,
    problems: &mut Vec<ValidationProblem>,
) {
    match (condition.comparison(), field.kind()) {
        (Comparison::Truthy, FieldKind::Bool) => {}
        (Comparison::Truthy, _) => invalid(
            owner,
            relation,
            "a bare field condition requires bool",
            problems,
        ),
        (_, FieldKind::Enum(values))
            if condition
                .value()
                .is_some_and(|value| values.contains(value)) => {}
        (_, FieldKind::Enum(_)) => invalid(
            owner,
            relation,
            "comparison value is not in the field enum",
            problems,
        ),
        (_, FieldKind::Bool) if matches!(condition.value(), Some("true" | "false")) => {}
        (_, FieldKind::Bool) => invalid(
            owner,
            relation,
            "bool field accepts only true or false",
            problems,
        ),
        (_, FieldKind::List(_) | FieldKind::Object(_)) => invalid(
            owner,
            relation,
            "list and object fields cannot be compared",
            problems,
        ),
        _ => {}
    }
}

fn invalid(
    step: &StepId,
    relation: &'static str,
    message: &str,
    problems: &mut Vec<ValidationProblem>,
) {
    problems.push(ValidationProblem::InvalidComparison {
        step: step.clone(),
        relation,
        message: message.into(),
    });
}
