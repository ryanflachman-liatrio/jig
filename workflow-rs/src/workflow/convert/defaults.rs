use std::num::NonZeroUsize;
use std::path::PathBuf;

use super::{BuildError, nonblank};
use crate::workflow::raw::{RawDefaults, RawSecurity};
use crate::workflow::{
    AgentBackend, BudgetUsd, ClaudeTransport, EffortLevel, ModelConfig, PermissionMode,
    SecurityConfig, WorkflowDefaults,
};

pub(crate) struct ModelFields {
    pub model: String,
    pub fallback_model: String,
    pub effort: String,
    pub max_turns: i64,
    pub max_thinking_tokens: i64,
    pub max_budget_usd: f64,
    pub permission_mode: String,
}

pub(crate) fn convert_defaults(raw: RawDefaults) -> Result<WorkflowDefaults, BuildError> {
    let context = "[defaults]";
    let max_parallel = positive_or_default(raw.max_parallel, 4, context, "max_parallel")?;
    let model = model_config(
        context,
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
    let backend = backend(
        context,
        &raw.backend,
        &raw.transport,
        AgentBackend::default(),
    )?;
    Ok(WorkflowDefaults {
        model,
        backend,
        cwd: nonblank(raw.cwd).map(PathBuf::from),
        max_parallel,
        artifacts_dir: PathBuf::from(
            nonblank(raw.artifacts_dir).unwrap_or_else(|| ".jig/artifacts".into()),
        ),
        inject_context: raw.inject_context.unwrap_or(true),
        security: security_config(raw.security, context)?,
    })
}

pub(crate) fn model_config(context: &str, fields: ModelFields) -> Result<ModelConfig, BuildError> {
    Ok(ModelConfig {
        model: nonblank(fields.model),
        fallback_model: nonblank(fields.fallback_model),
        effort: optional_effort(&fields.effort, context)?,
        max_turns: optional_positive(fields.max_turns, context, "max_turns")?,
        max_thinking_tokens: optional_positive(
            fields.max_thinking_tokens,
            context,
            "max_thinking_tokens",
        )?,
        max_budget: if fields.max_budget_usd == 0.0 {
            None
        } else {
            Some(
                BudgetUsd::try_from(fields.max_budget_usd).map_err(|source| {
                    BuildError::Budget {
                        context: context.into(),
                        source,
                    }
                })?,
            )
        },
        permission_mode: optional_permission(&fields.permission_mode, context)?,
    })
}

pub(crate) fn backend(
    context: &str,
    backend: &str,
    transport: &str,
    inherited: AgentBackend,
) -> Result<AgentBackend, BuildError> {
    if backend.is_empty() && transport.is_empty() {
        return Ok(inherited);
    }
    let backend = if backend.is_empty() {
        match inherited {
            AgentBackend::Claude(_) => "claude",
            AgentBackend::Cursor => "cursor",
            AgentBackend::Codex => "codex",
        }
    } else {
        backend
    };
    let transport = if transport.is_empty() {
        if matches!(backend, "cursor" | "codex") {
            "acp"
        } else {
            "sdk"
        }
    } else {
        transport
    };
    match (backend, transport) {
        ("claude", "sdk") => Ok(AgentBackend::Claude(ClaudeTransport::Sdk)),
        ("claude", "acp") => Ok(AgentBackend::Claude(ClaudeTransport::Acp)),
        ("cursor", "acp") => Ok(AgentBackend::Cursor),
        ("codex", "acp") => Ok(AgentBackend::Codex),
        ("cursor" | "codex", _) => Err(BuildError::Invalid {
            context: context.into(),
            message: format!("backend {backend:?} requires transport \"acp\""),
        }),
        _ => Err(BuildError::Invalid {
            context: context.into(),
            message: format!("unsupported backend/transport combination {backend:?}/{transport:?}"),
        }),
    }
}

pub(crate) fn optional_positive(
    value: i64,
    context: &str,
    field: &'static str,
) -> Result<Option<NonZeroUsize>, BuildError> {
    if value == 0 {
        return Ok(None);
    }
    let value = usize::try_from(value).map_err(|_| invalid_number(context, field))?;
    Ok(NonZeroUsize::new(value))
}

pub(crate) fn validate_hosts(hosts: &[String], context: &str) -> Result<(), BuildError> {
    if let Some(host) = hosts.iter().find(|host| {
        host.is_empty()
            || !host.chars().all(|character| {
                character.is_ascii_alphanumeric() || matches!(character, ':' | '.' | '-' | '_')
            })
    }) {
        return Err(BuildError::Invalid {
            context: context.into(),
            message: format!("{host:?} is not a valid hostname"),
        });
    }
    Ok(())
}

fn security_config(raw: RawSecurity, context: &str) -> Result<SecurityConfig, BuildError> {
    validate_hosts(&raw.outbound_allowlist, context)?;
    if raw.fleet_budget_usd < 0.0 {
        return Err(BuildError::Invalid {
            context: context.into(),
            message: "fleet_budget_usd must be non-negative".into(),
        });
    }
    Ok(SecurityConfig {
        enabled: raw.enabled,
        tier1_enabled: raw.tier1_enabled,
        tier2_enabled: raw.tier2_enabled,
        outbound_allowlist: raw.outbound_allowlist,
        fleet_budget_usd: raw.fleet_budget_usd,
        concurrency_cap: optional_usize(raw.concurrency_cap, context, "concurrency_cap")?,
        batch_size: optional_usize(raw.batch_size, context, "batch_size")?,
        debounce_ms: optional_u64(raw.debounce_ms, context, "debounce_ms")?,
    })
}

fn optional_effort(value: &str, context: &str) -> Result<Option<EffortLevel>, BuildError> {
    if value.is_empty() {
        return Ok(None);
    }
    crate::workflow::profile::effort_from_str(value)
        .map(Some)
        .ok_or_else(|| BuildError::InvalidValue {
            context: context.into(),
            field: "effort",
            value: value.into(),
            expected: "low, medium, high, xhigh, or max",
        })
}

fn optional_permission(value: &str, context: &str) -> Result<Option<PermissionMode>, BuildError> {
    if value.is_empty() {
        return Ok(None);
    }
    crate::workflow::profile::permission_from_str(value)
        .map(Some)
        .ok_or_else(|| BuildError::InvalidValue {
            context: context.into(),
            field: "permission_mode",
            value: value.into(),
            expected: "default, acceptEdits, plan, or bypassPermissions",
        })
}

fn positive_or_default(
    value: i64,
    default: usize,
    context: &str,
    field: &'static str,
) -> Result<NonZeroUsize, BuildError> {
    if value == 0 {
        return Ok(NonZeroUsize::new(default).expect("default must be positive"));
    }
    Ok(optional_positive(value, context, field)?.expect("non-zero input must remain non-zero"))
}

fn optional_usize(
    value: i64,
    context: &str,
    field: &'static str,
) -> Result<Option<usize>, BuildError> {
    if value == 0 {
        return Ok(None);
    }
    usize::try_from(value)
        .map(Some)
        .map_err(|_| invalid_number(context, field))
}

fn optional_u64(value: i64, context: &str, field: &'static str) -> Result<Option<u64>, BuildError> {
    if value == 0 {
        return Ok(None);
    }
    u64::try_from(value)
        .map(Some)
        .map_err(|_| invalid_number(context, field))
}

fn invalid_number(context: &str, field: &'static str) -> BuildError {
    BuildError::Invalid {
        context: context.into(),
        message: format!("{field} must be non-negative"),
    }
}
