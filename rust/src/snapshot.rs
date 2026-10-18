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

/// An observable difference between two snapshots, in deterministic path order.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PathDifference {
    pub path: String,
    pub changes: Vec<SnapshotChange>,
    pub before: Option<PathState>,
    pub after: Option<PathState>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum SnapshotChange {
    Existence,
    Kind,
    Content,
    Target,
}

impl FilesystemSnapshot {
    /// Compare the union of explicitly selected paths without performing I/O.
    #[must_use]
    pub fn differences(&self, after: &Self) -> Vec<PathDifference> {
        self.entries
            .keys()
            .chain(after.entries.keys())
            .collect::<BTreeSet<_>>()
            .into_iter()
            .filter_map(|path| {
                let before = self.entries.get(path);
                let next = after.entries.get(path);
                if before == next {
                    return None;
                }
                let change = match (before, next) {
                    (Some(PathState::RegularFile { .. }), Some(PathState::RegularFile { .. })) => {
                        SnapshotChange::Content
                    }
                    (Some(PathState::Symlink { .. }), Some(PathState::Symlink { .. })) => {
                        SnapshotChange::Target
                    }
                    (None | Some(PathState::Missing), _) | (_, None | Some(PathState::Missing)) => {
                        SnapshotChange::Existence
                    }
                    _ => SnapshotChange::Kind,
                };
                Some(PathDifference {
                    path: path.clone(),
                    changes: vec![change],
                    before: before.cloned(),
                    after: next.cloned(),
                })
            })
            .collect()
    }
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

    /// Capture the baseline's paths and assert that every physical state is unchanged.
    ///
    /// # Errors
    /// Returns capture errors or an assertion containing deterministic per-path differences.
    pub async fn check_snapshot(&self, baseline: &FilesystemSnapshot) -> Result<(), Error> {
        let after = self.snapshot(baseline.entries.keys().cloned()).await?;
        let differences = baseline.differences(&after);
        if differences.is_empty() {
            Ok(())
        } else {
            Err(Error::Assertion(format!(
                "filesystem snapshot differences: {differences:?}"
            )))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::CommandResult;

    #[test]
    fn differences_are_sorted_and_distinguish_all_changes() {
        let before = FilesystemSnapshot {
            entries: BTreeMap::from([
                ("z".into(), PathState::Missing),
                ("b".into(), PathState::Directory),
                (
                    "a".into(),
                    PathState::RegularFile {
                        content: vec![0, 255],
                    },
                ),
                (
                    "c".into(),
                    PathState::Symlink {
                        target: b"../literal\n".to_vec(),
                    },
                ),
            ]),
        };
        let mut after = before.clone();
        after.entries.insert("z".into(), PathState::Directory);
        after
            .entries
            .insert("b".into(), PathState::RegularFile { content: vec![] });
        after
            .entries
            .insert("a".into(), PathState::RegularFile { content: vec![0] });
        after.entries.insert(
            "c".into(),
            PathState::Symlink {
                target: b"missing".to_vec(),
            },
        );
        assert!(before.differences(&before).is_empty());
        let differences = before.differences(&after);
        assert_eq!(
            differences
                .iter()
                .map(|diff| diff.path.as_str())
                .collect::<Vec<_>>(),
            ["a", "b", "c", "z"]
        );
        assert_eq!(
            differences
                .iter()
                .map(|diff| diff.changes.clone())
                .collect::<Vec<_>>(),
            [
                vec![SnapshotChange::Content],
                vec![SnapshotChange::Kind],
                vec![SnapshotChange::Target],
                vec![SnapshotChange::Existence]
            ]
        );
        assert_eq!(
            before.entries["a"],
            PathState::RegularFile {
                content: vec![0, 255]
            }
        );
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
