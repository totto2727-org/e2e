use caseyard::{Command, CommandResult, Error, ScopeError, with_environment};

#[tokio::test]
#[ignore = "requires caller-built example-cli:local and a Linux-container Docker Engine"]
async fn prints_greeting_scenario() -> Result<(), ScopeError<Error>> {
    with_environment("example-cli:local", |environment| async move {
        environment
            .check_command(
                Command::new(["echo", "hello from e2e"]),
                CommandResult {
                    exit_code: 0,
                    stdout: b"hello from e2e\n".to_vec(),
                    stderr: Vec::new(),
                },
            )
            .await
    })
    .await
}

#[tokio::test]
#[ignore = "requires caller-built example-cli:local and a Linux-container Docker Engine"]
async fn returns_expected_failure_scenario() -> Result<(), ScopeError<Error>> {
    with_environment("example-cli:local", |environment| async move {
        environment
            .check_command(
                Command::new(["false"]),
                CommandResult {
                    exit_code: 1,
                    stdout: Vec::new(),
                    stderr: Vec::new(),
                },
            )
            .await
    })
    .await
}
