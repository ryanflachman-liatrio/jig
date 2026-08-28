#[derive(Clone, Debug, Default, PartialEq)]
pub struct SecurityConfig {
    pub(crate) enabled: Option<bool>,
    pub(crate) tier1_enabled: Option<bool>,
    pub(crate) tier2_enabled: Option<bool>,
    pub(crate) outbound_allowlist: Vec<String>,
    pub(crate) fleet_budget_usd: f64,
    pub(crate) concurrency_cap: Option<usize>,
    pub(crate) batch_size: Option<usize>,
    pub(crate) debounce_ms: Option<u64>,
}

impl SecurityConfig {
    pub fn enabled(&self) -> Option<bool> {
        self.enabled
    }
    pub fn tier1_enabled(&self) -> Option<bool> {
        self.tier1_enabled
    }
    pub fn tier2_enabled(&self) -> Option<bool> {
        self.tier2_enabled
    }
    pub fn outbound_allowlist(&self) -> &[String] {
        &self.outbound_allowlist
    }
    pub fn fleet_budget_usd(&self) -> f64 {
        self.fleet_budget_usd
    }
    pub fn concurrency_cap(&self) -> Option<usize> {
        self.concurrency_cap
    }
    pub fn batch_size(&self) -> Option<usize> {
        self.batch_size
    }
    pub fn debounce_ms(&self) -> Option<u64> {
        self.debounce_ms
    }
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct SecurityOverride {
    pub(crate) enabled: Option<bool>,
    pub(crate) tier1_enabled: Option<bool>,
    pub(crate) tier2_enabled: Option<bool>,
    pub(crate) outbound_allowlist: Vec<String>,
}

impl SecurityOverride {
    pub fn enabled(&self) -> Option<bool> {
        self.enabled
    }
    pub fn tier1_enabled(&self) -> Option<bool> {
        self.tier1_enabled
    }
    pub fn tier2_enabled(&self) -> Option<bool> {
        self.tier2_enabled
    }
    pub fn outbound_allowlist(&self) -> &[String] {
        &self.outbound_allowlist
    }
}
