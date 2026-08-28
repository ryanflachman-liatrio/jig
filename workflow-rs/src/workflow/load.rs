use std::collections::HashSet;
use std::fs;
use std::path::{Path, PathBuf};

use thiserror::Error;

use super::convert::{self, BuildError};
use super::raw::{RawProfileFile, RawWorkflow};
use super::{AgentProfile, ProfileId, Workflow};

#[derive(Debug, Error)]
pub enum LoadError {
    #[error("could not read {path:?}: {source}")]
    Io {
        path: PathBuf,
        #[source]
        source: std::io::Error,
    },
    #[error("could not parse workflow TOML: {0}")]
    Toml(#[source] toml::de::Error),
    #[error("could not parse profile file {path:?}: {source}")]
    ProfileToml {
        path: PathBuf,
        #[source]
        source: toml::de::Error,
    },
    #[error("{0}")]
    InvalidProfile(String),
    #[error(transparent)]
    Build(#[from] BuildError),
}

pub fn load(path: impl AsRef<Path>) -> Result<Workflow, LoadError> {
    let path = path.as_ref();
    let data = fs::read_to_string(path).map_err(|source| LoadError::Io {
        path: path.to_owned(),
        source,
    })?;
    let mut workflow = decode(&data, path.parent())?;
    workflow.set_source_path(path.canonicalize().unwrap_or_else(|_| path.to_owned()));
    Ok(workflow)
}

/// Passing `None` skips filesystem-backed authoring-resource resolution.
pub fn decode(data: &str, base_dir: Option<&Path>) -> Result<Workflow, LoadError> {
    let raw: RawWorkflow = toml::from_str(data).map_err(LoadError::Toml)?;
    let profiles = load_profiles(base_dir)?;
    convert::build_workflow(raw, base_dir, profiles, data.to_owned()).map_err(Into::into)
}

fn load_profiles(base_dir: Option<&Path>) -> Result<Vec<AgentProfile>, LoadError> {
    let mut profiles = vec![AgentProfile::interactive(), AgentProfile::autonomous()];
    let reserved = profiles
        .iter()
        .map(|profile| profile.id().clone())
        .collect::<HashSet<_>>();
    let Some(base_dir) = base_dir else {
        return Ok(profiles);
    };
    let directory = base_dir.join(".agents/jig/profiles");
    if !directory.exists() {
        return Ok(profiles);
    }

    let mut entries = fs::read_dir(&directory)
        .map_err(|source| LoadError::Io {
            path: directory.clone(),
            source,
        })?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|source| LoadError::Io {
            path: directory.clone(),
            source,
        })?;
    entries.sort_by_key(std::fs::DirEntry::file_name);

    let mut seen = HashSet::<ProfileId>::new();
    for entry in entries {
        let path = entry.path();
        if !path.is_file()
            || path.extension().and_then(|extension| extension.to_str()) != Some("toml")
        {
            continue;
        }
        let data = fs::read_to_string(&path).map_err(|source| LoadError::Io {
            path: path.clone(),
            source,
        })?;
        let raw: RawProfileFile =
            toml::from_str(&data).map_err(|source| LoadError::ProfileToml {
                path: path.clone(),
                source,
            })?;
        for profile in raw.agents {
            let profile = convert::profile::convert(profile, &path.display().to_string())?;
            if reserved.contains(profile.id()) {
                return Err(LoadError::InvalidProfile(format!(
                    "profile {} shadows a built-in profile",
                    profile.id()
                )));
            }
            if !seen.insert(profile.id().clone()) {
                return Err(LoadError::InvalidProfile(format!(
                    "duplicate profile {}",
                    profile.id()
                )));
            }
            profiles.push(profile);
        }
    }
    Ok(profiles)
}
