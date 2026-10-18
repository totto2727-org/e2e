use crate::{Environment, Error, ExecutionFailure, PartialOutput, driver, path_valid};
use serde::Deserialize;
use std::collections::{BTreeMap, BTreeSet};

/// Physical state of exactly one requested path. Symlinks are never followed.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum PathState {
    Missing,
    RegularFile { content: Vec<u8> },
    Directory,
    Symlink { target: Vec<u8> },
}

/// Read-only filesystem baseline, sorted by the caller's literal path strings.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct FilesystemSnapshot {
    pub entries: BTreeMap<String, PathState>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WireEntry {
    path: String,
    kind: WireKind,
    data: Vec<u8>,
    target: Vec<u8>,
}
#[derive(Deserialize)]
#[serde(rename_all = "lowercase")]
enum WireKind {
    Missing,
    File,
    Directory,
    Symlink,
}

fn decode_snapshot(paths: &[String], entries: Vec<WireEntry>) -> Result<FilesystemSnapshot, Error> {
    let fail = || Error::Execution {
        failure: ExecutionFailure::Protocol("invalid filesystem snapshot paths or payload".into()),
        output: PartialOutput::default(),
    };
    if paths.len() != entries.len() {
        return Err(fail());
    }
    let mut result = BTreeMap::new();
    for (path, entry) in paths.iter().zip(entries) {
        if path != &entry.path {
            return Err(fail());
        }
        let state = match entry.kind {
            WireKind::Missing if entry.data.is_empty() && entry.target.is_empty() => {
                PathState::Missing
            }
            WireKind::Directory if entry.data.is_empty() && entry.target.is_empty() => {
                PathState::Directory
            }
            WireKind::File if entry.target.is_empty() => PathState::RegularFile {
                content: entry.data,
            },
            WireKind::Symlink if entry.data.is_empty() => PathState::Symlink {
                target: entry.target,
            },
            _ => return Err(fail()),
        };
        result.insert(entry.path, state);
    }
    Ok(FilesystemSnapshot { entries: result })
}

impl Environment {
    /// Capture only explicit paths, without recursion or metadata. Requires an installed Linux driver.
    ///
    /// # Errors
    /// Returns input or execution errors. Permission, read, and unsupported-kind failures are not absence.
    pub async fn snapshot(
        &self,
        paths: impl IntoIterator<Item = impl Into<String>>,
    ) -> Result<FilesystemSnapshot, Error> {
        let paths: Vec<String> = paths
            .into_iter()
            .map(Into::into)
            .collect::<BTreeSet<_>>()
            .into_iter()
            .collect();
        for path in &paths {
            path_valid(path)?;
        }
        let entries =
            driver::invoke(self, "snapshot", &serde_json::json!({"paths":paths}), None).await?;
        decode_snapshot(&paths, entries)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::CommandResult;

    #[test]
    fn decoded_snapshot_preserves_sorted_paths_and_exact_states() -> Result<(), Error> {
        let snapshot = decode_snapshot(
            &["/file".into(), "/link".into()],
            vec![
                WireEntry {
                    path: "/file".into(),
                    kind: WireKind::File,
                    data: vec![0, 255],
                    target: vec![],
                },
                WireEntry {
                    path: "/link".into(),
                    kind: WireKind::Symlink,
                    data: vec![],
                    target: b"../literal\n".to_vec(),
                },
            ],
        )?;
        assert_eq!(
            snapshot,
            FilesystemSnapshot {
                entries: BTreeMap::from([
                    (
                        "/file".into(),
                        PathState::RegularFile {
                            content: vec![0, 255]
                        }
                    ),
                    (
                        "/link".into(),
                        PathState::Symlink {
                            target: b"../literal\n".to_vec()
                        }
                    ),
                ]),
            }
        );
        Ok(())
    }

    #[test]
    fn malformed_or_failed_capture_never_becomes_missing() {
        for payload in [
            r#"{"ok":false,"error":"PermissionError","observation":null}"#,
            r#"{"ok":true,"value":[{"path":"/fixture","kind":"fifo","data":[],"target":[]}]}"#,
            r#"{"ok":true,"value":[{"path":"/fixture","kind":"file","data":[256],"target":[]}]}"#,
            "not json",
        ] {
            let result = CommandResult {
                stdout: payload.as_bytes().to_vec(),
                ..Default::default()
            };
            assert!(driver::decode::<Vec<WireEntry>>(&result).is_err());
        }
        assert!(decode_snapshot(&["/fixture".into()], vec![]).is_err());
        for (path, kind, data, target) in [
            ("/wrong", WireKind::Missing, vec![], vec![]),
            ("/fixture", WireKind::Directory, vec![1], vec![]),
            ("/fixture", WireKind::File, vec![], vec![1]),
        ] {
            assert!(
                decode_snapshot(
                    &["/fixture".into()],
                    vec![WireEntry {
                        path: path.into(),
                        kind,
                        data,
                        target
                    }]
                )
                .is_err()
            );
        }
    }

    #[test]
    fn exact_bytes_and_dangling_literal_targets_decode() -> Result<(), Error> {
        let result = CommandResult { stdout: br#"{"ok":true,"value":[{"path":"/binary","kind":"file","data":[0,255,10],"target":[]},{"path":"/link","kind":"symlink","data":[],"target":[255,10]}]}"#.to_vec(), ..Default::default() };
        let snapshot = decode_snapshot(
            &["/binary".into(), "/link".into()],
            driver::decode(&result)?,
        )?;
        assert_eq!(
            snapshot.entries["/binary"],
            PathState::RegularFile {
                content: vec![0, 255, 10]
            }
        );
        assert_eq!(
            snapshot.entries["/link"],
            PathState::Symlink {
                target: vec![255, 10]
            }
        );
        Ok(())
    }
}
