use super::{BuildError, ModelFields, model_config, nonblank};
use crate::workflow::raw::RawProfile;
use crate::workflow::{AgentProfile, ProfileId, ToolPolicy};

pub(crate) fn convert(raw: RawProfile, source: &str) -> Result<AgentProfile, BuildError> {
    let context = format!("profile {source}");
    let id = ProfileId::try_from(raw.id).map_err(|error| BuildError::Invalid {
        context: context.clone(),
        message: error.to_string(),
    })?;
    let model = model_config(
        &context,
        ModelFields {
            model: raw.model,
            fallback_model: raw.fallback_model,
            effort: raw.effort,
            max_turns: raw.max_turns,
            max_thinking_tokens: raw.max_thinking_tokens,
            max_budget_usd: raw.max_budget_usd,
            permission_mode: raw.permission_mode,
        },
    )?;
    Ok(AgentProfile {
        id,
        tools: ToolPolicy {
            allowed: raw.tools,
            disallowed: raw.disallowed_tools,
        },
        model,
        ask_user_question: raw.ask_user_question,
        append_system_prompt: nonblank(raw.append_system_prompt),
    })
}
