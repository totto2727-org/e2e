use crate::{Command, Environment, Error, ScopeError, combine_scope, driver};
use futures_util::FutureExt;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    future::Future,
    panic::AssertUnwindSafe,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};
use tokio::sync::Mutex;

/// A normalized Linux termios value. PENDIN is excluded by the driver.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize)]
pub struct TerminalSettings(
    pub u64,
    pub u64,
    pub u64,
    pub u64,
    pub u64,
    pub u64,
    pub Vec<u8>,
);

#[derive(Clone, Debug, PartialEq, Eq, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum TerminalStatus {
    Running,
    Exited {
        #[serde(deserialize_with = "exit_code")]
        code: i64,
    },
    Signaled {
        #[serde(deserialize_with = "signal_number")]
        signal: u32,
    },
}

fn exit_code<'de, D: serde::Deserializer<'de>>(decoder: D) -> Result<i64, D::Error> {
    let code = i64::deserialize(decoder)?;
    if code < 0 {
        Err(serde::de::Error::custom("exit code must be nonnegative"))
    } else {
        Ok(code)
    }
}
fn signal_number<'de, D: serde::Deserializer<'de>>(decoder: D) -> Result<u32, D::Error> {
    std::num::NonZeroU32::deserialize(decoder).map(std::num::NonZeroU32::get)
}

/// Cumulative raw channels and process observations. No cross-channel ordering is implied.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct TerminalObservation {
    pub stdout: Vec<u8>,
    pub terminal: Vec<u8>,
    pub unread_input: Vec<u8>,
    pub status: TerminalStatus,
    pub termios_restored: bool,
    pub initial_termios: TerminalSettings,
    pub current_termios: TerminalSettings,
}

/// A driver error may retain full observations or just bytes at the watchdog boundary.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize)]
#[serde(untagged)]
pub enum TerminalFailureObservation {
    Complete(TerminalObservation),
    Partial(TerminalOutput),
}

#[derive(Clone, Debug, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TerminalOutput {
    pub stdout: Vec<u8>,
    pub terminal: Vec<u8>,
}

#[derive(Clone, Copy, Debug, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum TerminalChannel {
    Stdout,
    Terminal,
}

#[derive(Clone, Copy, Debug, Serialize)]
pub enum TerminalSignal {
    #[serde(rename = "SIGINT")]
    Interrupt,
    #[serde(rename = "SIGTERM")]
    Terminate,
    #[serde(rename = "SIGHUP")]
    Hangup,
    #[serde(rename = "SIGQUIT")]
    Quit,
    #[serde(rename = "SIGKILL")]
    Kill,
    #[serde(rename = "SIGUSR1")]
    User1,
    #[serde(rename = "SIGUSR2")]
    User2,
}

/// `input: None` attaches stdin to the PTY. `Some(bytes)` supplies a finite pipe plus EOF.
#[derive(Clone, Debug)]
pub struct TerminalOptions {
    pub rows: u16,
    pub columns: u16,
    pub input: Option<Vec<u8>>,
    pub timeout: Duration,
}
impl Default for TerminalOptions {
    fn default() -> Self {
        Self {
            rows: 40,
            columns: 120,
            input: None,
            timeout: Duration::from_secs(50),
        }
    }
}
fn duration(value: Duration) -> Result<f64, Error> {
    if value.is_zero() || value > Duration::from_secs(60) {
        Err(Error::InvalidInput(
            "terminal timeout must be in (0, 60] seconds".into(),
        ))
    } else {
        Ok(value.as_secs_f64())
    }
}
fn geometry(rows: u16, columns: u16) -> Result<(), Error> {
    if rows == 0 || columns == 0 {
        Err(Error::InvalidInput(
            "terminal dimensions must be nonzero".into(),
        ))
    } else {
        Ok(())
    }
}

