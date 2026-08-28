use super::ValidationProblem;
use crate::workflow::{FieldKind, Input, ReviewSource, Step, StepId, Workflow};

pub(crate) fn validate(workflow: &Workflow, problems: &mut Vec<ValidationProblem>) {
    for step in workflow.steps() {
        validate_common(workflow, step, problems);
        match step {
            Step::Agent(agent) => {
                if let Some(condition) = &agent.block_on {
                    if condition.step() != step.id() {
                        problems.push(ValidationProblem::NotSelfReference {
                            step: step.id().clone(),
                            relation: "block_on",
                        });
                    }
                    super::condition::validate(
                        workflow,
                        step.id(),
                        "block_on",
                        condition,
                        problems,
                    );
                }
            }
            Step::Review(review) => validate_review(workflow, review, problems),
            Step::Command(_) => {}
        }
    }
}

fn validate_common(workflow: &Workflow, step: &Step, problems: &mut Vec<ValidationProblem>) {
    let common = step.common();
    if let Some(condition) = common.when() {
        require_dependency(step, condition.step(), "when", problems);
        super::condition::validate(workflow, step.id(), "when", condition, problems);
    }
    for input in common.inputs() {
        if let Input::Step {
            step: target,
            field,
            ..
        } = input
        {
            require_known(workflow, step.id(), target, "input", problems);
            require_dependency(step, target, "input", problems);
            if !field.is_empty() {
                validate_field(
                    workflow,
                    step.id(),
                    target,
                    "input",
                    field.segments(),
                    problems,
                );
            }
        }
    }
    if let Some(validation) = common.validation()
        && validation.requires_output()
        && common.output().is_none()
    {
        problems.push(ValidationProblem::MissingOutput {
            step: step.id().clone(),
        });
    }
    if let Some(loop_config) = common.loop_config() {
        require_known(
            workflow,
            step.id(),
            loop_config.goto(),
            "loop.goto",
            problems,
        );
        let condition = loop_config.condition();
        if condition.step() != step.id() {
            require_dependency(step, condition.step(), "loop.when", problems);
        }
        super::condition::validate(workflow, step.id(), "loop.when", condition, problems);
        if let Some(feedback) = loop_config.feedback() {
            require_known(workflow, step.id(), feedback, "loop.feedback", problems);
        }
    }
}

fn validate_review(
    workflow: &Workflow,
    review: &crate::workflow::ReviewStep,
    problems: &mut Vec<ValidationProblem>,
) {
    for target in review.targets() {
        if let ReviewSource::Step {
            step,
            field: field_path,
        } = target.source()
        {
            require_known(
                workflow,
                review.common().id(),
                step,
                "review target",
                problems,
            );
            require_dependency_for_common(review.common(), step, "review target", problems);
            if !field_path.is_empty()
                && let Some(field) = super::condition::field(workflow, step, field_path.segments())
                && !matches!(field.kind(), FieldKind::Text)
            {
                problems.push(ValidationProblem::ReviewFieldNotText {
                    step: review.common().id().clone(),
                });
            }
        }
    }
}

fn validate_field(
    workflow: &Workflow,
    owner: &StepId,
    target: &StepId,
    relation: &'static str,
    path: &[String],
    problems: &mut Vec<ValidationProblem>,
) {
    if super::condition::field(workflow, target, path).is_none() {
        problems.push(ValidationProblem::MissingField {
            step: owner.clone(),
            relation,
            target: target.clone(),
            field: path.join("."),
        });
    }
}

fn require_known(
    workflow: &Workflow,
    owner: &StepId,
    target: &StepId,
    relation: &'static str,
    problems: &mut Vec<ValidationProblem>,
) {
    if workflow.step(target).is_none() {
        problems.push(ValidationProblem::UnknownReference {
            step: owner.clone(),
            relation,
            target: target.clone(),
        });
    }
}

fn require_dependency(
    owner: &Step,
    target: &StepId,
    relation: &'static str,
    problems: &mut Vec<ValidationProblem>,
) {
    require_dependency_for_common(owner.common(), target, relation, problems);
}

fn require_dependency_for_common(
    owner: &crate::workflow::StepCommon,
    target: &StepId,
    relation: &'static str,
    problems: &mut Vec<ValidationProblem>,
) {
    if !owner.dependencies().contains(target) {
        problems.push(ValidationProblem::MissingDependency {
            step: owner.id().clone(),
            relation,
            target: target.clone(),
        });
    }
}
