use super::*;
use std::{
    io::Read,
    sync::atomic::{AtomicBool, Ordering},
};

type TestResult = Result<(), Box<dyn std::error::Error>>;

pub(super) fn disconnected_environment() -> Result<Environment, Error> {
    Ok(Environment {
        container: Weak::new(),
        docker: bollard::Docker::connect_with_local_defaults()?,
        id: "native-no-container".into(),
        deadline: Instant::now() + CASE_BUDGET,
        cancel: CancellationToken::new(),
        driver: Arc::new(tokio::sync::Mutex::new(None)),
    })
}

#[test]
fn argv_boundaries_and_empty_values_are_preserved() -> TestResult {
    let command = Command::new(["printf", "%s", "", "two words", "'literal'"]);
    command.validate()?;
    assert_eq!(command.args, ["printf", "%s", "", "two words", "'literal'"]);
    Ok(())
}
#[test]
fn invalid_command_fields_are_rejected() {
    for command in [
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
        Command {
            env: vec!["KEY".into()],
            ..Command::new(["echo"])
        },
        Command {
            env: vec!["=value".into()],
            ..Command::new(["echo"])
        },
    ] {
        assert!(matches!(command.validate(), Err(Error::InvalidInput(_))));
    }
}
#[test]
fn environment_values_preserve_empty_values_and_equals() -> TestResult {
    assert_eq!(environment_entry("A=")?, ("A", ""));
    assert_eq!(environment_entry("A=one=two")?, ("A", "one=two"));
    Ok(())
}
#[tokio::test]
async fn invalid_image_is_rejected_before_docker_access() {
    for image in ["", "invalid\0image"] {
        assert!(matches!(
            with_environment(image, |_| async { Ok::<_, Error>(()) }).await,
            Err(ScopeError::Infrastructure(Error::InvalidInput(_)))
        ));
    }
}
#[test]
fn native_scope_accepts_borrowed_non_send_callbacks() {
    let value = std::rc::Rc::new(String::from("borrowed"));
    let future = with_environment("", |_| async { Ok::<_, Error>(&value) });
    drop(future);
    assert_eq!(&**value, "borrowed");
}
#[test]
fn invalid_paths_are_rejected() {
    for path in ["", "invalid\0path"] {
        assert!(matches!(path_valid(path), Err(Error::InvalidInput(_))));
    }
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
fn upload_archive_preserves_exact_bytes_and_mode() -> TestResult {
    let payload = [0, 1, 255, b'\n'];
    let bytes = file_archive(std::ffi::OsStr::new("fixture"), &payload, 0o644)?;
    let mut archive = tar::Archive::new(bytes.as_slice());
    let mut entries = archive.entries()?;
    let mut entry = entries.next().ok_or("expected archive entry")??;
    assert_eq!(entry.header().mode()?, 0o644);
    assert_eq!(entry.path()?, Path::new("fixture"));
    let mut actual = Vec::new();
    entry.read_to_end(&mut actual)?;
    assert_eq!(actual, payload);
    drop(entry);
    assert!(entries.next().is_none());
    Ok(())
}
#[test]
fn callback_and_cleanup_failures_are_both_retained() {
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
#[test]
fn callback_values_and_error_types_are_preserved() {
    #[derive(Debug, PartialEq)]
    struct ConsumerError(u8);
    assert_eq!(combine_scope::<_, &str>(Ok(42), vec![]).ok(), Some(42));
    assert!(matches!(
        combine_scope::<(), _>(Err(ScopeError::Callback(ConsumerError(7))), vec![]),
        Err(ScopeError::Callback(ConsumerError(7)))
    ));
}
#[tokio::test(start_paused = true)]
async fn deadline_drops_pending_operation() {
    struct Dropped(Arc<AtomicBool>);
    impl Drop for Dropped {
        fn drop(&mut self) {
            self.0.store(true, Ordering::SeqCst);
        }
    }
    let dropped = Arc::new(AtomicBool::new(false));
    let guard = Dropped(dropped.clone());
    let result: Result<(), Error> = bounded(
        Instant::now() + CASE_BUDGET,
        &CancellationToken::new(),
        async {
            let _guard = guard;
            futures_util::future::pending().await
        },
    )
    .await;
    assert!(matches!(result, Err(Error::Deadline)));
    assert!(dropped.load(Ordering::SeqCst));
}
#[tokio::test]
async fn cancellation_releases_operation_read_lock_before_cleanup() -> TestResult {
    let owner: Container = Arc::new(RwLock::new(None));
    let cancel = CancellationToken::new();
    let held = owner.read().await;
    let operation = bounded(Instant::now() + CASE_BUDGET, &cancel, async {
        let _held = held;
        futures_util::future::pending::<Result<(), Error>>().await
    });
    cancel.cancel();
    let (result, cleanup) = tokio::join!(operation, cleanup(&owner, "empty-native-scope"));
    assert!(matches!(result, Err(Error::Cancelled)));
    cleanup?;
    Ok(())
}
#[tokio::test(start_paused = true)]
async fn cleanup_does_not_wait_forever_for_unpolled_operation() {
    let owner: Container = Arc::new(RwLock::new(None));
    let _held = owner.read().await;
    let Err(Error::Cleanup { resource, cause }) = cleanup(&owner, "blocked-native-scope").await
    else {
        panic!("expected bounded cleanup lock wait")
    };
    assert_eq!(resource, "blocked-native-scope");
    assert!(matches!(*cause, Error::Deadline));
}
#[test]
fn scope_drop_cancels_escaped_operation_tokens() {
    let cancel = CancellationToken::new();
    let escaped = cancel.clone();
    drop(CancelOnDrop(cancel));
    assert!(escaped.is_cancelled());
}
#[test]
fn weak_handles_do_not_extend_scope_ownership() {
    let owner: Container = Arc::new(RwLock::new(None));
    let escaped = Arc::downgrade(&owner);
    drop(owner);
    assert!(escaped.upgrade().is_none());
}
