use bollard::{Docker, query_parameters::ListContainersOptionsBuilder};
use cli_e2e::{Case, Command, CommandResult, Error, ExecutionFailure, run_cases};
use std::{
    collections::HashSet,
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};

fn expected(exit_code: i64, stdout: &str, stderr: &str) -> CommandResult {
    CommandResult {
        exit_code,
        stdout: stdout.as_bytes().to_vec(),
        stderr: stderr.as_bytes().to_vec(),
    }
}
/// Explicit acceptance only. The root task builds the image before this test.
#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn six_common_cases_preserve_image_and_remove_all_owned_containers() {
    let docker = Docker::connect_with_defaults()
        .unwrap()
        .negotiate_version()
        .await
        .unwrap();
    let image_before = docker.inspect_image("e2e-example:local").await.unwrap().id;
    let before = owned_containers(&docker).await;
    let ids = Arc::new(Mutex::new(Vec::new()));
    let mut cases = vec![
        Case::new("greeting", |env| async move {
            env.check_command(
                Command::new(["echo", "hello from e2e"]),
                expected(0, "hello from e2e\n", ""),
            )
            .await?;
            let result = env.run(Command::new(["echo", "UTF-8"])).await?;
            assert_eq!(std::str::from_utf8(&result.stdout).unwrap(), "UTF-8\n");
            Ok(())
        }),
        Case::new("file workflow", |env| async move {
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
        }),
        Case::new("expected failure", |env| async move {
            env.check_command(
                Command::new(["sh", "-c", "echo expected-failure; exit 7"]),
                expected(7, "expected-failure\n", ""),
            )
            .await
        }),
        Case::new("separate streams", |env| async move {
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
        }),
        Case::new("physical paths", |env| async move {
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
        }),
        Case::new("deadline", |env| async move {
            let start = Instant::now();
            assert!(matches!(
                env.run(Command::new(["sleep", "infinity"])).await,
                Err(Error::Execution {
                    failure: ExecutionFailure::Deadline,
                    ..
                })
            ));
            assert!(start.elapsed() < Duration::from_secs(70));
            Ok(())
        }),
    ];
    // Wrap callbacks to record each identity through the public environment API.
    // The additional two cases explicitly test separate resource identities.
    for name in ["identity a", "identity b"] {
        let ids = ids.clone();
        cases.push(Case::new(name, move |env| async move {
            ids.lock().unwrap().push(env.container_id().to_owned());
            env.check_command(Command::new(["true"]), expected(0, "", ""))
                .await
        }));
    }
    let result = run_cases("e2e-example:local", cases).await;
    assert_eq!(
        owned_containers(&docker).await,
        before,
        "scenario or lease leaked"
    );
    assert_eq!(
        docker.inspect_image("e2e-example:local").await.unwrap().id,
        image_before
    );
    result.unwrap();
    let ids = ids.lock().unwrap();
    assert_eq!(ids.len(), 2);
    assert_ne!(ids[0], ids[1]);
}
async fn owned_containers(docker: &Docker) -> HashSet<String> {
    let filters = std::collections::HashMap::from([(
        "label".to_string(),
        vec!["org.totto2727.e2e.rust.owner".to_string()],
    )]);
    docker
        .list_containers(Some(
            ListContainersOptionsBuilder::default()
                .all(true)
                .filters(&filters)
                .build(),
        ))
        .await
        .unwrap()
        .into_iter()
        .map(|c| c.id.unwrap())
        .collect()
}
