//! Async CLI cases in fresh Testcontainers-managed Linux containers.
//!
//! ```no_run
//! use cli_e2e::{Command, CommandResult, ScopeError, with_environment};
//! # async fn example() -> Result<(), ScopeError<cli_e2e::Error>> {
//! with_environment("e2e-example:local", |env| async move {
//!     env.check_command(Command::new(["echo", "hello"]),
//!         CommandResult { exit_code: 0, stdout: b"hello\n".to_vec(), stderr: vec![] }).await
//! }).await?;
//! # Ok(()) }
//! ```
use futures_util::FutureExt;
use std::{
    fmt,
    future::Future,
    panic::AssertUnwindSafe,
    path::Path,
    sync::{Arc, Weak},
    time::Duration,
};
use testcontainers::{
    ContainerAsync, GenericImage, TestcontainersError,
    bollard::{self, query_parameters::UploadToContainerOptionsBuilder},
    core::{ExecCommand, client::docker_client_instance},
    runners::AsyncRunner,
};
use tokio::{
    io::AsyncReadExt,
    sync::RwLock,
    time::{Instant, timeout, timeout_at},
};
use tokio_util::sync::CancellationToken;

const CASE_BUDGET: Duration = Duration::from_secs(60);
const CLEANUP_LOCK_BUDGET: Duration = Duration::from_secs(30);
type Container = Arc<RwLock<Option<ContainerAsync<GenericImage>>>>;

/// Argv, optional process working directory, and `KEY=value` environment entries.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Command {
    pub args: Vec<String>,
    pub working_dir: Option<String>,
    pub env: Vec<String>,
}
impl Command {
    pub fn new(args: impl IntoIterator<Item = impl Into<String>>) -> Self {
        Self {
            args: args.into_iter().map(Into::into).collect(),
            ..Self::default()
        }
    }
    /// Validate the executable and reject NUL bytes or malformed environment entries.
    ///
    /// # Errors
    /// Returns [`Error::InvalidInput`] for an empty executable, NUL input, or an
    /// environment entry without a nonempty key and `=` separator.
    pub fn validate(&self) -> Result<(), Error> {
        if self.args.first().is_none_or(String::is_empty) {
            return Err(Error::InvalidInput("empty executable".into()));
        }
        for value in self
            .args
            .iter()
            .chain(self.working_dir.iter())
            .chain(self.env.iter())
        {
            valid(value)?;
        }
        for entry in &self.env {
            environment_entry(entry)?;
        }
        Ok(())
    }
    fn into_exec(self) -> Result<ExecCommand, Error> {
        self.validate()?;
        let mut args = self.args;
        if let Some(directory) = self.working_dir {
            // Testcontainers has no per-exec working-directory option. Never
            // interpolate caller data into this script: cwd and argv are positional.
            let mut wrapped = vec![
                "sh".into(),
                "-c".into(),
                "cd -- \"$1\" && shift && exec \"$@\"".into(),
                "cli-e2e".into(),
                directory,
            ];
            wrapped.append(&mut args);
            args = wrapped;
        }
        let env = self
            .env
            .iter()
            .map(|entry| environment_entry(entry))
            .collect::<Result<Vec<_>, _>>()?;
        Ok(ExecCommand::new(args).with_env_vars(env))
    }
}
fn environment_entry(entry: &str) -> Result<(&str, &str), Error> {
    entry
        .split_once('=')
        .filter(|(key, _)| !key.is_empty())
        .ok_or_else(|| Error::InvalidInput("environment entry must be KEY=value".into()))
}
/// Successful execution, including application failure exit codes.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CommandResult {
    pub exit_code: i64,
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
}
/// Bytes already drained from Testcontainers before execution failed.
/// Stdout is drained before stderr, so unread stderr is not guaranteed here.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct PartialOutput {
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
}
#[derive(Debug)]
pub enum ExecutionFailure {
    Deadline,
    Cancelled,
    Testcontainers(Box<TestcontainersError>),
    Io(std::io::Error),
    Protocol(String),
}
/// Typed failures retain cleanup failures alongside the original case error.
#[derive(Debug)]
pub enum Error {
    InvalidInput(String),
    Unsupported(String),
    Testcontainers(Box<TestcontainersError>),
    Docker(bollard::errors::Error),
    Execution {
        failure: ExecutionFailure,
        output: PartialOutput,
    },
    Assertion(String),
    Archive(std::io::Error),
    Deadline,
    Cancelled,
    Cleanup {
        resource: String,
        cause: Box<Error>,
    },
}
impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{self:?}")
    }
}
impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Archive(e)
            | Self::Execution {
                failure: ExecutionFailure::Io(e),
                ..
            } => Some(e),
            Self::Cleanup { cause, .. } => Some(cause),
            Self::Docker(e) => Some(e),
            Self::Testcontainers(e)
            | Self::Execution {
                failure: ExecutionFailure::Testcontainers(e),
                ..
            } => Some(e.as_ref()),
            _ => None,
        }
    }
}
impl From<TestcontainersError> for Error {
    fn from(error: TestcontainersError) -> Self {
        Self::Testcontainers(Box::new(error))
    }
}
impl From<bollard::errors::Error> for Error {
    fn from(error: bollard::errors::Error) -> Self {
        Self::Docker(error)
    }
}
impl From<std::io::Error> for Error {
    fn from(error: std::io::Error) -> Self {
        Self::Archive(error)
    }
}
fn valid(value: &str) -> Result<(), Error> {
    if value.contains('\0') {
        Err(Error::InvalidInput("NUL input".into()))
    } else {
        Ok(())
    }
}
fn path_valid(value: &str) -> Result<(), Error> {
    valid(value)?;
    if value.is_empty() {
        Err(Error::InvalidInput("empty path".into()))
    } else {
        Ok(())
    }
}

