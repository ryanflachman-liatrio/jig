use serde::Deserialize;

#[derive(Clone, Debug, Deserialize)]
#[serde(untagged)]
pub(crate) enum RawInput {
    String(String),
    Table(RawInputTable),
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct RawInputTable {
    #[serde(rename = "ref")]
    pub reference: Option<String>,
    pub path: Option<String>,
    pub inline: bool,
    pub from: Option<String>,
    pub label: Option<String>,
    #[serde(rename = "as")]
    pub name: Option<String>,
}
