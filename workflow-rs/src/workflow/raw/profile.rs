use serde::Deserialize;

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawProfile {
    pub id: String,
    pub tools: Vec<String>,
    pub disallowed_tools: Vec<String>,
    pub model: String,
    pub fallback_model: String,
    pub effort: String,
    pub max_turns: i64,
    pub max_thinking_tokens: i64,
    pub max_budget_usd: f64,
    pub permission_mode: String,
    pub ask_user_question: bool,
    pub append_system_prompt: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawProfileFile {
    #[serde(rename = "agent")]
    pub agents: Vec<RawProfile>,
}
