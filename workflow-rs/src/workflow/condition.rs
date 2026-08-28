use std::fmt;
use std::str::FromStr;

use thiserror::Error;

use super::StepId;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Comparison {
    Truthy,
    Equal,
    NotEqual,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FieldPath(Vec<String>);

impl FieldPath {
    pub(crate) fn from_segments(segments: Vec<String>) -> Self {
        Self(segments)
    }

    pub fn segments(&self) -> &[String] {
        &self.0
    }

    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }
}

impl fmt::Display for FieldPath {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0.join("."))
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Condition {
    raw: String,
    step: StepId,
    field: FieldPath,
    comparison: Comparison,
    value: Option<String>,
}

impl Condition {
    pub fn raw(&self) -> &str {
        &self.raw
    }

    pub fn step(&self) -> &StepId {
        &self.step
    }

    pub fn field(&self) -> &FieldPath {
        &self.field
    }

    pub fn comparison(&self) -> Comparison {
        self.comparison
    }

    pub fn value(&self) -> Option<&str> {
        self.value.as_deref()
    }
}

#[derive(Clone, Debug, Eq, Error, PartialEq)]
pub enum ConditionParseError {
    #[error("condition is empty")]
    Empty,
    #[error("invalid condition reference {0:?}")]
    InvalidReference(String),
    #[error("condition contains mismatched quotes in {0:?}")]
    MismatchedQuotes(String),
}

impl FromStr for Condition {
    type Err = ConditionParseError;

    fn from_str(raw: &str) -> Result<Self, Self::Err> {
        let expression = raw.trim();
        if expression.is_empty() {
            return Err(ConditionParseError::Empty);
        }

        for (token, comparison) in [("==", Comparison::Equal), ("!=", Comparison::NotEqual)] {
            if let Some((left, right)) = expression.split_once(token) {
                let (step, field) = parse_reference(left.trim())?;
                return Ok(Self {
                    raw: raw.to_owned(),
                    step,
                    field,
                    comparison,
                    value: Some(unquote(right.trim())?),
                });
            }
        }

        let (step, field) = parse_reference(expression)?;
        Ok(Self {
            raw: raw.to_owned(),
            step,
            field,
            comparison: Comparison::Truthy,
            value: None,
        })
    }
}

fn parse_reference(value: &str) -> Result<(StepId, FieldPath), ConditionParseError> {
    let mut segments = value.split('.');
    let step = segments.next().unwrap_or_default();
    let step = StepId::try_from(step)
        .map_err(|_| ConditionParseError::InvalidReference(value.to_owned()))?;
    let fields = segments.map(str::to_owned).collect::<Vec<_>>();
    if fields
        .iter()
        .any(|field| StepId::try_from(field.as_str()).is_err())
    {
        return Err(ConditionParseError::InvalidReference(value.to_owned()));
    }
    Ok((step, FieldPath(fields)))
}

fn unquote(value: &str) -> Result<String, ConditionParseError> {
    let bytes = value.as_bytes();
    if bytes.len() >= 2
        && matches!(
            (bytes[0], bytes[bytes.len() - 1]),
            (b'\'', b'\'') | (b'"', b'"')
        )
    {
        return Ok(value[1..value.len() - 1].to_owned());
    }
    if value.contains(['\'', '"']) {
        return Err(ConditionParseError::MismatchedQuotes(value.to_owned()));
    }
    Ok(value.to_owned())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_field_comparison() {
        let condition: Condition = "research.status == 'done'".parse().unwrap();
        assert_eq!(condition.step().as_str(), "research");
        assert_eq!(condition.field().segments(), ["status"]);
        assert_eq!(condition.value(), Some("done"));
    }

    #[test]
    fn parses_truthy_verdict() {
        let condition: Condition = "approved".parse().unwrap();
        assert_eq!(condition.comparison(), Comparison::Truthy);
        assert!(condition.field().is_empty());
    }
}
