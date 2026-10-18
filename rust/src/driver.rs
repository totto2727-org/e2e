use crate::{Command, CommandResult, Environment, Error, ExecutionFailure, PartialOutput};
use serde::{Deserialize, de::DeserializeOwned};
use serde_json::Value;

fn protocol(message: String, result: &CommandResult) -> Error {
    Error::Execution {
        failure: ExecutionFailure::Protocol(message),
        output: PartialOutput {
            stdout: result.stdout.clone(),
            stderr: result.stderr.clone(),
        },
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Reply<T> {
    ok: bool,
    value: Option<T>,
    error: Option<String>,
    observation: Option<Value>,
}

pub(crate) async fn invoke<T: DeserializeOwned>(
    env: &Environment,
    mode: &str,
    request: &Value,
    original: Option<&Command>,
) -> Result<T, Error> {
    let path = env
        .bounded(async {
            env.driver.lock().await.clone().ok_or_else(|| {
                Error::InvalidInput(
                    "install a matching Linux driver before using snapshots or terminals".into(),
                )
            })
        })
        .await?;
    let mut command = Command::new([path.as_str(), mode, &request.to_string()]);
    if let Some(original) = original {
        command.working_dir.clone_from(&original.working_dir);
        command.env.clone_from(&original.env);
    }
    let result = env.run(command).await?;
    decode(&result)
}

impl Environment {
    /// Install a caller-built static Linux helper matching the container architecture once.
    /// Its private executable and staging directory live only inside this environment.
    ///
    /// # Errors
    /// Returns invalid-input errors for empty bytes or duplicate installation, or execution/upload errors.
    pub async fn install_driver(&self, bytes: &[u8]) -> Result<(), Error> {
        if bytes.is_empty() {
            return Err(Error::InvalidInput("driver bytes must not be empty".into()));
        }
        let mut installed = self.bounded(async { Ok(self.driver.lock().await) }).await?;
        if installed.is_some() {
            return Err(Error::InvalidInput("driver is already installed".into()));
        }
        let result = self
            .run(Command::new([
                "mktemp",
                "-d",
                "/tmp/caseyard-driver-XXXXXXXXXX",
            ]))
            .await?;
        if result.exit_code != 0 || !result.stderr.is_empty() {
            return Err(protocol(
                "driver staging directory creation failed".into(),
                &result,
            ));
        }
        let directory = std::str::from_utf8(&result.stdout)
            .ok()
            .and_then(|value| value.strip_suffix('\n'))
            .filter(|value| {
                value.starts_with("/tmp/caseyard-driver-")
                    && !value[5..].contains('/')
                    && !value.contains(['\n', '\r', '\0'])
            })
            .ok_or_else(|| protocol("invalid driver staging directory".into(), &result))?;
        let path = format!("{directory}/driver");
        // Docker uploads may be root-owned even for a nonroot image user.
        self.write_file_mode(&path, bytes, 0o755).await?;
        *installed = Some(path);
        Ok(())
    }
}

pub(crate) fn decode<T: DeserializeOwned>(result: &CommandResult) -> Result<T, Error> {
    if !result.stderr.is_empty() {
        return Err(protocol(
            format!("driver failed (exit {})", result.exit_code),
            result,
        ));
    }
    let raw: Value = serde_json::from_slice(&result.stdout)
        .map_err(|error| protocol(format!("invalid driver response: {error}"), result))?;
    if raw.get("ok") == Some(&Value::Bool(true)) && raw.get("value").is_none() {
        return Err(protocol("driver success lacks value".into(), result));
    }
    let reply: Reply<Value> = serde_json::from_value(raw)
        .map_err(|error| protocol(format!("invalid driver response: {error}"), result))?;
    if reply.ok {
        if result.exit_code != 0 {
            return Err(protocol(
                format!("driver failed (exit {})", result.exit_code),
                result,
            ));
        }
        if reply.error.is_some() || reply.observation.is_some() {
            return Err(protocol("inconsistent driver success".into(), result));
        }
        serde_json::from_value(reply.value.unwrap_or(Value::Null))
            .map_err(|error| protocol(format!("invalid driver value: {error}"), result))
    } else {
        if reply.value.is_some() {
            return Err(protocol("inconsistent driver failure".into(), result));
        }
        let message = reply
            .error
            .filter(|message| !message.is_empty())
            .ok_or_else(|| protocol("driver failure lacks diagnostic".into(), result))?;
        let observation = reply
            .observation
            .map(serde_json::from_value)
            .transpose()
            .map_err(|error| {
                protocol(
                    format!("invalid driver failure observation: {error}"),
                    result,
                )
            })?;
        Err(Error::Driver {
            message,
            observation,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[tokio::test]
    async fn missing_empty_and_duplicate_install_fail_without_docker() -> Result<(), Error> {
        let env = crate::tests::disconnected_environment()?;
        assert!(matches!(
            env.install_driver(&[]).await,
            Err(Error::InvalidInput(_))
        ));
        assert!(matches!(
            invoke::<()>(&env, "snapshot", &json!({"paths":[]}), None).await,
            Err(Error::InvalidInput(_))
        ));
        *env.driver.lock().await = Some("/installed/driver".into());
        assert!(matches!(
            env.install_driver(&[1]).await,
            Err(Error::InvalidInput(_))
        ));
        for path in ["", "bad\0path"] {
            assert!(matches!(
                env.snapshot([path]).await,
                Err(Error::InvalidInput(_))
            ));
        }
        Ok(())
    }

    #[test]
    fn helper_failures_retain_raw_output_and_partial_terminal_observation() {
        let result = CommandResult { stdout: br#"{"ok":false,"error":"expected terminal bytes [255]","observation":{"stdout":[0,255],"terminal":[27]}}"#.to_vec(), ..Default::default() };
        let Err(Error::Driver {
            message,
            observation: Some(observation),
        }) = decode::<()>(&result)
        else {
            panic!("expected structured driver error")
        };
        assert!(message.contains("[255]"));
        assert_eq!(
            *observation,
            crate::TerminalFailureObservation::Partial(crate::TerminalOutput {
                stdout: vec![0, 255],
                terminal: vec![27]
            })
        );
        let result = CommandResult {
            exit_code: 1,
            stdout: vec![255],
            stderr: b"diagnostic".to_vec(),
        };
        let Err(Error::Execution { output, .. }) = decode::<()>(&result) else {
            panic!("expected execution failure")
        };
        assert_eq!(output.stdout, [255]);
        assert_eq!(output.stderr, b"diagnostic");
    }

    #[test]
    fn malformed_driver_envelopes_are_rejected() {
        for payload in [
            json!({"ok":"true","value":null}),
            json!({"ok":true}),
            json!({"ok":false,"value":1,"error":"failure"}),
            json!({"ok":false}),
            json!({"ok":true,"value":null,"error":"failure"}),
            json!({"ok":false,"error":"failure","observation":{"stdout":[256],"terminal":[]}}),
        ] {
            let result = CommandResult {
                stdout: payload.to_string().into_bytes(),
                ..Default::default()
            };
            assert!(matches!(
                decode::<()>(&result),
                Err(Error::Execution {
                    failure: ExecutionFailure::Protocol(_),
                    ..
                })
            ));
        }
    }
}
