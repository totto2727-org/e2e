//! Async CLI cases in fresh, locally built Linux containers.
//!
//! ```no_run
//! use cli_e2e::{Case, Command, CommandResult, run_cases};
//! # async fn example() -> Result<(), cli_e2e::Error> {
//! run_cases("e2e-example:local", vec![Case::new("greeting", |env| async move {
//!     env.check_command(Command::new(["echo", "hello"]),
//!         CommandResult { exit_code: 0, stdout: b"hello\n".to_vec(), stderr: vec![] }).await
//! })]).await?;
//! # Ok(()) }
//! ```
use bollard::{
    API_DEFAULT_VERSION, Docker,
    container::LogOutput,
    exec::StartExecResults,
    models::{ContainerCreateBody, ExecConfig},
    query_parameters::*,
};
use futures_util::{FutureExt, StreamExt, future::BoxFuture, stream::FuturesUnordered};
use std::{
    collections::{HashMap, HashSet},
    fmt,
    future::Future,
    io::Read,
    panic::AssertUnwindSafe,
    path::Path,
    sync::{
        Arc,
        atomic::{AtomicU8, Ordering},
    },
    time::Duration,
};
use tokio::{
    sync::Semaphore,
    time::{Instant, timeout, timeout_at},
};
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
    Multiple(Vec<Error>),
    Case {
        name: String,
        cause: Box<Error>,
    },
    Panic,
}
impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{self:?}")
    }
}
impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Docker(e) => Some(e),
            Self::Archive(e) => Some(e),
            Self::Cleanup { cause, .. } | Self::Case { cause, .. } => Some(cause),
            Self::Execution {
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

/// An owned async callback. `new` hides future boxing.
pub struct Case {
    name: String,
    action: Box<dyn FnOnce(Environment) -> BoxFuture<'static, Result<(), Error>> + Send>,
}
impl Case {
    pub fn new<F, Fut>(name: impl Into<String>, action: F) -> Self
    where
        F: FnOnce(Environment) -> Fut + Send + 'static,
        Fut: Future<Output = Result<(), Error>> + Send + 'static,
    {
        Self {
            name: name.into(),
            action: Box::new(move |env| Box::pin(action(env))),
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
    pub fn container_id(&self) -> &str {
        &self.id
    }
    async fn bounded<T>(&self, fut: impl Future<Output = Result<T, Error>>) -> Result<T, Error> {
        tokio::select! { biased; _ = self.cancel.cancelled() => Err(Error::Cancelled),
        result = timeout_at(self.deadline, fut) => result.map_err(|_| Error::Deadline)? }
    }
    /// Execute argv and retain bytes on transport, cancellation or deadline failure.
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
    pub async fn check_command(
        &self,
        command: Command,
        expected: CommandResult,
    ) -> Result<(), Error> {
        compare_command(self.run(command).await?, expected)
    }
    /// Copy exact bytes with mode 0644 into an existing parent directory.
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
    pub async fn check_regular_file(&self, path: &str) -> Result<(), Error> {
        self.physical("-f", path).await
    }
    pub async fn check_directory(&self, path: &str) -> Result<(), Error> {
        self.physical("-d", path).await
    }
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
fn compare_command(actual: CommandResult, expected: CommandResult) -> Result<(), Error> {
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
        timeout(CLEANUP_BUDGET, cleanup)
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
/// Run at most two cases concurrently, resolving only a caller-owned local image.
/// Dropping this future signals cancellation to a supervised cleanup task.
/// Keep the Tokio runtime alive to allow that cleanup to finish.
pub async fn run_cases(image: &str, cases: Vec<Case>) -> Result<(), Error> {
    valid(image)?;
    if image.is_empty() {
        return Err(Error::InvalidInput("empty image".into()));
    }
    let mut names = HashSet::new();
    for case in &cases {
        valid(&case.name)?;
        if case.name.is_empty() || !names.insert(case.name.clone()) {
            return Err(Error::InvalidInput("empty or duplicate case name".into()));
        }
    }
    let cancel = CancellationToken::new();
    let _guard = CancelOnDrop(cancel.clone());
    let image = image.to_string();
    tokio::spawn(async move { supervise(image, cases, cancel).await })
        .await
        .map_err(|_| Error::Panic)?
}
async fn setup<T>(
    cancel: &CancellationToken,
    future: impl Future<Output = Result<T, Error>>,
) -> Result<T, Error> {
    tokio::select! { biased; _ = cancel.cancelled() => Err(Error::Cancelled), result = timeout(CASE_BUDGET, future) => result.map_err(|_| Error::Deadline)? }
}
async fn supervise(
    image: String,
    cases: Vec<Case>,
    cancel: CancellationToken,
) -> Result<(), Error> {
    let docker = setup(&cancel, connect()).await?;
    let inspected = setup(&cancel, async { Ok(docker.inspect_image(&image).await?) }).await?;
    let image = inspected
        .id
        .ok_or_else(|| Error::Unsupported("image ID absent".into()))?;
    let owner = uuid::Uuid::new_v4().to_string();
    let lease = Resource::new(docker.clone(), &owner);
    let mut errors = Vec::new();
    let retained = setup(&cancel, lease.create(&image)).await;
    if let Err(e) = retained {
        errors.push(e);
    } else {
        let slots = Arc::new(Semaphore::new(2));
        let mut pending = FuturesUnordered::new();
        for case in cases {
            let docker = docker.clone();
            let owner = owner.clone();
            let image = image.clone();
            let slots = slots.clone();
            let cancel = cancel.clone();
            pending.push(async move {
                let permit = tokio::select! { _ = cancel.cancelled() => return Err(Error::Cancelled), p = slots.acquire_owned() => p.map_err(|_| Error::Cancelled)? };
                let deadline = Instant::now() + CASE_BUDGET;
                let case_cancel = cancel.child_token();
                let resource = Resource::new(docker.clone(), &owner);
                let action = async {
                    let id = resource.create(&image).await?;
                    docker.start_container(&id, None).await?;
                    Ok(Environment { docker: docker.clone(), id, deadline, cancel: case_cancel.clone() })
                };
                let result = tokio::select! { _ = cancel.cancelled() => Err(Error::Cancelled), r = timeout_at(deadline, action) => r.map_err(|_| Error::Deadline).and_then(|r| r) };
                let result = match result {
                    Err(e) => Err(e), Ok(env) => {
                        tokio::select! { _ = cancel.cancelled() => Err(Error::Cancelled),
                            r = AssertUnwindSafe(async { (case.action)(env).await }).catch_unwind() => r.unwrap_or(Err(Error::Panic)) }
                    }
                };
                case_cancel.cancel();
                let cleanup = resource.cleanup().await; drop(permit);
                combine(result, cleanup).map_err(|cause| Error::Case { name: case.name, cause: Box::new(cause) })
            });
        }
        while let Some(result) = pending.next().await {
            if let Err(e) = result {
                errors.push(e);
            }
        }
    }
    if let Err(e) = lease.cleanup().await {
        errors.push(e);
    }
    if errors.is_empty() {
        Ok(())
    } else {
        Err(Error::Multiple(errors))
    }
}
fn combine(first: Result<(), Error>, cleanup: Result<(), Error>) -> Result<(), Error> {
    match (first, cleanup) {
        (Ok(()), Ok(())) => Ok(()),
        (Err(e), Ok(())) | (Ok(()), Err(e)) => Err(e),
        (Err(e), Err(c)) => Err(Error::Multiple(vec![e, c])),
    }
}
#[cfg(test)]
#[path = "lib_test.rs"]
mod tests;
#[cfg(test)]
#[path = "transport_test.rs"]
mod transport_tests;
