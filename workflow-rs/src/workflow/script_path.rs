use std::path::{Path, PathBuf};

pub fn repo_root(start: impl AsRef<Path>) -> Option<PathBuf> {
    let start = start.as_ref();
    if start.as_os_str().is_empty() {
        return None;
    }
    let mut directory = start.canonicalize().ok()?;
    loop {
        if directory.join(".git").exists() {
            return Some(directory);
        }
        if !directory.pop() {
            return None;
        }
    }
}

pub fn script_path(root: impl AsRef<Path>, script: impl AsRef<Path>) -> PathBuf {
    let root = root.as_ref();
    let script = script.as_ref();
    if root.as_os_str().is_empty() || script.is_absolute() {
        script.to_owned()
    } else {
        root.join(script)
    }
}
