use cli_e2e::{Command, CommandResult, Error, ExecutionFailure, ScopeError, with_environment};
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
        env.check_file("/workspace/binary", &[0, 255, 10]).await
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
async fn deadline() -> Result<(), ScopeError<Error>> {
    let start = Instant::now();
    with_environment(IMAGE, |env| async move {
        assert!(matches!(
            env.run(Command::new(["sleep", "infinity"])).await,
            Err(Error::Execution {
                failure: ExecutionFailure::Deadline,
                ..
            })
        ));
        Ok(())
    })
    .await?;
    assert!(start.elapsed() < Duration::from_secs(70));
    Ok(())
}

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn scopes_are_fresh_remove_owned_containers_and_preserve_image()
-> Result<(), Box<dyn std::error::Error>> {
    use bollard::{Docker, query_parameters::ListContainersOptionsBuilder};
    let docker = Docker::connect_with_defaults()?.negotiate_version().await?;
    let image_before = docker.inspect_image(IMAGE).await?.id;
    let mut ids = Vec::new();
    for _ in 0..2 {
        let inspect = docker.clone();
        let (id, owner) = with_environment(IMAGE, move |env| async move {
            env.check_missing("/workspace/scoped-fixture").await?;
            env.write_file("/workspace/scoped-fixture", b"owned\n")
                .await?;
            let info = inspect.inspect_container(env.container_id(), None).await?;
            let owner = info
                .config
                .and_then(|config| config.labels)
                .and_then(|mut labels| labels.remove("org.totto2727.e2e.rust.owner"))
                .ok_or_else(|| Error::Assertion("scope owner label absent".into()))?;
            Ok::<_, Error>((env.container_id().to_string(), owner))
        })
        .await?;
        let filters = std::collections::HashMap::from([(
            "label".to_string(),
            vec![format!("org.totto2727.e2e.rust.owner={owner}")],
        )]);
        assert!(
            docker
                .list_containers(Some(
                    ListContainersOptionsBuilder::default()
                        .all(true)
                        .filters(&filters)
                        .build()
                ))
                .await?
                .is_empty(),
            "scenario or lease leaked for this scope"
        );
        ids.push(id);
    }
    assert_ne!(ids[0], ids[1]);
    assert_eq!(docker.inspect_image(IMAGE).await?.id, image_before);
    Ok(())
}
