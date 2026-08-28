use std::num::NonZeroUsize;

use super::{BuildContext, BuildError, input, nonblank, step_context};
use crate::workflow::raw::{RawLoop, RawStep, RawValidation};
use crate::workflow::{
    CommandSource, CommandStep, Condition, FailureBehavior, LoopConfig, OutputPath, SchemaPath,
    ScriptPath, StepCommon, StepId, StepValidation,
};

pub(crate) fn convert_common(
    raw: &mut RawStep,
    allow_user_input: bool,
    build: &BuildContext<'_>,
) -> Result<StepCommon, BuildError> {
    let context = step_context(&raw.id);
    let id =
        StepId::try_from(std::mem::take(&mut raw.id)).map_err(|source| BuildError::StepId {
            context: context.clone(),
            source,
        })?;
    let depends_on = std::mem::take(&mut raw.depends_on)
        .into_iter()
        .map(|dependency| {
            StepId::try_from(dependency).map_err(|source| BuildError::StepId {
                context: context.clone(),
                source,
            })
        })
        .collect::<Result<Vec<_>, _>>()?;
    let when = parse_condition(&raw.when, &context, "when")?;
    let failure = failure(&raw.on_failure, raw.max_retries, &context)?;
    let inputs = input::convert(
        std::mem::take(&mut raw.inputs),
        id.as_str(),
        allow_user_input,
    )?;
    let validation = raw
        .validate
        .take()
        .map(|value| convert_validation(value, &context, build))
        .transpose()?;
    let loop_config = raw
        .loop_config
        .take()
        .map(|value| convert_loop(value, &context))
        .transpose()?;
    Ok(StepCommon {
        id,
        depends_on,
        when,
        output: nonblank(std::mem::take(&mut raw.output)).map(OutputPath::new),
        failure,
        inputs,
        validation,
        loop_config,
    })
}

pub(crate) fn convert_command(
    mut raw: RawStep,
    build: &BuildContext<'_>,
) -> Result<CommandStep, BuildError> {
    let context = step_context(&raw.id);
    reject_agent_fields(&raw, &context)?;
    if !raw.review.is_empty() {
        return Err(BuildError::Invalid {
            context,
            message: "review targets are only valid on review steps".into(),
        });
    }
    let source = match (nonblank(raw.run.clone()), nonblank(raw.script.clone())) {
        (Some(_), Some(_)) => {
            return Err(BuildError::Conflict {
                context,
                left: "run",
                right: "script",
            });
        }
        (Some(run), None) => CommandSource::Inline(run),
        (None, Some(script)) => CommandSource::Script(ScriptPath::new(script)),
        (None, None) => {
            return Err(BuildError::Missing {
                context,
                field: "run or script",
            });
        }
    };
    if let CommandSource::Script(path) = &source
        && let Some(base_dir) = build.base_dir
    {
        let root = crate::workflow::repo_root(base_dir).unwrap_or_else(|| base_dir.to_owned());
        let resolved = crate::workflow::script_path(root, path.as_path());
        if !resolved.is_file() {
            return Err(BuildError::MissingResource {
                context: step_context(&raw.id),
                path: resolved,
            });
        }
    }
    let output = super::output::scalar_contract(raw.output_type.take(), &context)?;
    let common = convert_common(&mut raw, false, build)?;
    Ok(CommandStep {
        common,
        source,
        output,
    })
}

pub(crate) fn reject_agent_fields(raw: &RawStep, context: &str) -> Result<(), BuildError> {
    let has_agent_fields = !raw.skill.is_empty()
        || !raw.agent_file.is_empty()
        || !raw.profile.is_empty()
        || !raw.allowed_tools.is_empty()
        || !raw.disallowed_tools.is_empty()
        || raw.inject_context.is_some()
        || raw.context.is_some()
        || !raw.append_system_prompt.is_empty()
        || !raw.block_on.is_empty()
        || raw.schema.is_some()
        || !raw.schema_file.is_empty();
    if has_agent_fields {
        Err(BuildError::Invalid {
            context: context.into(),
            message: "agent-only fields are set on a non-agent step".into(),
        })
    } else {
        Ok(())
    }
}

fn failure(value: &str, retries: i64, context: &str) -> Result<FailureBehavior, BuildError> {
    match value {
        "" | "abort" if retries == 0 => Ok(FailureBehavior::Abort),
        "continue" if retries == 0 => Ok(FailureBehavior::Continue),
        "retry" => {
            let retries = if retries == 0 { 1 } else { retries };
            let retries = usize::try_from(retries)
                .ok()
                .and_then(NonZeroUsize::new)
                .ok_or_else(|| BuildError::Invalid {
                    context: context.into(),
                    message: "max_retries must be positive for retry behavior".into(),
                })?;
            Ok(FailureBehavior::Retry {
                max_retries: retries,
            })
        }
        "abort" | "continue" => Err(BuildError::Invalid {
            context: context.into(),
            message: "max_retries is only valid when on_failure = \"retry\"".into(),
        }),
        other => Err(BuildError::InvalidValue {
            context: context.into(),
            field: "on_failure",
            value: other.into(),
            expected: "abort, retry, or continue",
        }),
    }
}

fn convert_validation(
    raw: RawValidation,
    context: &str,
    build: &BuildContext<'_>,
) -> Result<StepValidation, BuildError> {
    let validation = StepValidation {
        command: nonblank(raw.command),
        output_schema: nonblank(raw.output_schema).map(SchemaPath::new),
        output_exists: raw.output_exists,
        output_contains: nonblank(raw.output_contains),
    };
    if validation.command.is_none() && !validation.requires_output() {
        return Err(BuildError::Invalid {
            context: context.into(),
            message: "[step.validate] has no checks".into(),
        });
    }
    if let (Some(base_dir), Some(schema)) = (build.base_dir, validation.output_schema()) {
        let resolved = base_dir.join(schema.as_path());
        if !resolved.is_file() {
            return Err(BuildError::MissingResource {
                context: context.into(),
                path: resolved,
            });
        }
    }
    Ok(validation)
}

fn convert_loop(raw: RawLoop, context: &str) -> Result<LoopConfig, BuildError> {
    let condition =
        parse_condition(&raw.when, context, "loop.when")?.ok_or_else(|| BuildError::Missing {
            context: context.into(),
            field: "loop.when",
        })?;
    let goto = StepId::try_from(raw.goto).map_err(|source| BuildError::StepId {
        context: context.into(),
        source,
    })?;
    let max_iterations = usize::try_from(raw.max_iterations)
        .ok()
        .and_then(NonZeroUsize::new)
        .ok_or_else(|| BuildError::Invalid {
            context: context.into(),
            message: "loop.max_iterations must be positive".into(),
        })?;
    let feedback = nonblank(raw.feedback)
        .map(|value| {
            let reference = value.strip_prefix('@').ok_or_else(|| BuildError::Invalid {
                context: context.into(),
                message: "loop.feedback must start with '@'".into(),
            })?;
            StepId::try_from(reference).map_err(|source| BuildError::StepId {
                context: context.into(),
                source,
            })
        })
        .transpose()?;
    Ok(LoopConfig {
        condition,
        goto,
        max_iterations,
        feedback,
    })
}

pub(crate) fn parse_condition(
    value: &str,
    context: &str,
    field: &str,
) -> Result<Option<Condition>, BuildError> {
    if value.trim().is_empty() {
        return Ok(None);
    }
    value
        .parse()
        .map(Some)
        .map_err(|source| BuildError::Condition {
            context: format!("{context} {field}"),
            source,
        })
}
