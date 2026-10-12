use super::*;
use std::sync::Arc;
use tokio::time::timeout;

pub(super) type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;

#[test]
fn argv_boundaries_and_empty_values_are_preserved() -> TestResult {
    let cmd = Command::new(["printf", "%s", "", "two words", "'literal'"]);
    cmd.validate()?;
    assert_eq!(cmd.args, ["printf", "%s", "", "two words", "'literal'"]);
    Ok(())
}
#[test]
fn invalid_fields_are_rejected() {
    for cmd in [
        Command::default(),
        Command::new([""]),
        Command::new(["echo", "\0"]),
        Command {
            working_dir: Some("/a\0".into()),
            ..Command::new(["echo"])
        },
        Command {
            env: vec!["A=\0".into()],
            ..Command::new(["echo"])
        },
    ] {
        assert!(matches!(cmd.validate(), Err(Error::InvalidInput(_))));
    }
    assert!(matches!(path_valid("\0"), Err(Error::InvalidInput(_))));
}
#[test]
fn every_command_field_is_compared_exactly() -> TestResult {
    let expected = CommandResult {
        exit_code: 7,
        stdout: vec![0, 255],
        stderr: vec![],
    };
    compare_command(&expected, &expected)?;
    for actual in [
        CommandResult {
            exit_code: 0,
            ..expected.clone()
        },
        CommandResult {
            stdout: vec![0],
            ..expected.clone()
        },
        CommandResult {
            stderr: vec![1],
            ..expected.clone()
        },
    ] {
        assert!(matches!(
            compare_command(&actual, &expected),
            Err(Error::Assertion(_))
        ));
    }
    Ok(())
}
#[test]
fn chunks_preserve_binary_bytes_and_stream_order() -> TestResult {
    let mut output = PartialOutput::default();
    for chunk in [
        LogOutput::StdOut {
            message: vec![0, 255].into(),
        },
        LogOutput::StdErr {
            message: b"err".as_slice().into(),
        },
        LogOutput::StdOut {
            message: b"next".as_slice().into(),
        },
    ] {
        append_output(&mut output, chunk)?;
    }
    assert_eq!(
        output,
        PartialOutput {
            stdout: vec![0, 255, b'n', b'e', b'x', b't'],
            stderr: b"err".to_vec()
        }
    );
    assert!(matches!(
        append_output(
            &mut output,
            LogOutput::StdIn {
                message: vec![].into()
            }
        ),
        Err(Error::Unsupported(_))
    ));
    Ok(())
}
#[test]
fn archive_preserves_bytes_and_mode() -> TestResult {
    let payload = [0, 1, 255, b'\n'];
    let bytes = file_archive(std::ffi::OsStr::new("fixture"), &payload)?;
    assert_eq!(read_archive(&bytes)?, payload);
    let mut archive = tar::Archive::new(bytes.as_slice());
    let entry = archive
        .entries()?
        .next()
        .ok_or("expected archive entry")??;
    assert_eq!(entry.header().mode()?, 0o644);
    assert_eq!(entry.path()?, Path::new("fixture"));
    Ok(())
}
#[test]
fn prior_error_and_cleanup_error_are_both_retained() {
    let cleanup = Error::Cleanup {
        resource: "owned".into(),
        cause: Box::new(Error::Deadline),
    };
    let Err(ScopeError::Cleanup {
        prior: Some(prior),
        errors,
    }) = combine_scope::<(), _>(Err(ScopeError::Callback("consumer error")), vec![cleanup])
    else {
        panic!("expected both errors")
    };
    assert!(matches!(*prior, ScopeError::Callback("consumer error")));
    assert_eq!(errors.len(), 1);
    assert!(matches!(errors[0], Error::Cleanup { .. }));
}
fn environment(deadline: Instant) -> TestResult<Environment> {
    Ok(Environment {
        docker: Docker::connect_with_http("http://127.0.0.1:1", 60, API_DEFAULT_VERSION)?,
        id: "not-used".into(),
        deadline,
        cancel: CancellationToken::new(),
    })
}
#[tokio::test(start_paused = true)]
async fn deadline_drops_owned_pending_operation() -> TestResult {
    struct Dropped(Arc<std::sync::atomic::AtomicBool>);
    impl Drop for Dropped {
        fn drop(&mut self) {
            self.0.store(true, std::sync::atomic::Ordering::SeqCst);
        }
    }
    let env = environment(Instant::now() + CASE_BUDGET)?;
    let dropped = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let guard = Dropped(dropped.clone());
    let result: Result<(), Error> = env
        .bounded(async {
            let _guard = guard;
            futures_util::future::pending().await
        })
        .await;
    assert!(matches!(result, Err(Error::Deadline)));
    assert!(dropped.load(std::sync::atomic::Ordering::SeqCst));
    Ok(())
}
#[tokio::test]
async fn cancellation_unblocks_pending_operations() -> TestResult {
    let env = environment(Instant::now() + CASE_BUDGET)?;
    env.cancel.cancel();
    let result: Result<(), Error> = env.bounded(futures_util::future::pending()).await;
    assert!(matches!(result, Err(Error::Cancelled)));
    Ok(())
}
#[tokio::test]
async fn image_validation_fails_before_docker_connection() {
    for image in ["", "bad\0image"] {
        assert!(matches!(
            with_environment(image, |_| async { Ok::<(), Error>(()) }).await,
            Err(ScopeError::Infrastructure(Error::InvalidInput(_)))
        ));
    }
}

