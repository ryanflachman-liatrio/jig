use serde::Deserialize;

use super::RawStep;

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawMeta {
    pub name: String,
    pub version: String,
    pub description: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawSecurity {
    pub enabled: Option<bool>,
    pub tier1_enabled: Option<bool>,
    pub tier2_enabled: Option<bool>,
    pub outbound_allowlist: Vec<String>,
    pub fleet_budget_usd: f64,
    pub concurrency_cap: i64,
    pub batch_size: i64,
    pub debounce_ms: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawDefaults {
    pub model: String,
    pub fallback_model: String,
    pub effort: String,
    pub max_turns: i64,
    pub max_thinking_tokens: i64,
    pub max_budget_usd: f64,
    pub cwd: String,
    pub permission_mode: String,
    pub max_parallel: i64,
    pub artifacts_dir: String,
    pub inject_context: Option<bool>,
    pub backend: String,
    pub transport: String,
    pub security: RawSecurity,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawWorkflow {
    #[serde(rename = "workflow")]
    pub meta: RawMeta,
    pub defaults: RawDefaults,
    #[serde(rename = "step")]
    pub steps: Vec<RawStep>,
}
