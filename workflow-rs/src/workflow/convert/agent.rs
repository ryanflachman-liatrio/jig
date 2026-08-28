use std::fs;

use super::{
    BuildContext, BuildError, ModelFields, backend, model_config, nonblank, step_context,
    validate_hosts,
};
use crate::workflow::agent_file::{AgentFile, parse_agent_file, parse_skill_file};
use crate::workflow::raw::RawStep;
use crate::workflow::{
    AgentContext, AgentFilePath, AgentInstructions, AgentProfile, AgentStep, InstructionBody,
    Isolation, ModelConfig, OutputTemplate, ProfileId, SecurityOverride, SkillPath, ToolPolicy,
};

pub(crate) fn convert(mut raw: RawStep, build: &BuildContext<'_>) -> Result<AgentStep, BuildError> {
    let context = step_context(&raw.id);
    if !raw.run.is_empty() || !raw.script.is_empty() || !raw.review.is_empty() {
        return Err(BuildError::Invalid {
            context,
            message: "run, script, and review targets are not valid on agent steps".into(),
        });
    }
    if raw.context.is_some() && raw.inject_context == Some(false) {
        return Err(BuildError::Invalid {
            context,
            message: "[step.context] cannot be combined with inject_context = false".into(),
        });
    }

    let (instructions, file) = instructions(&raw, build)?;
    let profile = profile(&raw, build)?;
    if profile.is_some_and(|profile| profile.ask_user_question) && !raw.block_on.is_empty() {
        return Err(BuildError::Invalid {
            context: step_context(&raw.id),
            message: "block_on and an interactive profile serve overlapping purposes".into(),
        });
    }

    let tools = tools(&raw, file.as_ref(), profile);
    let isolation = isolation(&raw.isolation, &tools, &raw.id)?;
    let model = effective_model(&raw, file.as_ref(), profile, build)?;
    let backend = backend(
        &step_context(&raw.id),
        &raw.backend,
        &raw.transport,
        build.defaults.backend,
    )?;
    let output = super::output::agent_contract(
        raw.output_type.take(),
        raw.schema.take(),
        std::mem::take(&mut raw.schema_file),
        &step_context(&raw.id),
        build,
    )?;
    let output_template = output_template(&raw, build)?;
    let block_on =
        super::common::parse_condition(&raw.block_on, &step_context(&raw.id), "block_on")?;
    let security = security(&raw)?;
    let inject_context = raw.inject_context.unwrap_or(build.defaults.inject_context);
    let agent_context = raw.context.take().map(|context| AgentContext {
        purpose: nonblank(context.purpose),
        notes: nonblank(context.notes),
    });
    let append_system_prompt = nonblank(std::mem::take(&mut raw.append_system_prompt))
        .or_else(|| profile.and_then(|profile| profile.append_system_prompt.clone()));
    let common = super::common::convert_common(&mut raw, true, build)?;

    Ok(AgentStep {
        common,
        instructions,
        profile: profile.map(|profile| profile.id.clone()),
        isolation,
        inject_context,
        context: agent_context,
        tools,
        model,
        backend,
        output,
        output_template,
        append_system_prompt,
        block_on,
        security,
    })
}

fn instructions(
    raw: &RawStep,
    build: &BuildContext<'_>,
) -> Result<(AgentInstructions, Option<AgentFile>), BuildError> {
    let context = step_context(&raw.id);
    match (
        nonblank(raw.skill.clone()),
        nonblank(raw.agent_file.clone()),
    ) {
        (Some(_), Some(_)) => Err(BuildError::Conflict {
            context,
            left: "skill",
            right: "agent_file",
        }),
        (None, None) => Err(BuildError::Missing {
            context,
            field: "skill or agent_file",
        }),
        (Some(path), None) => {
            let path = SkillPath::new(path);
            let file = resolve_file(
                build,
                path.as_path().join("SKILL.md"),
                &context,
                parse_skill_file,
            )?;
            let body = file.as_ref().map_or(InstructionBody::Unresolved, |file| {
                InstructionBody::Resolved(file.prompt.clone())
            });
            Ok((AgentInstructions::Skill { path, body }, file))
        }
        (None, Some(path)) => {
            let path = AgentFilePath::new(path);
            let file = resolve_file(build, path.as_path().to_owned(), &context, parse_agent_file)?;
            let body = file.as_ref().map_or(InstructionBody::Unresolved, |file| {
                InstructionBody::Resolved(file.prompt.clone())
            });
            Ok((AgentInstructions::AgentFile { path, body }, file))
        }
    }
}

fn resolve_file(
    build: &BuildContext<'_>,
    authored: std::path::PathBuf,
    context: &str,
    parser: fn(&[u8]) -> Result<AgentFile, crate::workflow::agent_file::AgentFileError>,
) -> Result<Option<AgentFile>, BuildError> {
    let Some(base_dir) = build.base_dir else {
        return Ok(None);
    };
    let resolved = base_dir.join(authored);
    let data = fs::read(&resolved).map_err(|_| BuildError::MissingResource {
        context: context.into(),
        path: resolved,
    })?;
    parser(&data)
        .map(Some)
        .map_err(|error| BuildError::Invalid {
            context: context.into(),
            message: error.to_string(),
        })
}