pub(super) async fn request(socket: &mut tokio::net::TcpStream) -> TestResult<String> {
    use tokio::io::AsyncReadExt;
    let mut bytes = Vec::new();
    while !bytes.ends_with(b"\r\n\r\n") {
        bytes.push(socket.read_u8().await?);
    }
    let header = String::from_utf8(bytes)?;
    let length = header
        .lines()
        .find_map(|l| {
            l.to_ascii_lowercase()
                .strip_prefix("content-length: ")
                .map(str::to_string)
        })
        .map(|l| l.parse::<usize>())
        .transpose()?
        .unwrap_or(0);
    let mut body = vec![0; length];
    socket.read_exact(&mut body).await?;
    Ok(header + &String::from_utf8_lossy(&body))
}
#[tokio::test]
async fn real_http_exec_timeout_retains_output_and_closes_socket() -> TestResult {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await?;
    let docker = Docker::connect_with_http(
        &format!("http://{}", listener.local_addr()?),
        60,
        API_DEFAULT_VERSION,
    )?;
    let server = tokio::spawn(async move {
        let (mut first, _) = listener.accept().await?;
        let req = request(&mut first).await?;
        assert!(req.contains("/containers/test/exec"));
        let body = r#"{"Id":"exec-id"}"#;
        first
            .write_all(
                format!(
                    "HTTP/1.1 201 Created\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                    body.len(),
                    body
                )
                .as_bytes(),
            )
            .await?;
        drop(first);
        let (mut stream, _) = listener.accept().await?;
        assert!(request(&mut stream).await?.contains("/exec/exec-id/start"));
        stream.write_all(b"HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n\x01\0\0\0\0\0\0\x03abc\x02\0\0\0\0\0\0\x03err").await?;
        let mut byte = [0];
        assert_eq!(
            timeout(Duration::from_secs(2), stream.read(&mut byte)).await??,
            0
        );
        Ok::<(), Box<dyn std::error::Error + Send + Sync>>(())
    });
    let env = Environment {
        docker,
        id: "test".into(),
        deadline: Instant::now() + Duration::from_millis(150),
        cancel: CancellationToken::new(),
    };
    let Err(Error::Execution {
        failure: ExecutionFailure::Deadline,
        output,
    }) = env.run(Command::new(["sleep", "infinity"])).await
    else {
        panic!("expected deadline with partial output")
    };
    assert_eq!(
        output,
        PartialOutput {
            stdout: b"abc".to_vec(),
            stderr: b"err".to_vec()
        }
    );
    server.await??;
    Ok(())
}
