use thiserror::Error;

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct AgentFile {
    pub name: Option<String>,
    pub description: Option<String>,
    pub tools: Vec<String>,
    pub model: Option<String>,
    pub prompt: String,
}

#[derive(Clone, Debug, Eq, Error, PartialEq)]
pub enum AgentFileError {
    #[error("missing YAML frontmatter")]
    MissingFrontmatter,
    #[error("unterminated YAML frontmatter")]
    UnterminatedFrontmatter,
    #[error("invalid frontmatter line {0:?}; expected `key: value`")]
    InvalidLine(String),
    #[error("skill frontmatter field {0:?} is required")]
    MissingSkillField(&'static str),
}

pub(crate) fn parse_agent_file(data: &[u8]) -> Result<AgentFile, AgentFileError> {
    let text = String::from_utf8_lossy(data).replace("\r\n", "\n");
    let lines = text.lines().collect::<Vec<_>>();
    let mut index = lines
        .iter()
        .position(|line| !line.trim().is_empty())
        .unwrap_or(lines.len());
    if lines.get(index).map(|line| line.trim()) != Some("---") {
        return Err(AgentFileError::MissingFrontmatter);
    }

    index += 1;
    let frontmatter_start = index;
    while index < lines.len() && lines[index].trim() != "---" {
        index += 1;
    }
    if index == lines.len() {
        return Err(AgentFileError::UnterminatedFrontmatter);
    }

    let mut file = AgentFile {
        prompt: lines[index + 1..].join("\n").trim().to_owned(),
        ..AgentFile::default()
    };
    for line in &lines[frontmatter_start..index] {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let (key, value) = line
            .split_once(':')
            .ok_or_else(|| AgentFileError::InvalidLine(line.to_owned()))?;
        let value = value.trim().trim_matches(['\'', '"']);
        match key.trim() {
            "name" => file.name = nonblank(value),
            "description" => file.description = nonblank(value),
            "model" => file.model = nonblank(value),
            "tools" => file.tools = parse_tools(value),
            _ => {}
        }
    }
    Ok(file)
}

pub(crate) fn parse_skill_file(data: &[u8]) -> Result<AgentFile, AgentFileError> {
    let file = parse_agent_file(data)?;
    if file.name.is_none() {
        return Err(AgentFileError::MissingSkillField("name"));
    }
    if file.description.is_none() {
        return Err(AgentFileError::MissingSkillField("description"));
    }
    if file.prompt.is_empty() {
        return Err(AgentFileError::MissingSkillField("instruction body"));
    }
    Ok(file)
}

pub fn parse_agent_file_content(data: &[u8]) -> Result<(Option<String>, String), AgentFileError> {
    let file = parse_agent_file(data)?;
    Ok((file.model, file.prompt))
}

fn nonblank(value: &str) -> Option<String> {
    (!value.trim().is_empty()).then(|| value.to_owned())
}

fn parse_tools(value: &str) -> Vec<String> {
    value
        .trim()
        .trim_start_matches('[')
        .trim_end_matches(']')
        .split(',')
        .map(|tool| tool.trim().trim_matches(['\'', '"']))
        .filter(|tool| !tool.is_empty())
        .map(str::to_owned)
        .collect()
}