struct Session {
    env: Environment,
    path: String,
    closed: AtomicBool,
    cancel: tokio_util::sync::CancellationToken,
    lock: Mutex<()>,
}
/// A scope-bound terminal. Escaped clones are closed when its callback completes.
#[derive(Clone)]
pub struct TerminalSession(Arc<Session>);
impl TerminalSession {
    async fn request(&self, mut request: Value) -> Result<TerminalObservation, Error> {
        crate::bounded(self.0.env.deadline, &self.0.cancel, async {
            let _lock = self.0.lock.lock().await;
            if self.0.closed.load(Ordering::Acquire) {
                return Err(Error::Cancelled);
            }
            request["path"] = json!(self.0.path);
            Box::pin(driver::invoke(&self.0.env, "request", &request, None)).await
        })
        .await
    }
    /// Read cumulative raw output without waiting for process exit.
    /// # Errors
    /// Returns scope cancellation, driver, transport, or deadline errors.
    pub async fn observe(&self) -> Result<TerminalObservation, Error> {
        self.request(json!({"operation":"observe"})).await
    }
    /// Match exact nonempty bytes, consuming through the match and retaining trailing bytes.
    /// # Errors
    /// Returns invalid-input, mismatch, timeout, cancellation, or infrastructure errors.
    pub async fn expect(
        &self,
        channel: TerminalChannel,
        bytes: &[u8],
        timeout: Duration,
    ) -> Result<TerminalObservation, Error> {
        if bytes.is_empty() {
            return Err(Error::InvalidInput(
                "expected bytes must be nonempty".into(),
            ));
        }
        let timeout = duration(timeout)?;
        self.request(
            json!({"operation":"expect", "channel":channel, "data":bytes, "timeout":timeout}),
        )
        .await
    }
    /// Send literal terminal input. Control characters are not signal aliases.
    /// # Errors
    /// Returns a driver error for pipe stdin or exited children, or infrastructure errors.
    pub async fn send(&self, bytes: &[u8]) -> Result<TerminalObservation, Error> {
        self.request(json!({"operation":"send", "data":bytes}))
            .await
    }
    /// Change terminal dimensions and notify the application process group with SIGWINCH.
    /// # Errors
    /// Returns invalid-input, driver, cancellation, or infrastructure errors.
    pub async fn resize(&self, rows: u16, columns: u16) -> Result<TerminalObservation, Error> {
        geometry(rows, columns)?;
        self.request(json!({"operation":"resize", "rows":rows, "columns":columns}))
            .await
    }
    /// Send a named signal to the application process group, separately from literal input.
    /// # Errors
    /// Returns driver, cancellation, or infrastructure errors.
    pub async fn signal(&self, signal: TerminalSignal) -> Result<TerminalObservation, Error> {
        self.request(json!({"operation":"signal", "name":signal}))
            .await
    }
    /// Wait for child exit and drain raw output. Nonzero exits are observations, not errors.
    /// # Errors
    /// Returns invalid-input, timeout, cancellation, driver, or infrastructure errors.
    pub async fn wait(&self, timeout: Duration) -> Result<TerminalObservation, Error> {
        let timeout = duration(timeout)?;
        self.request(json!({"operation":"wait", "timeout":timeout}))
            .await
    }
}

