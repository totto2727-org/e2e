use cli_e2e::{Case, Command, CommandResult, Environment, Error, run_cases};

#[tokio::test]
#[ignore = "requires caller-built example-cli:local and a Linux-container Docker Engine"]
async fn cli_scenarios() -> Result<(), Error> {
    run_cases(
        "example-cli:local",
        vec![
            Case::new("prints greeting", prints_greeting_scenario),
            Case::new("returns expected failure", returns_expected_failure_scenario),
        ],
    )
    .await
}

async fn prints_greeting_scenario(environment: Environment) -> Result<(), Error> {
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
}

async fn returns_expected_failure_scenario(environment: Environment) -> Result<(), Error> {
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
}
