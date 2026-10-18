use caseyard::{
    Command, CommandResult, Error, ExecutionFailure, PathKind, ScopeError, with_environment,
};
use std::time::{Duration, Instant};

const IMAGE: &str = "e2e-example:local";

async fn install_test_driver(env: &caseyard::Environment) -> Result<(), Error> {
    let architecture = env.run(Command::new(["uname", "-m"])).await?;
    let name = match architecture.stdout.as_slice() {
        b"aarch64\n" => "driver-linux-arm64",
        b"x86_64\n" => "driver-linux-amd64",
        other => {
            return Err(Error::Unsupported(format!(
                "container architecture: {other:?}"
            )));
        }
    };
    let bytes = std::fs::read(
        std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../go/target")
            .join(name),
    )?;
    env.install_driver(&bytes).await
}

fn expected(exit_code: i64, stdout: &str, stderr: &str) -> CommandResult {
    CommandResult {
        exit_code,
        stdout: stdout.as_bytes().to_vec(),
        stderr: stderr.as_bytes().to_vec(),
    }
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local, matching static driver and Docker"]
async fn filesystem_snapshots() -> Result<(), ScopeError<Error>> {
    use caseyard::PathState;
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env).await?;
        env.write_file("/workspace/binary", &[0, 255, 10]).await?;
        assert_eq!(env.run(Command::new(["sh", "-c", "mkdir /workspace/directory; ln -s binary /workspace/link; ln -s 'missing target' /workspace/dangling"])).await?, expected(0, "", ""));
        let paths = ["/workspace/missing", "/workspace/link", "/workspace/directory", "/workspace/dangling", "/workspace/binary"];
        let baseline = env.snapshot(paths).await?;
        assert_eq!(baseline.entries["/workspace/binary"], PathState::RegularFile { content: vec![0, 255, 10] });
        assert_eq!(baseline.entries["/workspace/directory"], PathState::Directory);
        assert_eq!(baseline.entries["/workspace/missing"], PathState::Missing);
        assert_eq!(baseline.entries["/workspace/link"], PathState::Symlink { target: b"binary".to_vec() });
        assert_eq!(baseline.entries["/workspace/dangling"], PathState::Symlink { target: b"missing target".to_vec() });
        assert_eq!(env.run(Command::new(["sh", "-c", "touch /workspace/directory/unselected; chmod 0600 /workspace/binary"])).await?, expected(0, "", ""));
        assert_eq!(env.snapshot(baseline.entries.keys().cloned()).await?, baseline);
        env.write_file("/workspace/binary", &[255, 0]).await?;
        assert_eq!(env.run(Command::new(["sh", "-c", "rm /workspace/directory/unselected; rmdir /workspace/directory; touch /workspace/directory /workspace/missing; ln -snf changed /workspace/dangling"])).await?, expected(0, "", ""));
        let after = env.snapshot(paths).await?;
        let mut expected_after = baseline.clone();
        expected_after.entries.insert("/workspace/binary".into(), PathState::RegularFile { content: vec![255, 0] });
        expected_after.entries.insert("/workspace/dangling".into(), PathState::Symlink { target: b"changed".to_vec() });
        expected_after.entries.insert("/workspace/directory".into(), PathState::RegularFile { content: vec![] });
        expected_after.entries.insert("/workspace/missing".into(), PathState::RegularFile { content: vec![] });
        assert_eq!(after, expected_after);
        assert_ne!(after, baseline);
        assert_eq!(baseline.entries["/workspace/binary"], PathState::RegularFile { content: vec![0, 255, 10] });
        assert_eq!(env.run(Command::new(["mkfifo", "/workspace/fifo"])).await?, expected(0, "", ""));
        assert!(matches!(env.snapshot(["/workspace/fifo"]).await, Err(Error::Driver { .. })));
        // A non-directory parent is an inspection error, not a missing child.
        assert!(matches!(env.snapshot(["/workspace/binary/child"]).await, Err(Error::Driver { .. })));
        Ok(())
    }).await
}