async fn close(session: &Session) -> Result<(), Error> {
    let mut cleanup_env = session.env.clone();
    cleanup_env.deadline = tokio::time::Instant::now() + Duration::from_secs(30);
    cleanup_env.cancel = tokio_util::sync::CancellationToken::new();
    let _lock = cleanup_env
        .bounded(async { Ok(session.lock.lock().await) })
        .await?;
    driver::invoke(
        &cleanup_env,
        "request",
        &json!({"path":session.path,"operation":"close"}),
        None,
    )
    .await
}
struct SessionGuard {
    session: Arc<Session>,
    armed: bool,
}
impl Drop for SessionGuard {
    fn drop(&mut self) {
        self.session.closed.store(true, Ordering::Release);
        self.session.cancel.cancel();
        if self.armed {
            let session = self.session.clone();
            if let Ok(runtime) = tokio::runtime::Handle::try_current() {
                // Native exec inspection borrows a non-Sync stream, so cleanup is not Send.
                let runtime_for_cleanup = runtime.clone();
                runtime.spawn_blocking(move || {
                    if let Err(error) = runtime_for_cleanup.block_on(close(&session)) {
                        eprintln!("caseyard terminal drop cleanup failed: {error}");
                    }
                });
            }
        }
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Started {
    path: String,
}

impl Environment {
    /// Run a scoped terminal callback with stdin/stderr on a PTY and separate piped stdout.
    /// A caller-installed static Linux driver is required. Callback completion closes escaped handles.
    /// # Errors
    /// Preserves concrete callback errors and accompanying close errors in `ScopeError`.
    /// # Panics
    /// Resumes callback panics after closing the terminal.
    pub async fn with_terminal<F, Fut, T, E>(
        &self,
        command: Command,
        options: TerminalOptions,
        callback: F,
    ) -> Result<T, ScopeError<E>>
    where
        F: FnOnce(TerminalSession) -> Fut,
        Fut: Future<Output = Result<T, E>>,
    {
        command.validate().map_err(ScopeError::Infrastructure)?;
        geometry(options.rows, options.columns).map_err(ScopeError::Infrastructure)?;
        let timeout = duration(options.timeout).map_err(ScopeError::Infrastructure)?;
        let started: Started = driver::invoke(self, "start", &json!({"argv":command.args,"rows":options.rows,"columns":options.columns,"input":options.input,"timeout":timeout}), Some(&command)).await.map_err(ScopeError::Infrastructure)?;
        crate::path_valid(&started.path).map_err(ScopeError::Infrastructure)?;
        let session = Arc::new(Session {
            env: self.clone(),
            path: started.path,
            closed: AtomicBool::new(false),
            cancel: self.cancel.child_token(),
            lock: Mutex::new(()),
        });
        let mut guard = SessionGuard {
            session: session.clone(),
            armed: true,
        };
        let outcome = AssertUnwindSafe(async { callback(TerminalSession(session.clone())).await })
            .catch_unwind()
            .await;
        session.closed.store(true, Ordering::Release);
        session.cancel.cancel();
        let cleanup = close(&session).await;
        guard.armed = false;
        match outcome {
            Err(payload) => {
                if let Err(error) = cleanup {
                    eprintln!("caseyard terminal cleanup failed while unwinding: {error}");
                }
                std::panic::resume_unwind(payload)
            }
            Ok(result) => combine_scope(
                result.map_err(ScopeError::Callback),
                cleanup.err().into_iter().collect(),
            ),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn geometry_and_duration_have_strict_boundaries() {
        assert!(geometry(0, 80).is_err());
        assert!(geometry(40, 0).is_err());
        assert!(geometry(u16::MAX, u16::MAX).is_ok());
        assert!(duration(Duration::ZERO).is_err());
        assert!(duration(Duration::from_secs(61)).is_err());
        assert_eq!(duration(Duration::from_secs(60)).ok(), Some(60.0));
    }

    #[tokio::test]
    async fn invalid_terminal_options_fail_before_driver_or_docker() -> Result<(), Error> {
        let env = crate::tests::disconnected_environment()?;
        for options in [
            TerminalOptions {
                rows: 0,
                ..Default::default()
            },
            TerminalOptions {
                columns: 0,
                ..Default::default()
            },
            TerminalOptions {
                timeout: Duration::ZERO,
                ..Default::default()
            },
        ] {
            assert!(matches!(
                env.with_terminal(Command::new(["true"]), options, |_| async {
                    Ok::<_, Error>(())
                })
                .await,
                Err(ScopeError::Infrastructure(Error::InvalidInput(_)))
            ));
        }
        Ok(())
    }

    #[tokio::test]
    async fn closed_handles_cannot_reach_driver_and_drop_closes_escaped_clones() -> Result<(), Error>
    {
        let env = crate::tests::disconnected_environment()?;
        let session = Arc::new(Session {
            env,
            path: "/private/socket".into(),
            closed: AtomicBool::new(false),
            cancel: tokio_util::sync::CancellationToken::new(),
            lock: Mutex::new(()),
        });
        let handle = TerminalSession(session.clone());
        drop(SessionGuard {
            session,
            armed: false,
        });
        assert!(matches!(handle.observe().await, Err(Error::Cancelled)));
        assert!(matches!(
            handle.send(b"literal\x03\x04").await,
            Err(Error::Cancelled)
        ));
        assert!(matches!(
            handle
                .expect(TerminalChannel::Stdout, b"", Duration::from_secs(1))
                .await,
            Err(Error::InvalidInput(_))
        ));
        Ok(())
    }

    #[test]
    fn raw_observations_and_normalized_termios_are_structural() -> Result<(), serde_json::Error> {
        let observed: TerminalObservation = serde_json::from_value(json!({
            "stdout":[0,255], "terminal":[27,91,72], "unreadInput":[4],
            "status":{"kind":"exited","code":7}, "termiosRestored":true,
            "initialTermios":[1,2,3,4,5,6,[0,255]], "currentTermios":[1,2,3,4,5,6,[0,255]]
        }))?;
        assert_eq!(observed.stdout, [0, 255]);
        assert_eq!(observed.terminal, [27, 91, 72]);
        assert_eq!(observed.unread_input, [4]);
        assert_eq!(observed.status, TerminalStatus::Exited { code: 7 });
        assert_eq!(observed.initial_termios, observed.current_termios);
        assert!(observed.termios_restored);
        for invalid in [json!([256]), json!([-1]), json!([1.5]), json!([true])] {
            assert!(serde_json::from_value::<Vec<u8>>(invalid).is_err());
        }
        for invalid in [
            json!({"kind":"exited","code":-1}),
            json!({"kind":"signaled","signal":0}),
        ] {
            assert!(serde_json::from_value::<TerminalStatus>(invalid).is_err());
        }
        Ok(())
    }

    #[tokio::test]
    async fn session_cancellation_releases_queued_operations_without_docker() -> Result<(), Error> {
        let env = crate::tests::disconnected_environment()?;
        let session = Arc::new(Session {
            cancel: env.cancel.child_token(),
            env,
            path: "/private/socket".into(),
            closed: AtomicBool::new(false),
            lock: Mutex::new(()),
        });
        let _held = session.lock.lock().await;
        let handle = TerminalSession(session.clone());
        let operation = handle.observe();
        tokio::pin!(operation);
        assert!(futures_util::poll!(operation.as_mut()).is_pending());
        session.cancel.cancel();
        assert!(matches!(operation.await, Err(Error::Cancelled)));
        Ok(())
    }

    #[tokio::test]
    async fn cleanup_attempt_has_fresh_budget_after_case_cancellation() -> Result<(), Error> {
        let mut env = crate::tests::disconnected_environment()?;
        env.deadline = tokio::time::Instant::now() - Duration::from_secs(1);
        env.cancel.cancel();
        let session = Session {
            cancel: env.cancel.child_token(),
            env,
            path: "/private/socket".into(),
            closed: AtomicBool::new(true),
            lock: Mutex::new(()),
        };
        // Reaching capability validation proves neither the old deadline nor cancellation skipped cleanup.
        assert!(matches!(close(&session).await, Err(Error::InvalidInput(_))));
        Ok(())
    }
}
