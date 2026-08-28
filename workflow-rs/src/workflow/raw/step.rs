use serde::Deserialize;

use super::RawInput;

#[derive(Clone, Debug, Deserialize)]
#[serde(untagged)]
pub(crate) enum RawOutputType {
    Kind(String),
    Enum {
        #[serde(rename = "enum")]
        values: Vec<String>,
    },
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawContext {
    pub purpose: String,
    pub notes: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawValidation {
    pub command: String,
    pub output_schema: String,
    pub output_exists: bool,
    pub output_contains: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawLoop {
    pub when: String,
    pub goto: String,
    pub max_iterations: i64,
    pub feedback: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawReviewTarget {
    pub source: String,
    pub label: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawStepSecurity {
    pub enabled: Option<bool>,
    pub tier1_enabled: Option<bool>,
    pub tier2_enabled: Option<bool>,
    pub outbound_allowlist: Vec<String>,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawStep {
    pub id: String,
    #[serde(rename = "type")]
    pub step_type: String,
    pub depends_on: Vec<String>,
    pub when: String,
    pub output: String,
    pub output_type: Option<RawOutputType>,
    pub on_failure: String,
    pub max_retries: i64,

    pub skill: String,
    pub agent_file: String,
    pub profile: String,
    pub inputs: Vec<RawInput>,
    pub isolation: String,
    pub inject_context: Option<bool>,
    pub context: Option<RawContext>,
    pub allowed_tools: Vec<String>,
    pub disallowed_tools: Vec<String>,

    pub model: String,
    pub fallback_model: String,
    pub effort: String,
    pub max_turns: i64,
    pub max_thinking_tokens: i64,
    pub max_budget_usd: f64,
    pub permission_mode: String,
    pub backend: String,
    pub transport: String,

    pub output_template: String,
    pub append_system_prompt: String,
    pub schema: Option<toml::Table>,
    pub schema_file: String,
    pub run: String,
    pub script: String,
    pub review: Vec<RawReviewTarget>,
    pub block_on: String,
    pub validate: Option<RawValidation>,
    #[serde(rename = "loop")]
    pub loop_config: Option<RawLoop>,
    pub security: RawStepSecurity,
}