fn profile<'a>(
    raw: &RawStep,
    build: &'a BuildContext<'_>,
) -> Result<Option<&'a AgentProfile>, BuildError> {
    let Some(id) = nonblank(raw.profile.clone()) else {
        return Ok(None);
    };
    let id = ProfileId::try_from(id).map_err(|error| BuildError::Invalid {
        context: step_context(&raw.id),
        message: error.to_string(),
    })?;
    build
        .profiles
        .get(&id)
        .map(Some)
        .ok_or_else(|| BuildError::Invalid {
            context: step_context(&raw.id),
            message: format!("unknown profile {id:?}"),
        })
}

fn tools(raw: &RawStep, file: Option<&AgentFile>, profile: Option<&AgentProfile>) -> ToolPolicy {
    let mut allowed = if !raw.allowed_tools.is_empty() {
        raw.allowed_tools.clone()
    } else if let Some(file) = file.filter(|file| !file.tools.is_empty()) {
        file.tools.clone()
    } else {
        profile
            .map(|profile| profile.tools.allowed.clone())
            .unwrap_or_default()
    };
    if profile.is_some_and(|profile| profile.ask_user_question)
        && !allowed.iter().any(|tool| tool == "AskUserQuestion")
    {
        allowed.push("AskUserQuestion".into());
    }
    let disallowed = if raw.disallowed_tools.is_empty() {
        profile
            .map(|profile| profile.tools.disallowed.clone())
            .unwrap_or_default()
    } else {
        raw.disallowed_tools.clone()
    };
    ToolPolicy {
        allowed,
        disallowed,
    }
}

fn effective_model(
    raw: &RawStep,
    file: Option<&AgentFile>,
    profile: Option<&AgentProfile>,
    build: &BuildContext<'_>,
) -> Result<ModelConfig, BuildError> {
    let inherited = profile
        .map(|profile| &profile.model)
        .unwrap_or(&build.defaults.model);
    let model = nonblank(raw.model.clone())
        .or_else(|| file.and_then(|file| file.model.clone()))
        .or_else(|| inherited.model.clone());
    let fallback =
        nonblank(raw.fallback_model.clone()).or_else(|| inherited.fallback_model.clone());
    let mut resolved = model_config(
        &step_context(&raw.id),
        ModelFields {
            model: model.unwrap_or_default(),
            fallback_model: fallback.unwrap_or_default(),
            effort: raw.effort.clone(),
            max_turns: raw.max_turns,
            max_thinking_tokens: raw.max_thinking_tokens,
            max_budget_usd: raw.max_budget_usd,
            permission_mode: raw.permission_mode.clone(),
        },
    )?;
    if resolved.effort.is_none() {
        resolved.effort = inherited.effort;
    }
    if resolved.max_turns.is_none() {
        resolved.max_turns = inherited.max_turns;
    }
    if resolved.max_thinking_tokens.is_none() {
        resolved.max_thinking_tokens = inherited.max_thinking_tokens;
    }
    if resolved.max_budget.is_none() {
        resolved.max_budget = inherited.max_budget;
    }
    if resolved.permission_mode.is_none() {
        resolved.permission_mode = inherited.permission_mode;
    }
    Ok(resolved)
}

fn isolation(value: &str, tools: &ToolPolicy, id: &str) -> Result<Isolation, BuildError> {
    match value {
        "worktree" => Ok(Isolation::Worktree),
        "none" => Ok(Isolation::None),
        "" if tools.is_mutating() => Ok(Isolation::Worktree),
        "" => Ok(Isolation::None),
        value => Err(BuildError::InvalidValue {
            context: step_context(id),
            field: "isolation",
            value: value.into(),
            expected: "worktree or none",
        }),
    }
}

fn output_template(
    raw: &RawStep,
    build: &BuildContext<'_>,
) -> Result<Option<OutputTemplate>, BuildError> {
    let Some(path) = nonblank(raw.output_template.clone()).map(std::path::PathBuf::from) else {
        return Ok(None);
    };
    let body = if let Some(base_dir) = build.base_dir {
        let resolved = base_dir.join(&path);
        let text = fs::read_to_string(&resolved).map_err(|_| BuildError::MissingResource {
            context: step_context(&raw.id),
            path: resolved,
        })?;
        InstructionBody::Resolved(text.trim().to_owned())
    } else {
        InstructionBody::Unresolved
    };
    Ok(Some(OutputTemplate { path, body }))
}

fn security(raw: &RawStep) -> Result<SecurityOverride, BuildError> {
    let context = format!("{} [step.security]", step_context(&raw.id));
    validate_hosts(&raw.security.outbound_allowlist, &context)?;
    Ok(SecurityOverride {
        enabled: raw.security.enabled,
        tier1_enabled: raw.security.tier1_enabled,
        tier2_enabled: raw.security.tier2_enabled,
        outbound_allowlist: raw.security.outbound_allowlist.clone(),
    })
}