#[tokio::test]
#[ignore = "requires caller-built image, matching static driver and Docker"]
async fn filesystem_snapshot_literal_paths_deletion_and_json_bytes() -> Result<(), ScopeError<Error>>
{
    use caseyard::PathState;
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env).await?;
        let before_bytes = b"{\n  \"a\": 1\n}\n";
        let after_bytes = b"{\"a\":1}";
        let before_json: serde_json::Value = serde_json::from_slice(before_bytes)
            .map_err(|error| Error::InvalidInput(error.to_string()))?;
        let after_json: serde_json::Value = serde_json::from_slice(after_bytes)
            .map_err(|error| Error::InvalidInput(error.to_string()))?;
        assert_eq!(before_json, after_json);
        env.write_file("/workspace/file with spaces", before_bytes)
            .await?;
        env.write_file("/workspace/delete me", b"removed").await?;
        env.write_file("/workspace/empty", b"").await?;
        assert_eq!(
            env.run(Command::new([
                "ln",
                "-s",
                "file with spaces",
                "/workspace/link"
            ]))
            .await?,
            CommandResult::default()
        );
        let paths = [
            "/workspace/file with spaces",
            "/workspace/delete me",
            "/workspace/empty",
            "/workspace/link",
        ];
        let baseline = env.snapshot(paths).await?;
        assert_eq!(
            baseline.entries["/workspace/empty"],
            PathState::RegularFile { content: vec![] }
        );
        env.write_file("/workspace/file with spaces", after_bytes)
            .await?;
        assert_eq!(
            env.run(Command::new(["rm", "--", "/workspace/delete me"]))
                .await?,
            CommandResult::default()
        );
        assert_eq!(
            env.run(Command::new([
                "ln",
                "-snf",
                "./file with spaces",
                "/workspace/link"
            ]))
            .await?,
            CommandResult::default()
        );
        assert_eq!(
            env.run(Command::new(["cat", "/workspace/link"])).await?,
            CommandResult {
                stdout: after_bytes.to_vec(),
                ..Default::default()
            }
        );
        let after = env.snapshot(paths).await?;
        assert_eq!(after.entries["/workspace/delete me"], PathState::Missing);
        assert_eq!(
            after.entries["/workspace/link"],
            PathState::Symlink {
                target: b"./file with spaces".to_vec()
            }
        );
        let mut expected_after = baseline.clone();
        expected_after
            .entries
            .insert("/workspace/delete me".into(), PathState::Missing);
        expected_after.entries.insert(
            "/workspace/file with spaces".into(),
            PathState::RegularFile {
                content: after_bytes.to_vec(),
            },
        );
        expected_after.entries.insert(
            "/workspace/link".into(),
            PathState::Symlink {
                target: b"./file with spaces".to_vec(),
            },
        );
        assert_eq!(after, expected_after);
        assert_eq!(
            baseline.entries["/workspace/file with spaces"],
            PathState::RegularFile {
                content: before_bytes.to_vec()
            }
        );
        Ok(())
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built image, matching static driver and Docker"]
async fn terminal_raw_bytes_expect_resize_and_restoration() -> Result<(), Box<dyn std::error::Error>>
{
    use caseyard::{TerminalChannel, TerminalOptions, TerminalStatus};
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env).await.map_err(ScopeError::Infrastructure)?;
        env.write_file("/workspace/unchanged", b"baseline").await.map_err(ScopeError::Infrastructure)?;
        let checks = env.clone();
        let (escaped, observed) = env.with_terminal(
            Command { working_dir: Some("/workspace".into()), env: vec!["CASEYARD_VALUE=literal value".into()], ..Command::new(["sh", "-c", "saved=$(stty -g < /dev/tty); stty raw -echo < /dev/tty; printf '\\033[2Jready' >&2; printf '%s:%s:' \"$PWD\" \"$CASEYARD_VALUE\"; dd bs=1 count=2 2>/dev/null; stty \"$saved\" < /dev/tty; printf '\\033[0m' >&2; exit 7"]) },
            TerminalOptions::default(),
            |session| async move {
                session.expect(TerminalChannel::Terminal, b"ready", Duration::from_secs(5)).await?;
                session.expect(TerminalChannel::Stdout, b"/workspace:literal value:", Duration::from_secs(5)).await?;
                session.resize(24, 80).await?;
                assert_eq!(checks.read_file("/workspace/unchanged").await?, b"baseline");
                session.send(&[3,4]).await?;
                session.expect(TerminalChannel::Stdout, &[3], Duration::from_secs(5)).await?;
                session.expect(TerminalChannel::Stdout, &[4], Duration::from_secs(5)).await?;
                let observed = session.wait(Duration::from_secs(5)).await?;
                Ok::<_, Error>((session, observed))
            },
        ).await?;
        assert_eq!(observed.status, TerminalStatus::Exited { code: 7 });
        assert_eq!(observed.stdout, b"/workspace:literal value:\x03\x04");
        assert_eq!(observed.terminal, b"\x1b[2Jready\x1b[0m");
        assert!(observed.termios_restored);
        assert_eq!(observed.initial_termios, observed.current_termios);
        assert!(matches!(escaped.observe().await, Err(Error::Cancelled)));
        Ok::<_, ScopeError<Error>>(())
    }).await?;
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built image, matching static driver and Docker"]
async fn terminal_key_bytes_context_sigint_and_cancel_snapshot()
-> Result<(), Box<dyn std::error::Error>> {
    use caseyard::{TerminalChannel, TerminalOptions, TerminalSignal, TerminalStatus};
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env).await.map_err(ScopeError::Infrastructure)?;
        env.write_file("/workspace/unchanged", &[0,255,10]).await.map_err(ScopeError::Infrastructure)?;
        let baseline = env.snapshot(["/workspace/unchanged", "/workspace/not-created"]).await.map_err(ScopeError::Infrastructure)?;
        let keys = b"\x1b[A\x1b[B \r\x1b\x04\x03";
        assert_eq!(keys.len(), 11);
        let observed = env.with_terminal(Command {
            working_dir: Some("/workspace".into()),
            env: vec!["HOME=/workspace/synthetic-home".into(), "TERM=caseyard-unsupported-terminal".into()],
            ..Command::new(["sh", "-c", "saved=$(stty -g < /dev/tty); stty raw -echo < /dev/tty; printf 'ready' >&2; printf '<%s><%s><%s><%s><%s><%s>' \"$PWD\" \"$HOME\" \"$TERM\" \"$1\" \"$2\" \"$3\"; dd bs=1 count=11 2>/dev/null; stty \"$saved\" < /dev/tty", "caseyard", "two words", "'literal'", ""])
        }, TerminalOptions::default(), |session| async move {
            session.expect(TerminalChannel::Terminal, b"ready", Duration::from_secs(5)).await?;
            session.send(keys).await?;
            session.wait(Duration::from_secs(5)).await
        }).await?;
        let mut expected = b"</workspace></workspace/synthetic-home><caseyard-unsupported-terminal><two words><'literal'><>".to_vec();
        expected.extend_from_slice(keys);
        assert_eq!(observed.stdout, expected);
        assert_eq!(observed.terminal, b"ready");
        assert_eq!(observed.status, TerminalStatus::Exited { code: 0 });
        assert!(observed.termios_restored);
        assert_eq!(env.snapshot(baseline.entries.keys().cloned()).await.map_err(ScopeError::Infrastructure)?, baseline);
        let observed = env.with_terminal(Command {
            working_dir: Some("/workspace".into()),
            env: vec!["HOME=/workspace/synthetic-home".into(), "TERM=xterm-256color".into()],
            ..Command::new(["sh", "-c", "printf '%s:%s:%s' \"$PWD\" \"$HOME\" \"$TERM\"; printf ready >&2; exec sleep infinity"])
        }, TerminalOptions::default(), |session| async move {
            session.expect(TerminalChannel::Terminal, b"ready", Duration::from_secs(5)).await?;
            session.signal(TerminalSignal::Interrupt).await?;
            session.wait(Duration::from_secs(5)).await
        }).await?;
        assert_eq!(observed.stdout, b"/workspace:/workspace/synthetic-home:xterm-256color");
        assert_eq!(observed.status, TerminalStatus::Signaled { signal: 2 });
        assert_eq!(env.snapshot(baseline.entries.keys().cloned()).await.map_err(ScopeError::Infrastructure)?, baseline);
        Ok::<_, ScopeError<Error>>(())
    }).await?;
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built image, matching static driver and Docker"]
async fn terminal_split_prompt_consumption_and_missing_match_diagnostic()
-> Result<(), Box<dyn std::error::Error>> {
    use caseyard::{TerminalChannel, TerminalFailureObservation, TerminalOptions, TerminalStatus};
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env).await.map_err(ScopeError::Infrastructure)?;
        let observed = env.with_terminal(Command::new(["sh", "-c", "saved=$(stty -g < /dev/tty); stty raw -echo < /dev/tty; printf TO >&2; printf split-ready; dd bs=1 count=1 >/dev/null 2>&1; printf KENprompt >&2; dd bs=1 count=1 >/dev/null 2>&1; printf prompt >&2; stty \"$saved\" < /dev/tty"]), TerminalOptions::default(), |session| async move {
            session.expect(TerminalChannel::Stdout, b"split-ready", Duration::from_secs(5)).await?;
            let split = session.expect(TerminalChannel::Terminal, b"TOKEN", Duration::from_millis(100)).await;
            let Err(Error::Driver { observation: Some(observation), .. }) = split else { panic!("split token must wait for its second half") };
            let TerminalFailureObservation::Complete(before) = *observation else { panic!("expected split-token diagnostic observation") };
            assert_eq!(before.terminal, b"TO");
            session.send(b"x").await?;
            session.expect(TerminalChannel::Terminal, b"TOKEN", Duration::from_secs(5)).await?;
            session.expect(TerminalChannel::Terminal, b"prompt", Duration::from_secs(5)).await?;
            let missing = session.expect(TerminalChannel::Terminal, b"prompt", Duration::from_millis(100)).await;
            let Err(Error::Driver { message, observation: Some(observation) }) = missing else { panic!("consumed prompt must not match twice") };
            assert!(message.contains("terminal"));
            assert!(message.contains("[112 114 111 109 112 116]"));
            let TerminalFailureObservation::Complete(observation) = *observation else { panic!("expected diagnostic observation") };
            assert_eq!(observation.terminal, b"TOKENprompt");
            assert_eq!(observation.status, TerminalStatus::Running);
            session.send(b"y").await?;
            session.expect(TerminalChannel::Terminal, b"prompt", Duration::from_secs(5)).await?;
            session.wait(Duration::from_secs(5)).await
        }).await?;
        assert_eq!(observed.terminal, b"TOKENpromptprompt");
        assert_eq!(observed.stdout, b"split-ready");
        assert_eq!(observed.status, TerminalStatus::Exited { code: 0 });
        Ok::<_, ScopeError<Error>>(())
    }).await?;
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built image, matching static driver and Docker"]
async fn terminal_tiny_recovery_long_output_and_screen_restoration()
-> Result<(), Box<dyn std::error::Error>> {
    use caseyard::{TerminalChannel, TerminalOptions, TerminalStatus};
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env).await.map_err(ScopeError::Infrastructure)?;
        env.write_file("/workspace/unchanged", b"unchanged").await.map_err(ScopeError::Infrastructure)?;
        let baseline = env.snapshot(["/workspace/unchanged", "/workspace/not-created"]).await.map_err(ScopeError::Infrastructure)?;
        let checks = env.clone();
        let expected_baseline = baseline.clone();
        let observed = env.with_terminal(Command::new(["sh", "-c", "saved=$(stty -g < /dev/tty); stty raw -echo < /dev/tty; printf '\\033[?1049h\\033[?25ltiny' >&2; dd bs=1 count=1 >/dev/null 2>&1; stty size < /dev/tty >&2; printf ignored >&2; dd bs=1 count=3 >/dev/null 2>&1; stty size < /dev/tty >&2; head -c 131072 /dev/zero; printf trailing-end; stty \"$saved\" < /dev/tty; printf '\\033[?25h\\033[?1049l' >&2"]), TerminalOptions { rows: 1, columns: 8, ..Default::default() }, |session| async move {
            session.expect(TerminalChannel::Terminal, b"tiny", Duration::from_secs(5)).await?;
            session.send(b"\r").await?;
            let tiny = session.expect(TerminalChannel::Terminal, b"1 8\nignored", Duration::from_secs(5)).await?;
            assert_eq!(tiny.status, TerminalStatus::Running);
            assert_eq!(tiny.stdout, b"");
            assert_eq!(checks.snapshot(expected_baseline.entries.keys().cloned()).await?, expected_baseline);
            session.resize(40, 120).await?;
            session.send(b"\x1b[B").await?;
            session.expect(TerminalChannel::Terminal, b"40 120\n", Duration::from_secs(5)).await?;
            session.expect(TerminalChannel::Stdout, b"trailing-end", Duration::from_secs(5)).await?;
            session.wait(Duration::from_secs(5)).await
        }).await?;
        let mut expected = vec![0; 131_072];
        expected.extend_from_slice(b"trailing-end");
        assert_eq!(observed.stdout, expected);
        assert!(observed.terminal.starts_with(b"\x1b[?1049h\x1b[?25l"));
        assert!(observed.terminal.ends_with(b"\x1b[?25h\x1b[?1049l"));
        assert!(observed.termios_restored);
        assert_eq!(observed.initial_termios, observed.current_termios);
        assert_eq!(observed.status, TerminalStatus::Exited { code: 0 });
        assert_eq!(env.snapshot(baseline.entries.keys().cloned()).await.map_err(ScopeError::Infrastructure)?, baseline);
        Ok::<_, ScopeError<Error>>(())
    }).await?;
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built image, matching static driver and Docker"]
async fn terminal_cancel_and_interrupt_restore_termios_screen_and_cursor()
-> Result<(), Box<dyn std::error::Error>> {
    use caseyard::{TerminalChannel, TerminalOptions, TerminalSignal, TerminalStatus};
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env).await.map_err(ScopeError::Infrastructure)?;
        env.write_file("/workspace/unchanged", b"baseline").await.map_err(ScopeError::Infrastructure)?;
        let baseline = env.snapshot(["/workspace/unchanged", "/workspace/not-created"]).await.map_err(ScopeError::Infrastructure)?;
        for mode in ["cancel", "interrupt"] {
            let observed = env.with_terminal(Command::new(["sh", "-c", "saved=$(stty -g < /dev/tty); restore() { stty \"$saved\" < /dev/tty; printf '\\033[?25h\\033[?1049l' >&2; }; trap 'restore; trap - INT; kill -INT $$' INT; stty raw -echo < /dev/tty; printf '\\033[?1049h\\033[?25lready' >&2; if [ \"$1\" = cancel ]; then dd bs=1 count=1 >/dev/null 2>&1; restore; else while :; do read -r value; done; fi", "caseyard", mode]), TerminalOptions::default(), |session| async move {
                session.expect(TerminalChannel::Terminal, b"ready", Duration::from_secs(5)).await?;
                if mode == "cancel" { session.send(b"\x1b").await?; }
                else { session.signal(TerminalSignal::Interrupt).await?; }
                session.wait(Duration::from_secs(5)).await
            }).await?;
            assert_eq!(observed.stdout, b"");
            assert_eq!(observed.terminal, b"\x1b[?1049h\x1b[?25lready\x1b[?25h\x1b[?1049l");
            assert!(observed.termios_restored);
            assert_eq!(observed.initial_termios, observed.current_termios);
            assert_eq!(observed.status, if mode == "cancel" { TerminalStatus::Exited { code: 0 } } else { TerminalStatus::Signaled { signal: 2 } });
            assert_eq!(env.snapshot(baseline.entries.keys().cloned()).await.map_err(ScopeError::Infrastructure)?, baseline);
        }
        Ok::<_, ScopeError<Error>>(())
    }).await?;
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built image, matching static driver and Docker"]
async fn terminal_pipe_input_unread_bytes_and_signal_are_observable()
-> Result<(), Box<dyn std::error::Error>> {
    use caseyard::{TerminalChannel, TerminalOptions, TerminalSignal, TerminalStatus};
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env)
            .await
            .map_err(ScopeError::Infrastructure)?;
        let observed = env
            .with_terminal(
                Command::new(["sh", "-c", "printf ready >&2; exit 7"]),
                TerminalOptions {
                    input: Some(vec![0, 255, 4]),
                    ..Default::default()
                },
                |session| async move {
                    session
                        .expect(TerminalChannel::Terminal, b"ready", Duration::from_secs(5))
                        .await?;
                    assert!(matches!(
                        session.send(b"not pipe input").await,
                        Err(Error::Driver { .. })
                    ));
                    session.wait(Duration::from_secs(5)).await
                },
            )
            .await?;
        assert_eq!(observed.status, TerminalStatus::Exited { code: 7 });
        assert_eq!(observed.unread_input, [0, 255, 4]);
        assert_eq!(observed.stdout, b"");
        let observed = env
            .with_terminal(
                Command::new(["cat"]),
                TerminalOptions {
                    input: Some(vec![0, 255, 10]),
                    ..Default::default()
                },
                |session| async move { session.wait(Duration::from_secs(5)).await },
            )
            .await?;
        assert_eq!(observed.status, TerminalStatus::Exited { code: 0 });
        assert_eq!(observed.stdout, [0, 255, 10]);
        assert_eq!(observed.terminal, b"");
        assert_eq!(observed.unread_input, b"");
        let observed = env
            .with_terminal(
                Command::new(["sh", "-c", "printf ready >&2; exec sleep infinity"]),
                TerminalOptions::default(),
                |session| async move {
                    session
                        .expect(TerminalChannel::Terminal, b"ready", Duration::from_secs(5))
                        .await?;
                    session.signal(TerminalSignal::Terminate).await?;
                    session.wait(Duration::from_secs(5)).await
                },
            )
            .await?;
        assert_eq!(observed.status, TerminalStatus::Signaled { signal: 15 });
        Ok::<_, ScopeError<Error>>(())
    })
    .await?;
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built image, matching static driver and Docker"]
async fn terminal_callback_failure_closes_process_and_socket() -> Result<(), ScopeError<Error>> {
    use caseyard::{TerminalChannel, TerminalOptions};
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env).await?;
        let outcome = env
            .with_terminal(
                Command::new([
                    "sh",
                    "-c",
                    "echo $$ > /workspace/terminal-pid; printf ready >&2; exec sleep infinity",
                ]),
                TerminalOptions::default(),
                |session| async move {
                    session
                        .expect(TerminalChannel::Terminal, b"ready", Duration::from_secs(5))
                        .await?;
                    Err::<(), _>(Error::InvalidInput("intentional callback failure".into()))
                },
            )
            .await;
        assert!(matches!(
            outcome,
            Err(ScopeError::Callback(Error::InvalidInput(_)))
        ));
        let alive = env
            .run(Command::new([
                "sh",
                "-c",
                "kill -0 \"$(cat /workspace/terminal-pid)\" 2>/dev/null",
            ]))
            .await?;
        assert_eq!(alive.exit_code, 1);
        assert_eq!(
            env.run(Command::new([
                "find",
                "/tmp",
                "-maxdepth",
                "1",
                "-name",
                "caseyard-pty-*"
            ]))
            .await?,
            CommandResult::default()
        );
        Ok(())
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built image, matching static driver and Docker"]
async fn terminal_future_drop_closes_process_and_escaped_handle() -> Result<(), ScopeError<Error>> {
    use caseyard::{TerminalChannel, TerminalOptions};
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env).await?;
        let (send, receive) = tokio::sync::oneshot::channel();
        let mut scope = Box::pin(env.with_terminal(
            Command::new([
                "sh",
                "-c",
                "echo $$ > /workspace/terminal-pid; printf ready >&2; exec sleep infinity",
            ]),
            TerminalOptions::default(),
            |session| async move {
                session
                    .expect(TerminalChannel::Terminal, b"ready", Duration::from_secs(5))
                    .await?;
                send.send(session).map_err(|_| Error::Cancelled)?;
                std::future::pending::<Result<(), Error>>().await
            },
        ));
        let escaped = tokio::select! {
            result = &mut scope => panic!("terminal scope unexpectedly returned: {result:?}"),
            result = receive => result.map_err(|_| Error::Cancelled)?,
        };
        drop(scope);
        assert!(matches!(escaped.observe().await, Err(Error::Cancelled)));
        // The Drop fallback is asynchronous. Poll the owned PID until close completes.
        tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                let alive = env
                    .run(Command::new([
                        "sh",
                        "-c",
                        "kill -0 \"$(cat /workspace/terminal-pid)\" 2>/dev/null",
                    ]))
                    .await?;
                if alive.exit_code == 1 {
                    break Ok::<_, Error>(());
                }
                tokio::task::yield_now().await;
            }
        })
        .await
        .map_err(|_| Error::Deadline)??;
        assert_eq!(
            env.run(Command::new([
                "find",
                "/tmp",
                "-maxdepth",
                "1",
                "-name",
                "caseyard-pty-*"
            ]))
            .await?,
            CommandResult::default()
        );
        Ok(())
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built image, matching static driver and Docker"]
async fn terminal_panic_is_resumed_after_process_cleanup() -> Result<(), ScopeError<Error>> {
    use caseyard::{TerminalChannel, TerminalOptions};
    use futures_util::FutureExt;
    with_environment(IMAGE, |env| async move {
        install_test_driver(&env).await?;
        let scope = env.with_terminal(
            Command::new([
                "sh",
                "-c",
                "echo $$ > /workspace/terminal-pid; printf ready >&2; exec sleep infinity",
            ]),
            TerminalOptions::default(),
            |session| async move {
                session
                    .expect(TerminalChannel::Terminal, b"ready", Duration::from_secs(5))
                    .await?;
                let observed = session.observe().await?;
                assert_eq!(
                    observed.status,
                    caseyard::TerminalStatus::Exited { code: 7 }
                );
                Ok::<_, Error>(())
            },
        );
        let Err(payload) = std::panic::AssertUnwindSafe(scope).catch_unwind().await else {
            panic!("expected original terminal callback panic")
        };
        let message = payload
            .downcast_ref::<String>()
            .ok_or_else(|| Error::Unsupported("expected native assertion panic".into()))?;
        assert!(message.contains("left: Running"));
        assert!(message.contains("right: Exited { code: 7 }"));
        let alive = env
            .run(Command::new([
                "sh",
                "-c",
                "kill -0 \"$(cat /workspace/terminal-pid)\" 2>/dev/null",
            ]))
            .await?;
        assert_eq!(alive.exit_code, 1);
        assert_eq!(
            env.run(Command::new([
                "find",
                "/tmp",
                "-maxdepth",
                "1",
                "-name",
                "caseyard-pty-*"
            ]))
            .await?,
            CommandResult::default()
        );
        Ok(())
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built nonroot image, matching static driver and Docker"]
async fn nonroot_driver_installation_snapshot_and_terminal()
-> Result<(), Box<dyn std::error::Error>> {
    use caseyard::{PathState, TerminalOptions, TerminalStatus};
    with_environment("e2e-example-nonroot:local", |env| async move {
        install_test_driver(&env)
            .await
            .map_err(ScopeError::Infrastructure)?;
        let uid = env
            .run(Command::new(["id", "-u"]))
            .await
            .map_err(ScopeError::Infrastructure)?;
        assert_ne!(uid.stdout, b"0\n");
        let baseline = env
            .snapshot(["/tmp/caseyard-nonroot-missing"])
            .await
            .map_err(ScopeError::Infrastructure)?;
        assert_eq!(
            baseline.entries["/tmp/caseyard-nonroot-missing"],
            PathState::Missing
        );
        assert_eq!(env.run(Command::new(["sh", "-c", "mkdir -p /tmp/caseyard-permissions/locked; printf secret > /tmp/caseyard-permissions/file"])).await.map_err(ScopeError::Infrastructure)?, CommandResult::default());
        let readable = env.snapshot(["/tmp/caseyard-permissions/file", "/tmp/caseyard-permissions/locked/child"]).await.map_err(ScopeError::Infrastructure)?;
        assert_eq!(env.run(Command::new(["chmod", "000", "/tmp/caseyard-permissions/file", "/tmp/caseyard-permissions/locked"])).await.map_err(ScopeError::Infrastructure)?, CommandResult::default());
        assert!(matches!(env.snapshot(["/tmp/caseyard-permissions/file"]).await, Err(Error::Driver { .. })));
        assert!(matches!(env.snapshot(["/tmp/caseyard-permissions/locked/child"]).await, Err(Error::Driver { .. })));
        assert!(matches!(env.snapshot(readable.entries.keys().cloned()).await, Err(Error::Driver { .. })));
        let observed = env
            .with_terminal(
                Command::new(["printf", "nonroot"]),
                TerminalOptions::default(),
                |session| async move { session.wait(Duration::from_secs(5)).await },
            )
            .await?;
        assert_eq!(observed.stdout, b"nonroot");
        assert_eq!(observed.status, TerminalStatus::Exited { code: 0 });
        Ok::<_, ScopeError<Error>>(())
    })
    .await?;
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn greeting() -> Result<(), ScopeError<Error>> {
    with_environment(IMAGE, |env| async move {
        assert_eq!(
            env.run(Command::new(["echo", "hello from e2e"])).await?,
            expected(0, "hello from e2e\n", "")
        );
        let result = env.run(Command::new(["echo", "UTF-8"])).await?;
        assert_eq!(result, expected(0, "UTF-8\n", ""));
        Ok(())
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn file_workflow() -> Result<(), ScopeError<Error>> {
    with_environment(IMAGE, |env| async move {
        assert_eq!(
            env.run(Command::new([
                "sh",
                "-c",
                "echo first > /workspace/result.txt"
            ]))
            .await?,
            expected(0, "", "")
        );
        assert_eq!(
            env.run(Command::new([
                "sh",
                "-c",
                "echo second >> /workspace/result.txt"
            ]))
            .await?,
            expected(0, "", "")
        );
        assert_eq!(
            env.run(Command::new(["cat", "/workspace/result.txt"]))
                .await?,
            expected(0, "first\nsecond\n", "")
        );
        assert_eq!(
            env.read_file("/workspace/result.txt").await?,
            b"first\nsecond\n"
        );
        env.write_file("/workspace/binary", &[0, 255, 10]).await?;
        assert_eq!(env.read_file("/workspace/binary").await?, &[0, 255, 10]);
        assert_eq!(
            env.run(Command::new(["stat", "-c", "%a", "/workspace/binary"]))
                .await?,
            expected(0, "644\n", "")
        );
        Ok(())
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn expected_failure() -> Result<(), ScopeError<Error>> {
    with_environment(IMAGE, |env| async move {
        assert_eq!(
            env.run(Command::new(["sh", "-c", "echo expected-failure; exit 7"]))
                .await?,
            expected(7, "expected-failure\n", "")
        );
        Ok(())
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn separate_streams() -> Result<(), ScopeError<Error>> {
    with_environment(IMAGE, |env| async move {
        let command = Command {
            working_dir: Some("/workspace".into()),
            env: vec!["HOME=/workspace/synthetic-home".into()],
            ..Command::new([
                "sh",
                "-c",
                "printf '%s:%s\\n' \"$HOME\" \"$PWD\"; printf 'diagnostic\\n' >&2; exit 7",
            ])
        };
        assert_eq!(
            env.run(command).await?,
            expected(7, "/workspace/synthetic-home:/workspace\n", "diagnostic\n")
        );
        Ok(())
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn physical_paths() -> Result<(), ScopeError<Error>> {
    with_environment(IMAGE, |env| async move {
        env.write_file("/workspace/result.txt", b"fixture\n")
            .await?;
        for args in [
            vec!["mkdir", "/workspace/directory"],
            vec!["ln", "-s", "result.txt", "/workspace/link"],
            vec!["ln", "-s", "missing.txt", "/workspace/dangling"],
            vec!["ln", "-s", "directory", "/workspace/directory-link"],
        ] {
            assert_eq!(env.run(Command::new(args)).await?, expected(0, "", ""));
        }
        assert_eq!(
            env.path_kind("/workspace/result.txt").await?,
            PathKind::RegularFile
        );
        assert_eq!(
            env.path_kind("/workspace/directory").await?,
            PathKind::Directory
        );
        assert_eq!(
            env.path_kind("/workspace/missing.txt").await?,
            PathKind::Missing
        );
        for path in [
            "/workspace/link",
            "/workspace/dangling",
            "/workspace/directory-link",
        ] {
            assert_eq!(env.path_kind(path).await?, PathKind::Symlink);
        }
        assert_eq!(env.read_symlink("/workspace/link").await?, b"result.txt");
        assert_eq!(
            env.read_symlink("/workspace/dangling").await?,
            b"missing.txt"
        );
        assert_eq!(
            env.run(Command::new([
                "ln",
                "-s",
                "literal\n\n",
                "/workspace/newline-link"
            ]))
            .await?,
            CommandResult::default()
        );
        assert_eq!(
            env.read_symlink("/workspace/newline-link").await?,
            b"literal\n\n"
        );
        assert_eq!(
            env.run(Command::new(["mkfifo", "/workspace/fifo"])).await?,
            CommandResult::default()
        );
        assert_eq!(env.path_kind("/workspace/fifo").await?, PathKind::Other);
        assert_eq!(
            env.run(Command::new([
                "sh",
                "-c",
                "ln -s \"$(printf '\\377')\" /workspace/byte-link"
            ]))
            .await?,
            CommandResult::default()
        );
        assert_eq!(env.read_symlink("/workspace/byte-link").await?, [255]);
        for path in ["/workspace/result.txt", "/workspace/missing.txt"] {
            assert!(matches!(
                env.read_symlink(path).await,
                Err(Error::Execution { .. })
            ));
        }
        Ok(())
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker, takes 60 seconds"]
async fn deadline() -> Result<(), Box<dyn std::error::Error>> {
    let start = Instant::now();
    let id = with_environment(IMAGE, |env| async move {
        assert!(matches!(
            env.run(Command::new(["sleep", "infinity"])).await,
            Err(Error::Execution {
                failure: ExecutionFailure::Deadline,
                ..
            })
        ));
        Ok::<_, Error>(env.container_id().to_string())
    })
    .await?;
    assert!(start.elapsed() < Duration::from_secs(70));
    let docker = testcontainers::core::client::docker_client_instance().await?;
    assert!(matches!(
        docker.inspect_container(&id, None).await,
        Err(
            testcontainers::bollard::errors::Error::DockerResponseServerError {
                status_code: 404,
                ..
            }
        )
    ));
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn scopes_are_fresh_remove_owned_containers_and_preserve_image()
-> Result<(), Box<dyn std::error::Error>> {
    use testcontainers::{bollard, core::client::docker_client_instance};
    let docker = docker_client_instance().await?;
    let image_before = docker.inspect_image(IMAGE).await?.id;
    let mut ids = Vec::new();
    for _ in 0..2 {
        let inspect = docker.clone();
        let id = with_environment(IMAGE, move |env| async move {
            assert_eq!(
                env.path_kind("/workspace/scoped-fixture").await?,
                PathKind::Missing
            );
            env.write_file("/workspace/scoped-fixture", b"owned\n")
                .await?;
            let info = inspect.inspect_container(env.container_id(), None).await?;
            assert_eq!(
                info.config
                    .and_then(|config| config.labels)
                    .and_then(|mut labels| labels.remove("org.testcontainers.managed-by")),
                Some("testcontainers".into())
            );
            Ok::<_, Error>(env.container_id().to_string())
        })
        .await?;
        assert!(
            matches!(
                docker.inspect_container(&id, None).await,
                Err(bollard::errors::Error::DockerResponseServerError {
                    status_code: 404,
                    ..
                })
            ),
            "Testcontainers did not remove the scope container"
        );
        ids.push(id);
    }
    assert_ne!(ids[0], ids[1]);
    assert_eq!(docker.inspect_image(IMAGE).await?.id, image_before);
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn working_directory_preserves_literal_argv() -> Result<(), ScopeError<Error>> {
    with_environment(IMAGE, |env| async move {
        let directory = "/workspace/a 'quoted' $(literal)";
        assert_eq!(
            env.run(Command::new(["mkdir", "--", directory])).await?,
            expected(0, "", "")
        );
        assert_eq!(
            env.run(Command {
                working_dir: Some(directory.into()),
                ..Command::new([
                    "printf",
                    "<%s>\n",
                    "",
                    "two words",
                    "$(touch /workspace/injected)",
                    "'literal'",
                ])
            })
            .await?,
            expected(
                0,
                "<>\n<two words>\n<$(touch /workspace/injected)>\n<'literal'>\n",
                "",
            )
        );
        assert_eq!(
            env.path_kind("/workspace/injected").await?,
            PathKind::Missing
        );
        Ok(())
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn callback_error_is_preserved_after_container_removal()
-> Result<(), Box<dyn std::error::Error>> {
    let result = with_environment(IMAGE, |env| async move {
        Err::<(), _>((env.container_id().to_string(), 7_u8))
    })
    .await;
    let Err(ScopeError::Callback((id, code))) = result else {
        panic!("expected original callback error")
    };
    assert_eq!(code, 7);
    let docker = testcontainers::core::client::docker_client_instance().await?;
    assert!(matches!(
        docker.inspect_container(&id, None).await,
        Err(
            testcontainers::bollard::errors::Error::DockerResponseServerError {
                status_code: 404,
                ..
            }
        )
    ));
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn callback_panic_is_resumed_after_container_removal()
-> Result<(), Box<dyn std::error::Error>> {
    use futures_util::FutureExt;
    let (send, receive) = tokio::sync::oneshot::channel();
    let scope = with_environment(IMAGE, |env| async move {
        send.send(env.container_id().to_string())
            .map_err(|_| Error::Cancelled)?;
        let actual = env.run(Command::new(["printf", "actual"])).await?;
        assert_eq!(actual, expected(7, "expected", "diagnostic"));
        Ok::<(), Error>(())
    });
    let Err(payload) = std::panic::AssertUnwindSafe(scope).catch_unwind().await else {
        panic!("expected original panic")
    };
    let message = payload
        .downcast_ref::<String>()
        .ok_or("expected native assertion panic")?;
    assert!(message.contains("left: CommandResult { exit_code: 0"));
    assert!(message.contains("right: CommandResult { exit_code: 7"));
    assert!(message.contains("stdout:"));
    assert!(message.contains("stderr:"));
    let docker = testcontainers::core::client::docker_client_instance().await?;
    assert!(matches!(
        docker.inspect_container(&receive.await?, None).await,
        Err(
            testcontainers::bollard::errors::Error::DockerResponseServerError {
                status_code: 404,
                ..
            }
        )
    ));
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn dropping_scope_uses_testcontainers_cleanup() -> Result<(), Box<dyn std::error::Error>> {
    let (send, receive) = tokio::sync::oneshot::channel();
    let mut scope = Box::pin(with_environment(IMAGE, |env| async move {
        send.send(env.container_id().to_string())
            .map_err(|_| Error::Cancelled)?;
        std::future::pending::<Result<(), Error>>().await
    }));
    let id = tokio::select! {
        result = &mut scope => panic!("scope unexpectedly completed: {result:?}"),
        result = receive => result?,
    };
    drop(scope);
    let docker = testcontainers::core::client::docker_client_instance().await?;
    assert!(matches!(
        docker.inspect_container(&id, None).await,
        Err(
            testcontainers::bollard::errors::Error::DockerResponseServerError {
                status_code: 404,
                ..
            }
        )
    ));
    Ok(())
}
