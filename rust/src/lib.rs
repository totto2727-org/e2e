//! Async CLI cases in fresh, locally built Linux containers.
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
use bollard::{
    API_DEFAULT_VERSION, Docker,
    container::LogOutput,
    exec::StartExecResults,
    models::{ContainerCreateBody, ExecConfig},
    query_parameters::{
        CreateContainerOptionsBuilder, DownloadFromContainerOptionsBuilder,
        RemoveContainerOptionsBuilder, UploadToContainerOptionsBuilder,
    },
};
use futures_util::{FutureExt, StreamExt};
use std::{
    collections::HashMap,
    fmt,
    future::Future,
    io::Read,
    panic::AssertUnwindSafe,
    path::Path,
    sync::atomic::{AtomicU8, Ordering},
    time::Duration,
};
use tokio::time::{Instant, timeout_at};
use tokio_util::sync::CancellationToken;

const LABEL: &str = "org.totto2727.e2e.rust.owner";
const CASE_BUDGET: Duration = Duration::from_secs(60);
const CLEANUP_BUDGET: Duration = Duration::from_secs(30);

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
    /// Validate the executable and reject NUL bytes in command fields.
    ///
    /// # Errors
    /// Returns [`Error::InvalidInput`] for an empty executable or NUL input.
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
        Ok(())
    }
}
/// Successful execution, including application failure exit codes.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CommandResult {
    pub exit_code: i64,
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
}
/// Captured bytes on execution failure. No exit code is asserted on errors.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct PartialOutput {
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
}
#[derive(Debug)]
pub enum ExecutionFailure {
    Deadline,
    Cancelled,
    Docker(bollard::errors::Error),
    Protocol(String),
}
/// Typed failures retain cleanup failures alongside the original case error.
#[derive(Debug)]
pub enum Error {
    InvalidInput(String),
    Unsupported(String),
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
            Self::Archive(e) => Some(e),
            Self::Cleanup { cause, .. } => Some(cause),
            Self::Docker(e)
            | Self::Execution {
                failure: ExecutionFailure::Docker(e),
                ..
            } => Some(e),
            _ => None,
        }
    }
}
impl From<bollard::errors::Error> for Error {
    fn from(e: bollard::errors::Error) -> Self {
        Self::Docker(e)
    }
}
impl From<std::io::Error> for Error {
    fn from(e: std::io::Error) -> Self {
        Self::Archive(e)
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
/// Case-scoped operations, with private transport and absolute deadline.
#[derive(Clone)]
pub struct Environment {
    docker: Docker,
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
    async fn bounded<T>(&self, fut: impl Future<Output = Result<T, Error>>) -> Result<T, Error> {
        tokio::select! { biased; () = self.cancel.cancelled() => Err(Error::Cancelled),
        result = timeout_at(self.deadline, fut) => result.map_err(|_| Error::Deadline)? }
    }
    /// Execute argv and retain bytes on transport, cancellation or deadline failure.
    ///
    /// # Errors
    /// Returns an input error for invalid argv and an execution error with partial
    /// output for Docker, protocol, cancellation, or deadline failures.
    pub async fn run(&self, command: Command) -> Result<CommandResult, Error> {
        command.validate()?;
        let mut output = PartialOutput::default();
        let result = self
            .bounded(async {
                let exec = self
                    .docker
                    .create_exec(
                        &self.id,
                        ExecConfig {
                            attach_stdout: Some(true),
                            attach_stderr: Some(true),
                            tty: Some(false),
                            cmd: Some(command.args),
                            working_dir: command.working_dir,
                            env: Some(command.env),
                            ..Default::default()
                        },
                    )
                    .await?;
                match self.docker.start_exec(&exec.id, None).await? {
                    StartExecResults::Attached {
                        output: mut stream,
                        input,
                    } => {
                        drop(input);
                        while let Some(chunk) = stream.next().await {
                            append_output(&mut output, chunk?)?;
                        }
                    }
                    StartExecResults::Detached => {
                        return Err(Error::Unsupported("detached exec response".into()));
                    }
                }
                loop {
                    let state = self.docker.inspect_exec(&exec.id).await?;
                    match state.running {
                        Some(false) => {
                            break state.exit_code.ok_or_else(|| {
                                Error::Unsupported("missing exec exit code".into())
                            });
                        }
                        Some(true) => tokio::time::sleep(Duration::from_millis(10)).await,
                        None => {
                            return Err(Error::Unsupported("missing exec running state".into()));
                        }
                    }
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
                    Error::Docker(e) => ExecutionFailure::Docker(e),
                    e => ExecutionFailure::Protocol(e.to_string()),
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
    /// Returns errors for invalid paths, archive construction, Docker transport,
    /// cancellation, or deadline expiry. The parent directory must already exist.
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
            self.docker
                .upload_to_container(
                    &self.id,
                    Some(
                        UploadToContainerOptionsBuilder::default()
                            .path(&parent.to_string_lossy())
                            .build(),
                    ),
                    bollard::body_full(bytes::Bytes::from(archive)),
                )
                .await?;
            Ok(())
        })
        .await
    }
    /// Read exact bytes from a single regular-file Docker archive entry.
    ///
    /// # Errors
    /// Returns errors for invalid paths or archives, Docker transport, cancellation,
    /// or deadline expiry.
    pub async fn read_file(&self, path: &str) -> Result<Vec<u8>, Error> {
        path_valid(path)?;
        self.bounded(async {
            let mut stream = self.docker.download_from_container(
                &self.id,
                Some(
                    DownloadFromContainerOptionsBuilder::default()
                        .path(path)
                        .build(),
                ),
            );
            let mut bytes = Vec::new();
            while let Some(chunk) = stream.next().await {
                bytes.extend_from_slice(&chunk?);
            }
            read_archive(&bytes)
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
fn append_output(output: &mut PartialOutput, chunk: LogOutput) -> Result<(), Error> {
    match chunk {
        LogOutput::StdOut { message } => output.stdout.extend_from_slice(&message),
        LogOutput::StdErr { message } => output.stderr.extend_from_slice(&message),
        _ => return Err(Error::Unsupported("unexpected exec stream channel".into())),
    }
    Ok(())
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
fn read_archive(bytes: &[u8]) -> Result<Vec<u8>, Error> {
    let mut archive = tar::Archive::new(bytes);
    let mut entries = archive.entries()?;
    let mut entry = entries
        .next()
        .ok_or_else(|| Error::Assertion("empty archive".into()))??;
    if !entry.header().entry_type().is_file() {
        return Err(Error::Assertion(
            "archive entry is not a regular file".into(),
        ));
    }
    let mut result = Vec::new();
    entry.read_to_end(&mut result)?;
    drop(entry);
    if entries.next().is_some() {
        return Err(Error::Assertion("archive contains multiple entries".into()));
    }
    Ok(result)
}

async fn connect() -> Result<Docker, Error> {
    if std::env::var_os("DOCKER_API_VERSION").is_some() {
        return Err(Error::Unsupported(
            "DOCKER_API_VERSION is not supported; unset it to negotiate".into(),
        ));
    }
    if std::env::var("DOCKER_TLS_VERIFY").is_ok_and(|v| !v.is_empty() && v != "0")
        || std::env::var_os("DOCKER_CERT_PATH").is_some()
    {
        return Err(Error::Unsupported(
            "TLS Docker endpoints are not supported".into(),
        ));
    }
    let host =
        std::env::var("DOCKER_HOST").unwrap_or_else(|_| "unix:///var/run/docker.sock".into());
    valid(&host)?;
    let docker = if let Some(path) = host.strip_prefix("unix://") {
        Docker::connect_with_unix(path, 60, API_DEFAULT_VERSION)?
    } else if host.starts_with("http://") || host.starts_with("tcp://") {
        Docker::connect_with_http(&host, 60, API_DEFAULT_VERSION)?
    } else {
        return Err(Error::Unsupported(format!(
            "unsupported Docker endpoint {host:?}"
        )));
    };
    Ok(docker.negotiate_version().await?)
}
struct Resource {
    docker: Docker,
    name: String,
    owner: String,
    creation: AtomicU8,
}
impl Resource {
    fn new(docker: Docker, owner: &str) -> Self {
        Self {
            docker,
            name: format!("cli-e2e-rust-{}", uuid::Uuid::new_v4()),
            owner: owner.into(),
            creation: AtomicU8::new(0),
        }
    }
    async fn create(&self, image: &str) -> Result<String, Error> {
        let body = ContainerCreateBody {
            image: Some(image.into()),
            labels: Some(HashMap::from([(LABEL.into(), self.owner.clone())])),
            ..Default::default()
        };
        self.creation.store(1, Ordering::SeqCst);
        let result = self
            .docker
            .create_container(
                Some(
                    CreateContainerOptionsBuilder::default()
                        .name(&self.name)
                        .build(),
                ),
                body,
            )
            .await;
        if result.is_ok()
            || matches!(
                result,
                Err(bollard::errors::Error::DockerResponseServerError {
                    status_code: 400..=499,
                    ..
                })
            )
        {
            self.creation.store(2, Ordering::SeqCst);
        }
        Ok(result?.id)
    }
    async fn cleanup(&self) -> Result<(), Error> {
        let deadline = Instant::now() + CLEANUP_BUDGET;
        if self.creation.load(Ordering::SeqCst) == 0 {
            return Ok(());
        }
        let mut last_error = None;
        let cleanup = async {
            loop {
                match self.docker.inspect_container(&self.name, None).await {
                    Err(bollard::errors::Error::DockerResponseServerError {
                        status_code: 404,
                        ..
                    }) if self.creation.load(Ordering::SeqCst) == 2 => return Ok(()),
                    Err(e) => {
                        last_error = Some(Error::Docker(e));
                    }
                    Ok(info) => {
                        if info
                            .config
                            .and_then(|c| c.labels)
                            .and_then(|l| l.get(LABEL).cloned())
                            .as_deref()
                            != Some(&self.owner)
                        {
                            return Err(Error::Unsupported(
                                "refusing cleanup of foreign resource".into(),
                            ));
                        }
                        let id = info.id.ok_or_else(|| {
                            Error::Unsupported("cleanup inspect missing container ID".into())
                        })?;
                        match self
                            .docker
                            .remove_container(
                                &id,
                                Some(
                                    RemoveContainerOptionsBuilder::default()
                                        .force(true)
                                        .v(true)
                                        .build(),
                                ),
                            )
                            .await
                        {
                            Ok(())
                            | Err(bollard::errors::Error::DockerResponseServerError {
                                status_code: 404,
                                ..
                            }) => return Ok(()),
                            Err(e) => {
                                last_error = Some(Error::Docker(e));
                            }
                        }
                    }
                }
                tokio::time::sleep(Duration::from_millis(100)).await;
            }
        };
        timeout_at(deadline, cleanup)
            .await
            .unwrap_or_else(|_| Err(last_error.unwrap_or(Error::Deadline)))
            .map_err(|cause| Error::Cleanup {
                resource: self.name.clone(),
                cause: Box::new(cause),
            })
    }
}
struct CancelOnDrop(CancellationToken);
impl Drop for CancelOnDrop {
    fn drop(&mut self) {
        self.0.cancel();
    }
}
/// Run one caller callback in a fresh container leased from a local image.
/// Native test runners control case names and concurrency. Dropping this future
/// cancels its supervised task, which retains ownership through bounded cleanup.
/// Keep the Tokio runtime alive until that background cleanup finishes.
///
/// # Errors
/// Returns typed infrastructure, callback, and cleanup failures without erasing
/// the callback's error type.
///
/// # Panics
/// Callback panics are resumed with their original payload after cleanup.
/// Cleanup failures during a panic are written to stderr before resuming it.
pub async fn with_environment<F, Fut, T, E>(image: &str, callback: F) -> Result<T, ScopeError<E>>
where
    F: FnOnce(Environment) -> Fut + Send + 'static,
    Fut: Future<Output = Result<T, E>> + Send + 'static,
    T: Send + 'static,
    E: Send + 'static,
{
    let deadline = Instant::now() + CASE_BUDGET;
    valid(image).map_err(ScopeError::Infrastructure)?;
    if image.is_empty() {
        return Err(ScopeError::Infrastructure(Error::InvalidInput(
            "empty image".into(),
        )));
    }
    let cancel = CancellationToken::new();
    let image = image.to_string();
    let scope_cancel = cancel.clone();
    owned_scope(
        async move { supervise(image, callback, scope_cancel, deadline).await },
        cancel,
    )
    .await
}
async fn owned_scope<T: Send + 'static, E: Send + 'static>(
    future: impl Future<Output = Outcome<T, E>> + Send + 'static,
    cancel: CancellationToken,
) -> Result<T, ScopeError<E>> {
    let _guard = CancelOnDrop(cancel);
    let task = tokio::spawn(future);
    match task.await {
        Ok(outcome) => finish(outcome),
        Err(error) if error.is_panic() => std::panic::resume_unwind(error.into_panic()),
        Err(_) => Err(ScopeError::Infrastructure(Error::Cancelled)),
    }
}
type PanicPayload = Box<dyn std::any::Any + Send>;
type Outcome<T, E> = Result<Result<T, ScopeError<E>>, PanicPayload>;
fn finish<T, E>(outcome: Outcome<T, E>) -> Result<T, ScopeError<E>> {
    match outcome {
        Ok(result) => result,
        Err(payload) => std::panic::resume_unwind(payload),
    }
}
async fn setup<T>(
    cancel: &CancellationToken,
    deadline: Instant,
    future: impl Future<Output = Result<T, Error>>,
) -> Result<T, Error> {
    tokio::select! { biased; () = cancel.cancelled() => Err(Error::Cancelled), result = timeout_at(deadline, future) => result.map_err(|_| Error::Deadline)? }
}
async fn supervise<F, Fut, T, E>(
    image: String,
    callback: F,
    cancel: CancellationToken,
    deadline: Instant,
) -> Outcome<T, E>
where
    F: FnOnce(Environment) -> Fut,
    Fut: Future<Output = Result<T, E>>,
{
    let docker = match setup(&cancel, deadline, connect()).await {
        Ok(docker) => docker,
        Err(error) => return Ok(Err(ScopeError::Infrastructure(error))),
    };
    supervise_with_docker(docker, image, callback, cancel, deadline).await
}
async fn supervise_with_docker<F, Fut, T, E>(
    docker: Docker,
    image: String,
    callback: F,
    cancel: CancellationToken,
    deadline: Instant,
) -> Outcome<T, E>
where
    F: FnOnce(Environment) -> Fut,
    Fut: Future<Output = Result<T, E>>,
{
    let image = setup(&cancel, deadline, async {
        docker
            .inspect_image(&image)
            .await?
            .id
            .ok_or_else(|| Error::Unsupported("image ID absent".into()))
    })
    .await;
    let image = match image {
        Ok(image) => image,
        Err(error) => return Ok(Err(ScopeError::Infrastructure(error))),
    };
    let owner = uuid::Uuid::new_v4().to_string();
    let lease = Resource::new(docker.clone(), &owner);
    let resource = Resource::new(docker.clone(), &owner);
    let case_cancel = cancel.child_token();
    let action = async {
        setup(&cancel, deadline, lease.create(&image))
            .await
            .map_err(ScopeError::Infrastructure)?;
        let env = tokio::select! {
            biased;
            () = cancel.cancelled() => Err(Error::Cancelled),
            result = timeout_at(deadline, async {
                let id = resource.create(&image).await?;
                docker.start_container(&id, None).await?;
                Ok(Environment { docker: docker.clone(), id, deadline, cancel: case_cancel.clone() })
            }) => result.map_err(|_| Error::Deadline).and_then(|result| result),
        }.map_err(ScopeError::Infrastructure)?;
        // Do not race a timer against env.run: it must publish its partial output.
        // The operation budget cannot preempt arbitrary caller code.
        tokio::select! {
            biased;
            () = cancel.cancelled() => Err(ScopeError::Infrastructure(Error::Cancelled)),
            result = async { callback(env).await } => result.map_err(ScopeError::Callback),
        }
    };
    let outcome = AssertUnwindSafe(action).catch_unwind().await;
    case_cancel.cancel();
    let mut errors = Vec::new();
    for result in [resource.cleanup().await, lease.cleanup().await] {
        if let Err(error) = result {
            errors.push(error);
        }
    }
    match outcome {
        Err(payload) => {
            if !errors.is_empty() {
                eprintln!("cli-e2e cleanup failed while unwinding: {errors:?}");
            }
            Err(payload)
        }
        Ok(result) => Ok(combine_scope(result, errors)),
    }
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
#[cfg(test)]
#[path = "transport_test.rs"]
mod transport_tests;
