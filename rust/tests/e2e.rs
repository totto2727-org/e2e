use caseyard::{Command, CommandResult, Error, ExecutionFailure, ScopeError, with_environment};
use std::time::{Duration, Instant};

const IMAGE: &str = "e2e-example:local";

fn expected(exit_code: i64, stdout: &str, stderr: &str) -> CommandResult {
    CommandResult {
        exit_code,
        stdout: stdout.as_bytes().to_vec(),
        stderr: stderr.as_bytes().to_vec(),
    }
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn greeting() -> Result<(), ScopeError<Error>> {
    with_environment(IMAGE, |env| async move {
        env.check_command(
            Command::new(["echo", "hello from e2e"]),
            expected(0, "hello from e2e\n", ""),
        )
        .await?;
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
        env.check_command(
            Command::new(["sh", "-c", "echo first > /workspace/result.txt"]),
            expected(0, "", ""),
        )
        .await?;
        env.check_command(
            Command::new(["sh", "-c", "echo second >> /workspace/result.txt"]),
            expected(0, "", ""),
        )
        .await?;
        env.check_command(
            Command::new(["cat", "/workspace/result.txt"]),
            expected(0, "first\nsecond\n", ""),
        )
        .await?;
        env.check_file("/workspace/result.txt", b"first\nsecond\n")
            .await?;
        env.write_file("/workspace/binary", &[0, 255, 10]).await?;
        env.check_file("/workspace/binary", &[0, 255, 10]).await?;
        env.check_command(
            Command::new(["stat", "-c", "%a", "/workspace/binary"]),
            expected(0, "644\n", ""),
        )
        .await
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn expected_failure() -> Result<(), ScopeError<Error>> {
    with_environment(IMAGE, |env| async move {
        env.check_command(
            Command::new(["sh", "-c", "echo expected-failure; exit 7"]),
            expected(7, "expected-failure\n", ""),
        )
        .await
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
        env.check_command(
            command,
            expected(7, "/workspace/synthetic-home:/workspace\n", "diagnostic\n"),
        )
        .await
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
            env.check_command(Command::new(args), expected(0, "", ""))
                .await?;
        }
        env.check_regular_file("/workspace/result.txt").await?;
        env.check_directory("/workspace/directory").await?;
        env.check_missing("/workspace/missing.txt").await?;
        env.check_symlink("/workspace/link", "result.txt").await?;
        env.check_symlink("/workspace/dangling", "missing.txt")
            .await?;
        assert!(matches!(
            env.check_missing("/workspace/dangling").await,
            Err(Error::Assertion(_))
        ));
        assert!(matches!(
            env.check_regular_file("/workspace/link").await,
            Err(Error::Assertion(_))
        ));
        assert!(matches!(
            env.check_directory("/workspace/directory-link").await,
            Err(Error::Assertion(_))
        ));
        assert!(matches!(
            env.check_symlink("/workspace/link", "/workspace/result.txt")
                .await,
            Err(Error::Assertion(_))
        ));
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
            env.check_missing("/workspace/scoped-fixture").await?;
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
        env.check_command(
            Command::new(["mkdir", "--", directory]),
            expected(0, "", ""),
        )
        .await?;
        env.check_command(
            Command {
                working_dir: Some(directory.into()),
                ..Command::new([
                    "printf",
                    "<%s>\n",
                    "",
                    "two words",
                    "$(touch /workspace/injected)",
                    "'literal'",
                ])
            },
            expected(
                0,
                "<>\n<two words>\n<$(touch /workspace/injected)>\n<'literal'>\n",
                "",
            ),
        )
        .await?;
        env.check_missing("/workspace/injected").await
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
        std::panic::panic_any(123_u32);
        #[allow(unreachable_code)]
        Ok::<(), Error>(())
    });
    let Err(payload) = std::panic::AssertUnwindSafe(scope).catch_unwind().await else {
        panic!("expected original panic")
    };
    assert_eq!(payload.downcast_ref::<u32>(), Some(&123));
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