/// Scoped lifecycle failure preserving the caller's concrete callback error.
#[derive(Debug)]
pub enum ScopeError<E> {
    Infrastructure(Error),
    Callback(E),
    Cleanup {
        prior: Option<Box<ScopeError<E>>>,
        errors: Vec<Error>,
    },
}
impl<E: fmt::Display> fmt::Display for ScopeError<E> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Infrastructure(error) => error.fmt(f),
            Self::Callback(error) => error.fmt(f),
            Self::Cleanup { prior, errors } => {
                if let Some(prior) = prior {
                    write!(f, "{prior}; ")?;
                }
                write!(f, "cleanup failed: {errors:?}")
            }
        }
    }
}
impl<E: std::error::Error + 'static> std::error::Error for ScopeError<E> {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Infrastructure(error) => Some(error),
            Self::Callback(error) => Some(error),
            Self::Cleanup { prior, errors } => prior.as_deref().map_or_else(
                || errors.first().map(|error| error as &dyn std::error::Error),
                |prior| Some(prior as &dyn std::error::Error),
            ),
        }
    }
}
/// Case-scoped operations. Weak handles cannot extend the container's lifetime.
#[derive(Clone)]
pub struct Environment {
    container: Weak<RwLock<Option<ContainerAsync<GenericImage>>>>,
    docker: bollard::Docker,
    id: String,
    deadline: Instant,
    cancel: CancellationToken,
}
impl Environment {
    /// Immutable identity for isolation assertions, not a mutable Docker handle.
    #[must_use]
    pub fn container_id(&self) -> &str {
        &self.id
    }
    fn owner(&self) -> Result<Container, Error> {
        self.container.upgrade().ok_or(Error::Cancelled)
    }
    async fn bounded<T>(&self, future: impl Future<Output = Result<T, Error>>) -> Result<T, Error> {
        bounded(self.deadline, &self.cancel, future).await
    }
    /// Execute argv through Testcontainers and return status and separate streams.
    ///
    /// # Errors
    /// Returns an input error for invalid argv and an execution error with bytes
    /// already drained on transport failure, cancellation, or deadline expiry.
    pub async fn run(&self, command: Command) -> Result<CommandResult, Error> {
        let command = command.into_exec()?;
        let mut output = PartialOutput::default();
        let result = self
            .bounded(async {
                let owner = self.owner()?;
                let guard = owner.read().await;
                let container = guard.as_ref().ok_or(Error::Cancelled)?;
                let mut exec = container.exec(command).await?;
                exec.stdout().read_to_end(&mut output.stdout).await?;
                exec.stderr().read_to_end(&mut output.stderr).await?;
                loop {
                    if let Some(code) = exec.exit_code().await? {
                        break Ok(code);
                    }
                    tokio::time::sleep(Duration::from_millis(10)).await;
                }
            })
            .await;
        match result {
            Ok(exit_code) => Ok(CommandResult {
                exit_code,
                stdout: output.stdout,
                stderr: output.stderr,
            }),
            Err(error) => Err(Error::Execution {
                failure: match error {
                    Error::Deadline => ExecutionFailure::Deadline,
                    Error::Cancelled => ExecutionFailure::Cancelled,
                    Error::Testcontainers(error) => ExecutionFailure::Testcontainers(error),
                    Error::Archive(error) => ExecutionFailure::Io(error),
                    error => ExecutionFailure::Protocol(error.to_string()),
                },
                output,
            }),
        }
    }
    /// Execute a command and compare its exit code and both streams exactly.
    ///
    /// # Errors
    /// Returns execution errors from [`Self::run`] or [`Error::Assertion`] on mismatch.
    pub async fn check_command(
        &self,
        command: Command,
        expected: CommandResult,
    ) -> Result<(), Error> {
        compare_command(&self.run(command).await?, &expected)
    }
    /// Copy exact bytes into a regular file with mode `0644`.
    ///
    /// # Errors
    /// Returns input, archive, upload, cancellation, or deadline errors.
    /// The parent directory must already exist.
    pub async fn write_file(&self, path: &str, bytes: &[u8]) -> Result<(), Error> {
        path_valid(path)?;
        let path = Path::new(path);
        let name = path
            .file_name()
            .ok_or_else(|| Error::InvalidInput("file basename required".into()))?;
        let parent = path
            .parent()
            .filter(|p| !p.as_os_str().is_empty())
            .unwrap_or(Path::new("."));
        let archive = file_archive(name, bytes)?;
        self.bounded(async {
            let owner = self.owner()?;
            let guard = owner.read().await;
            let _container = guard.as_ref().ok_or(Error::Cancelled)?;
            // Testcontainers only supports copy-to before startup. Use its public
            // configured SDK client for this missing operation, not a second backend.
            self.docker
                .upload_to_container(
                    &self.id,
                    Some(
                        UploadToContainerOptionsBuilder::default()
                            .path(&parent.to_string_lossy())
                            .build(),
                    ),
                    bollard::body_full(archive.into()),
                )
                .await?;
            Ok(())
        })
        .await
    }
    /// Read bytes through Testcontainers' single-file copy API.
    ///
    /// # Errors
    /// Returns input, native copy, cancellation, or deadline errors.
    /// Archive validation and symbolic-link behavior are owned by Testcontainers.
    pub async fn read_file(&self, path: &str) -> Result<Vec<u8>, Error> {
        path_valid(path)?;
        self.bounded(async {
            let owner = self.owner()?;
            let guard = owner.read().await;
            let container = guard.as_ref().ok_or(Error::Cancelled)?;
            Ok(container.copy_file_from(path, Vec::new()).await?)
        })
        .await
    }
    /// Compare a file's bytes exactly.
    ///
    /// # Errors
    /// Returns errors from [`Self::read_file`] or [`Error::Assertion`] on mismatch.
    pub async fn check_file(&self, path: &str, expected: &[u8]) -> Result<(), Error> {
        let actual = self.read_file(path).await?;
        if actual == expected {
            Ok(())
        } else {
            Err(Error::Assertion(format!(
                "file {path:?}: {actual:?} != {expected:?}"
            )))
        }
    }
    async fn probe(&self, flag: &str, path: &str) -> Result<bool, Error> {
        path_valid(path)?;
        let result = self.run(Command::new(["test", flag, path])).await?;
        match result.exit_code {
            0 => Ok(true),
            1 => Ok(false),
            _ => Err(Error::Execution {
                failure: ExecutionFailure::Protocol(format!("path probe failed: {result:?}")),
                output: PartialOutput {
                    stdout: result.stdout,
                    stderr: result.stderr,
                },
            }),
        }
    }
    /// Assert that no path exists, including a dangling symbolic link.
    ///
    /// # Errors
    /// Returns input, execution, or assertion errors when the probes fail.
    pub async fn check_missing(&self, path: &str) -> Result<(), Error> {
        if self.probe("-e", path).await? || self.probe("-L", path).await? {
            return Err(Error::Assertion(format!("{path:?} exists")));
        }
        Ok(())
    }
    async fn physical(&self, flag: &str, path: &str) -> Result<(), Error> {
        if !self.probe(flag, path).await? || self.probe("-L", path).await? {
            return Err(Error::Assertion(format!("{path:?} is not physical {flag}")));
        }
        Ok(())
    }
    /// Assert a physical regular file, not a symbolic link.
    ///
    /// # Errors
    /// Returns input, execution, or assertion errors when the probes fail.
    pub async fn check_regular_file(&self, path: &str) -> Result<(), Error> {
        self.physical("-f", path).await
    }
    /// Assert a physical directory, not a symbolic link.
    ///
    /// # Errors
    /// Returns input, execution, or assertion errors when the probes fail.
    pub async fn check_directory(&self, path: &str) -> Result<(), Error> {
        self.physical("-d", path).await
    }
    /// Assert a symbolic link with exactly the supplied literal target.
    ///
    /// # Errors
    /// Returns input, execution, or assertion errors when the probes fail.
    pub async fn check_symlink(&self, path: &str, target: &str) -> Result<(), Error> {
        valid(target)?;
        if !self.probe("-L", path).await? {
            return Err(Error::Assertion(format!("{path:?} is not a symlink")));
        }
        self.check_command(
            Command::new(["readlink", "--", path]),
            CommandResult {
                stdout: format!("{target}\n").into_bytes(),
                ..Default::default()
            },
        )
        .await
    }
}
fn compare_command(actual: &CommandResult, expected: &CommandResult) -> Result<(), Error> {
    if actual == expected {
        Ok(())
    } else {
        Err(Error::Assertion(format!(
            "command {actual:?} != {expected:?}"
        )))
    }
}
fn file_archive(name: &std::ffi::OsStr, bytes: &[u8]) -> Result<Vec<u8>, Error> {
    let mut archive = tar::Builder::new(Vec::new());
    let mut header = tar::Header::new_gnu();
    header.set_size(bytes.len() as u64);
    header.set_mode(0o644);
    header.set_cksum();
    archive.append_data(&mut header, Path::new(name), bytes)?;
    Ok(archive.into_inner()?)
}
async fn bounded<T>(
    deadline: Instant,
    cancel: &CancellationToken,
    future: impl Future<Output = Result<T, Error>>,
) -> Result<T, Error> {
    tokio::select! { biased;
        () = cancel.cancelled() => Err(Error::Cancelled),
        result = timeout_at(deadline, future) => result.map_err(|_| Error::Deadline)?,
    }
}
struct CancelOnDrop(CancellationToken);
impl Drop for CancelOnDrop {
    fn drop(&mut self) {
        self.0.cancel();
    }
}
/// Run a native async callback in a fresh Testcontainers-managed container.
/// The image must already exist. Testcontainers owns startup, removal, Drop,
/// watchdog recovery, and Docker endpoint configuration.
/// No `Send` or `'static` bound is required, including for native file reads.
///
/// # Errors
/// Returns typed infrastructure, callback, and explicit removal failures.
/// Testcontainers' Drop fallback logs failures rather than returning them.
///
/// # Panics
/// Resumes the original callback panic after explicit cleanup. Removal failures
/// during unwinding are reported to stderr without replacing the panic payload.
pub async fn with_environment<F, Fut, T, E>(image: &str, callback: F) -> Result<T, ScopeError<E>>
where
    F: FnOnce(Environment) -> Fut,
    Fut: Future<Output = Result<T, E>>,
{
    valid(image).map_err(ScopeError::Infrastructure)?;
    if image.is_empty() {
        return Err(ScopeError::Infrastructure(Error::InvalidInput(
            "empty image".into(),
        )));
    }
    let deadline = Instant::now() + CASE_BUDGET;
    let cancel = CancellationToken::new();
    let (docker, container) = bounded(deadline, &cancel, async {
        let docker = docker_client_instance()
            .await
            .map_err(TestcontainersError::from)?;
        let id = docker
            .inspect_image(image)
            .await?
            .id
            .ok_or_else(|| Error::Unsupported("image ID absent".into()))?;
        let (name, tag) = id
            .split_once(':')
            .ok_or_else(|| Error::Unsupported("image ID is not a digest".into()))?;
        // Passing the immutable ID avoids a tag changing between inspection and
        // creation. AsyncRunner can still attempt a pull on a create-time 404.
        let container = GenericImage::new(name, tag).start().await?;
        Ok((docker, container))
    })
    .await
    .map_err(ScopeError::Infrastructure)?;
    let id = container.id().to_string();
    let container = Arc::new(RwLock::new(Some(container)));
    let _cancel_on_drop = CancelOnDrop(cancel.clone());
    let env = Environment {
        container: Arc::downgrade(&container),
        docker,
        id: id.clone(),
        deadline,
        cancel: cancel.clone(),
    };
    let outcome = AssertUnwindSafe(async { callback(env).await })
        .catch_unwind()
        .await;
    cancel.cancel();
    let cleanup = cleanup(&container, &id).await;
    match outcome {
        Err(payload) => {
            if let Err(error) = cleanup {
                eprintln!("cli-e2e cleanup failed while unwinding: {error}");
            }
            std::panic::resume_unwind(payload)
        }
        Ok(result) => combine_scope(
            result.map_err(ScopeError::Callback),
            cleanup.err().into_iter().collect(),
        ),
    }
}
async fn cleanup(container: &Container, id: &str) -> Result<(), Error> {
    let action = async {
        // All public operations race cancellation while holding their read lock.
        // A cancelled operation releases that lock. Never wait forever on a task
        // which the caller detached or stopped polling.
        let mut guard = timeout(CLEANUP_LOCK_BUDGET, container.write())
            .await
            .map_err(|_| Error::Deadline)?;
        if let Some(container) = guard.take() {
            container.rm().await?;
        }
        Ok(())
    };
    action.await.map_err(|cause| Error::Cleanup {
        resource: id.into(),
        cause: Box::new(cause),
    })
}
fn combine_scope<T, E>(
    result: Result<T, ScopeError<E>>,
    errors: Vec<Error>,
) -> Result<T, ScopeError<E>> {
    if errors.is_empty() {
        result
    } else {
        Err(ScopeError::Cleanup {
            prior: result.err().map(Box::new),
            errors,
        })
    }
}
#[cfg(test)]
#[path = "lib_test.rs"]
mod tests;
