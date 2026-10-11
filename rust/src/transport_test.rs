use super::*;
use tokio::io::AsyncWriteExt;

async fn reply(socket: &mut tokio::net::TcpStream, status: &str, body: &str) {
    socket.write_all(format!("HTTP/1.1 {status}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).as_bytes()).await.unwrap();
}

#[tokio::test]
async fn eof_before_exit_is_polled_and_nonzero_is_success() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let docker = Docker::connect_with_http(
        &format!("http://{}", listener.local_addr().unwrap()),
        60,
        API_DEFAULT_VERSION,
    )
    .unwrap();
    let server = tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.unwrap();
        assert!(super::tests::request(&mut socket).await.contains("/exec"));
        reply(&mut socket, "201 Created", r#"{"Id":"exec"}"#).await;
        drop(socket);
        let (mut socket, _) = listener.accept().await.unwrap();
        assert!(
            super::tests::request(&mut socket)
                .await
                .contains("/exec/exec/start")
        );
        socket.write_all(b"HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n\x01\0\0\0\0\0\0\x03abc").await.unwrap();
        drop(socket);
        for body in [
            r#"{"Running":true,"ExitCode":0}"#,
            r#"{"Running":false,"ExitCode":7}"#,
        ] {
            let (mut socket, _) = listener.accept().await.unwrap();
            assert!(
                super::tests::request(&mut socket)
                    .await
                    .contains("/exec/exec/json")
            );
            reply(&mut socket, "200 OK", body).await;
        }
    });
    let env = Environment {
        docker,
        id: "case".into(),
        deadline: Instant::now() + Duration::from_secs(3),
        cancel: CancellationToken::new(),
    };
    assert_eq!(
        env.run(Command::new(["app"])).await.unwrap(),
        CommandResult {
            exit_code: 7,
            stdout: b"abc".to_vec(),
            stderr: vec![]
        }
    );
    server.await.unwrap();
}
#[tokio::test]
async fn cleanup_never_removes_foreign_container() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let docker = Docker::connect_with_http(
        &format!("http://{}", listener.local_addr().unwrap()),
        60,
        API_DEFAULT_VERSION,
    )
    .unwrap();
    let resource = Resource::new(docker, "mine");
    resource.creation.store(2, Ordering::SeqCst);
    let server = tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.unwrap();
        assert!(super::tests::request(&mut socket).await.starts_with("GET "));
        reply(
            &mut socket,
            "200 OK",
            r#"{"Id":"foreign","Config":{"Labels":{"org.totto2727.e2e.rust.owner":"other"}}}"#,
        )
        .await;
        assert!(
            timeout(Duration::from_millis(100), listener.accept())
                .await
                .is_err()
        );
    });
    let Err(Error::Cleanup {
        resource: name,
        cause,
    }) = resource.cleanup().await
    else {
        panic!("expected typed cleanup refusal")
    };
    assert_eq!(name, resource.name);
    assert!(matches!(*cause, Error::Unsupported(_)));
    server.await.unwrap();
}
#[tokio::test]
async fn cleanup_retries_owned_removal_and_keeps_fresh_budget() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let docker = Docker::connect_with_http(
        &format!("http://{}", listener.local_addr().unwrap()),
        60,
        API_DEFAULT_VERSION,
    )
    .unwrap();
    let resource = Resource::new(docker, "mine");
    resource.creation.store(2, Ordering::SeqCst);
    let server = tokio::spawn(async move {
        for attempt in 0..2 {
            let (mut socket, _) = listener.accept().await.unwrap();
            assert!(super::tests::request(&mut socket).await.starts_with("GET "));
            reply(
                &mut socket,
                "200 OK",
                r#"{"Id":"owned","Config":{"Labels":{"org.totto2727.e2e.rust.owner":"mine"}}}"#,
            )
            .await;
            drop(socket);
            let (mut socket, _) = listener.accept().await.unwrap();
            let removal = super::tests::request(&mut socket).await;
            assert!(removal.starts_with("DELETE "));
            assert!(removal.contains("/containers/owned?"), "{removal}");
            if attempt == 0 {
                reply(&mut socket, "500 Error", r#"{"message":"retry"}"#).await;
            } else {
                reply(&mut socket, "204 No Content", "").await;
            }
        }
    });
    resource.cleanup().await.unwrap();
    server.await.unwrap();
}

#[tokio::test]
async fn daemon_failure_is_not_application_nonzero() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let docker = Docker::connect_with_http(
        &format!("http://{}", listener.local_addr().unwrap()),
        60,
        API_DEFAULT_VERSION,
    )
    .unwrap();
    let server = tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.unwrap();
        super::tests::request(&mut socket).await;
        reply(&mut socket, "500 Error", r#"{"message":"daemon failed"}"#).await;
    });
    let env = Environment {
        docker,
        id: "case".into(),
        deadline: Instant::now() + Duration::from_secs(3),
        cancel: CancellationToken::new(),
    };
    let Err(Error::Execution {
        failure:
            ExecutionFailure::Docker(bollard::errors::Error::DockerResponseServerError {
                status_code: 500,
                ..
            }),
        output,
    }) = env.run(Command::new(["app"])).await
    else {
        panic!("expected typed Docker failure")
    };
    assert_eq!(output, PartialOutput::default());
    server.await.unwrap();
}

#[tokio::test]
async fn uncertain_creation_reconciles_missing_then_owned_container() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let docker = Docker::connect_with_http(
        &format!("http://{}", listener.local_addr().unwrap()),
        60,
        API_DEFAULT_VERSION,
    )
    .unwrap();
    let resource = Resource::new(docker, "mine");
    resource.creation.store(1, Ordering::SeqCst);
    let server = tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.unwrap();
        super::tests::request(&mut socket).await;
        reply(
            &mut socket,
            "404 Not Found",
            r#"{"message":"not yet visible"}"#,
        )
        .await;
        drop(socket);
        let (mut socket, _) = listener.accept().await.unwrap();
        super::tests::request(&mut socket).await;
        reply(
            &mut socket,
            "200 OK",
            r#"{"Id":"late-owned","Config":{"Labels":{"org.totto2727.e2e.rust.owner":"mine"}}}"#,
        )
        .await;
        drop(socket);
        let (mut socket, _) = listener.accept().await.unwrap();
        assert!(
            super::tests::request(&mut socket)
                .await
                .contains("/containers/late-owned?")
        );
        reply(&mut socket, "204 No Content", "").await;
    });
    resource.cleanup().await.unwrap();
    server.await.unwrap();
}

#[tokio::test(start_paused = true)]
async fn exhausted_cleanup_has_typed_resource_failure() {
    let resource = Resource::new(
        Docker::connect_with_http("http://127.0.0.1:1", 60, API_DEFAULT_VERSION).unwrap(),
        "mine",
    );
    resource.creation.store(2, Ordering::SeqCst);
    let started = Instant::now();
    let Err(Error::Cleanup { resource: name, .. }) = resource.cleanup().await else {
        panic!("expected typed cleanup failure")
    };
    assert_eq!(name, resource.name);
    assert_eq!(Instant::now() - started, CLEANUP_BUDGET);
}
