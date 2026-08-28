use std::collections::{HashMap, HashSet};

use super::ValidationProblem;
use crate::workflow::{Step, StepId, Workflow};

pub(crate) fn index(steps: &[Step]) -> HashMap<StepId, usize> {
    steps
        .iter()
        .enumerate()
        .map(|(index, step)| (step.id().clone(), index))
        .collect()
}

pub(crate) fn validate(workflow: &Workflow) -> Vec<ValidationProblem> {
    let mut problems = Vec::new();
    if workflow.steps().is_empty() {
        problems.push(ValidationProblem::EmptyWorkflow);
        return problems;
    }

    let mut seen = HashSet::new();
    for step in workflow.steps() {
        if !seen.insert(step.id().clone()) {
            problems.push(ValidationProblem::DuplicateStep(step.id().clone()));
        }
    }

    let index = index(workflow.steps());
    for step in workflow.steps() {
        for dependency in step.common().dependencies() {
            if dependency == step.id() {
                problems.push(ValidationProblem::SelfDependency {
                    step: step.id().clone(),
                });
            } else if !index.contains_key(dependency) {
                problems.push(ValidationProblem::UnknownDependency {
                    step: step.id().clone(),
                    dependency: dependency.clone(),
                });
            }
        }
    }
    if let Some((from, to)) = find_cycle(workflow.steps(), &index) {
        problems.push(ValidationProblem::DependencyCycle { from, to });
    }
    problems
}

fn find_cycle(steps: &[Step], index: &HashMap<StepId, usize>) -> Option<(StepId, StepId)> {
    fn visit(
        current: usize,
        steps: &[Step],
        index: &HashMap<StepId, usize>,
        state: &mut [Visit],
    ) -> Option<(StepId, StepId)> {
        state[current] = Visit::Active;
        for dependency in steps[current].common().dependencies() {
            let Some(&next) = index.get(dependency) else {
                continue;
            };
            if state[next] == Visit::Active {
                return Some((steps[current].id().clone(), steps[next].id().clone()));
            }
            if state[next] == Visit::New
                && let Some(cycle) = visit(next, steps, index, state)
            {
                return Some(cycle);
            }
        }
        state[current] = Visit::Done;
        None
    }

    let mut state = vec![Visit::New; steps.len()];
    for current in 0..steps.len() {
        if state[current] == Visit::New
            && let Some(cycle) = visit(current, steps, index, &mut state)
        {
            return Some(cycle);
        }
    }
    None
}

#[derive(Clone, Copy, Eq, PartialEq)]
enum Visit {
    New,
    Active,
    Done,
}
