use std::borrow::Borrow;
use std::fmt;
use std::str::FromStr;

use thiserror::Error;

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub struct StepId(String);

#[derive(Clone, Debug, Eq, Error, PartialEq)]
pub enum StepIdError {
    #[error("step id is empty")]
    Empty,
    #[error("step id contains invalid character {character:?} at byte {index}")]
    InvalidCharacter { index: usize, character: char },
}

impl StepId {
    pub fn new(value: impl Into<String>) -> Result<Self, StepIdError> {
        Self::try_from(value.into())
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }

    pub fn into_string(self) -> String {
        self.0
    }
}

impl TryFrom<String> for StepId {
    type Error = StepIdError;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        validate(&value)?;
        Ok(Self(value))
    }
}

impl TryFrom<&str> for StepId {
    type Error = StepIdError;

    fn try_from(value: &str) -> Result<Self, Self::Error> {
        Self::try_from(value.to_owned())
    }
}

impl FromStr for StepId {
    type Err = StepIdError;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        Self::try_from(value)
    }
}

impl AsRef<str> for StepId {
    fn as_ref(&self) -> &str {
        self.as_str()
    }
}

impl Borrow<str> for StepId {
    fn borrow(&self) -> &str {
        self.as_str()
    }
}

impl fmt::Display for StepId {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.as_str())
    }
}

fn validate(value: &str) -> Result<(), StepIdError> {
    if value.is_empty() {
        return Err(StepIdError::Empty);
    }
    if let Some((index, character)) = value.char_indices().find(|(_, character)| {
        !(character.is_ascii_alphanumeric() || matches!(character, '_' | '-'))
    }) {
        return Err(StepIdError::InvalidCharacter { index, character });
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_workflow_identifier_characters() {
        assert_eq!(
            StepId::new("build_docs-2").unwrap().as_str(),
            "build_docs-2"
        );
    }

    #[test]
    fn rejects_empty_and_punctuated_identifiers() {
        assert_eq!(StepId::new("").unwrap_err(), StepIdError::Empty);
        assert!(matches!(
            StepId::new("build.docs"),
            Err(StepIdError::InvalidCharacter { .. })
        ));
    }
}
