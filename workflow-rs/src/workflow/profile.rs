use std::borrow::Borrow;
use std::fmt;

use thiserror::Error;

use super::{EffortLevel, ModelConfig, PermissionMode, ToolPolicy};

#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct ProfileId(String);

#[derive(Clone, Debug, Eq, Error, PartialEq)]
pub enum ProfileIdError {
    #[error("profile id is empty")]
    Empty,
    #[error("profile id must start with '@'")]
    MissingPrefix,
    #[error("profile id contains an invalid character")]
    InvalidCharacter,
}

impl TryFrom<String> for ProfileId {
    type Error = ProfileIdError;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        let Some(bare) = value.strip_prefix('@') else {
            return Err(if value.is_empty() {
                ProfileIdError::Empty
            } else {
                ProfileIdError::MissingPrefix
            });
        };
        if bare.is_empty()
            || !bare
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-'))
        {
            return Err(ProfileIdError::InvalidCharacter);
        }
        Ok(Self(value))
    }
}

impl ProfileId {
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl Borrow<str> for ProfileId {
    fn borrow(&self) -> &str {
        self.as_str()
    }
}

impl fmt::Display for ProfileId {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.as_str())
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct AgentProfile {
    pub(crate) id: ProfileId,
    pub(crate) tools: ToolPolicy,
    pub(crate) model: ModelConfig,
    pub(crate) ask_user_question: bool,
    pub(crate) append_system_prompt: Option<String>,
}

impl AgentProfile {
    pub fn id(&self) -> &ProfileId {
        &self.id
    }

    pub fn tools(&self) -> &ToolPolicy {
        &self.tools
    }

    pub fn model(&self) -> &ModelConfig {
        &self.model
    }

    pub(crate) fn interactive() -> Self {
        Self {
            id: ProfileId::try_from("@interactive".to_owned()).unwrap(),
            tools: ToolPolicy {
                allowed: vec!["AskUserQuestion".into()],
                disallowed: Vec::new(),
            },
            model: ModelConfig::default(),
            ask_user_question: true,
            append_system_prompt: None,
        }
    }

    pub(crate) fn autonomous() -> Self {
        Self {
            id: ProfileId::try_from("@autonomous".to_owned()).unwrap(),
            tools: ToolPolicy {
                allowed: Vec::new(),
                disallowed: vec!["AskUserQuestion".into()],
            },
            model: ModelConfig::default(),
            ask_user_question: false,
            append_system_prompt: None,
        }
    }
}

pub(crate) fn effort_from_str(value: &str) -> Option<EffortLevel> {
    match value {
        "low" => Some(EffortLevel::Low),
        "medium" => Some(EffortLevel::Medium),
        "high" => Some(EffortLevel::High),
        "xhigh" => Some(EffortLevel::ExtraHigh),
        "max" => Some(EffortLevel::Max),
        _ => None,
    }
}

pub(crate) fn permission_from_str(value: &str) -> Option<PermissionMode> {
    match value {
        "default" => Some(PermissionMode::Default),
        "acceptEdits" => Some(PermissionMode::AcceptEdits),
        "plan" => Some(PermissionMode::Plan),
        "bypassPermissions" => Some(PermissionMode::BypassPermissions),
        _ => None,
    }
}
