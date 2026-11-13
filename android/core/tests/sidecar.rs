#![cfg(target_os = "linux")]

mod support;

use std::time::{Duration, Instant};

use hand_android_core::{
    BOARD_ERROR, BOARD_UNREACHABLE, Notice, STREAM_IDLE, STREAMS_PER_CONNECTION, parse_target,
};
use iroh::endpoint::{ReadError, WriteError};
use support::{
    Behavior, Board, Sidecar, SidecarOptions, body, dialer, endpoint, proxy, request, sidecar_bin,
};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::time::{sleep, timeout};

const WAIT: Duration = Duration::from_secs(10);

async fn wait_for(mut condition: impl FnMut() -> bool) -> bool {
    let deadline = Instant::now() + WAIT;
    while Instant::now() < deadline {
        if condition() {
            return true;
        }
        sleep(Duration::from_millis(20)).await;
    }
    condition()
}

#[tokio::test]
async fn proxied_http_requests_and_responses_cross_one_connection() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let secret = "0123456789abcdef0123456789abcdef0123456789abcdef";
    let board = Board::start(secret, Behavior::Http).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()).max_connections(1));
    let dialer = dialer(sidecar.target()).await;
    let (_dir, port) = proxy(dialer).await;

    let mut tasks = Vec::new();
    for path in ["/one", "/two", "/three"] {
        tasks.push(tokio::spawn(async move { request(port, path).await }));
    }
    let mut slowest = Duration::ZERO;
    for task in tasks {
        let (response, ttfb) = timeout(WAIT, task).await.unwrap().unwrap().unwrap();
        slowest = slowest.max(ttfb);
        assert_eq!(body(&response), b"hello from the board\n");
        assert!(response.starts_with(b"HTTP/1.1 200 OK\r\n"));
    }
    eprintln!("proxy time to first byte, slowest of three: {slowest:?}");
    assert_eq!(board.accepted(), 3);
}

#[tokio::test]
async fn half_close_reaches_the_board_and_the_answer_ends_the_client_side() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let secret = "0123456789abcdef0123456789abcdef0123456789abcdef";
    let board = Board::start(secret, Behavior::HalfClose).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()));
    let dialer = dialer(sidecar.target()).await;
    let (_dir, port) = proxy(dialer).await;

    let mut stream = tokio::net::TcpStream::connect(("127.0.0.1", port))
        .await
        .unwrap();
    stream
        .write_all(b"GET /half HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    stream.shutdown().await.unwrap();
    let mut response = Vec::new();
    timeout(WAIT, stream.read_to_end(&mut response))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(body(&response), b"hello from the board\n");
}

#[tokio::test]
async fn a_board_that_is_down_resets_with_code_three_and_the_connection_stays_open() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let closed = listener.local_addr().unwrap();
    drop(listener);
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(closed));
    let dialer = dialer(sidecar.target()).await;

    for attempt in 0..2 {
        let (mut send, mut recv) = dialer
            .open_with(b"GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
            .await
            .unwrap();
        let mut buffer = [0u8; 64];
        let error = timeout(WAIT, recv.read(&mut buffer))
            .await
            .unwrap()
            .unwrap_err();
        assert!(
            matches!(error, ReadError::Reset(code) if code.into_inner() == BOARD_UNREACHABLE),
            "{attempt}: {error:?}"
        );
        assert!(dialer.connection().await.unwrap().close_reason().is_none());
        send.finish().unwrap();
    }
    assert_eq!(dialer.notice(), Notice::BoardUnreachable);

    let (_dir, port) = proxy(dialer).await;
    let (response, ttfb) = timeout(WAIT, request(port, "/")).await.unwrap().unwrap();
    assert!(response.is_empty());
    assert!(ttfb < Duration::from_secs(2), "{ttfb:?}");
}

#[tokio::test]
async fn a_board_that_errors_resets_the_stream_with_code_five() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let secret = "0123456789abcdef0123456789abcdef0123456789abcdef";
    let board = Board::start(secret, Behavior::Reset).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()));
    let dialer = dialer(sidecar.target()).await;

    let (_send, mut recv) = dialer
        .open_with(b"GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    let mut buffer = [0u8; 64];
    let error = timeout(WAIT, recv.read(&mut buffer))
        .await
        .unwrap()
        .unwrap_err();
    assert!(
        matches!(error, ReadError::Reset(code) if code.into_inner() == BOARD_ERROR),
        "{error:?}"
    );
}

#[tokio::test]
async fn an_idle_stream_is_reset_with_code_four_while_another_stream_keeps_the_connection_busy() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let secret = "0123456789abcdef0123456789abcdef0123456789abcdef";
    let board = Board::start(secret, Behavior::Drain).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()).idle(1));
    let dialer = dialer(sidecar.target()).await;

    let (mut keepalive, _keepalive_recv) = dialer.open_with(b"a").await.unwrap();
    let heartbeat = tokio::spawn(async move {
        loop {
            if keepalive.write_all(b"a").await.is_err() {
                break;
            }
            sleep(Duration::from_millis(200)).await;
        }
    });
    sleep(Duration::from_millis(100)).await;
    let (_send, mut idle) = dialer.open_with(b"b").await.unwrap();
    let mut buffer = [0u8; 64];
    let error = timeout(Duration::from_secs(5), idle.read(&mut buffer))
        .await
        .unwrap()
        .unwrap_err();
    assert!(
        matches!(error, ReadError::Reset(code) if code.into_inner() == STREAM_IDLE),
        "{error:?}"
    );
    assert!(dialer.connection().await.unwrap().close_reason().is_none());
    heartbeat.abort();
}

#[tokio::test]
async fn an_idle_connection_closes_with_code_two_and_dials_again_on_the_next_stream() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let secret = "0123456789abcdef0123456789abcdef0123456789abcdef";
    let board = Board::start(secret, Behavior::Drain).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()).idle(1));
    let dialer = dialer(sidecar.target()).await;

    let connection = dialer.connection().await.unwrap();
    let error = timeout(Duration::from_secs(5), connection.closed())
        .await
        .unwrap();
    assert_eq!(
        hand_android_core::IDLE,
        match &error {
            iroh::endpoint::ConnectionError::ApplicationClosed(close) =>
                close.error_code.into_inner(),
            _ => panic!("{error:?}"),
        }
    );
    sleep(Duration::from_millis(100)).await;
    assert!(dialer.notice() == Notice::Idle || dialer.notice() == Notice::Connected);
    let (_send, _recv) = timeout(WAIT, dialer.open_with(b"next"))
        .await
        .unwrap()
        .unwrap();
}

#[tokio::test]
async fn the_thirteenth_stream_waits_for_one_to_end() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let secret = "0123456789abcdef0123456789abcdef0123456789abcdef";
    let board = Board::start(secret, Behavior::Hold).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()));
    let dialer = dialer(sidecar.target()).await;
    let (_dir, port) = proxy(dialer).await;

    let mut held = Vec::new();
    for _ in 0..STREAMS_PER_CONNECTION {
        let mut stream = tokio::net::TcpStream::connect(("127.0.0.1", port))
            .await
            .unwrap();
        stream
            .write_all(b"GET /hold HTTP/1.1\r\nHost: localhost\r\n\r\n")
            .await
            .unwrap();
        held.push(stream);
    }
    assert!(
        wait_for(|| board.accepted() == STREAMS_PER_CONNECTION).await,
        "the board saw {} streams",
        board.accepted()
    );

    let mut thirteenth = tokio::net::TcpStream::connect(("127.0.0.1", port))
        .await
        .unwrap();
    thirteenth
        .write_all(b"GET /thirteen HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    sleep(Duration::from_millis(500)).await;
    assert_eq!(board.accepted(), STREAMS_PER_CONNECTION);

    board.release_all();
    drop(held);
    assert!(
        wait_for(|| board.accepted() == STREAMS_PER_CONNECTION + 1).await,
        "the board still saw {} streams",
        board.accepted()
    );
}

#[tokio::test]
async fn the_stream_opens_on_the_first_byte_and_not_before() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let secret = "0123456789abcdef0123456789abcdef0123456789abcdef";
    let board = Board::start(secret, Behavior::Http).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()));
    let dialer = dialer(sidecar.target()).await;
    let (_dir, port) = proxy(dialer).await;

    let mut stream = tokio::net::TcpStream::connect(("127.0.0.1", port))
        .await
        .unwrap();
    sleep(Duration::from_millis(300)).await;
    assert_eq!(board.accepted(), 0);

    stream.write_all(b"G").await.unwrap();
    assert!(wait_for(|| board.accepted() == 1).await);
    stream
        .write_all(b"ET /first HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    let mut response = Vec::new();
    timeout(WAIT, stream.read_to_end(&mut response))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(body(&response), b"hello from the board\n");
}

#[tokio::test]
async fn a_full_laptop_backs_off_before_the_next_dial() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let secret = "0123456789abcdef0123456789abcdef0123456789abcdef";
    let board = Board::start(secret, Behavior::Http).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()).max_connections(1));

    let holder_endpoint = endpoint().await;
    let held = holder_endpoint
        .connect(sidecar.target(), hand_android_core::ALPN)
        .await
        .unwrap();
    sleep(Duration::from_millis(100)).await;

    let dialer = dialer(sidecar.target()).await;
    let started = Instant::now();
    let opening = tokio::spawn(async move {
        dialer
            .open_with(b"GET /after HTTP/1.1\r\nHost: localhost\r\n\r\n")
            .await
    });
    sleep(Duration::from_millis(300)).await;
    held.close(iroh::endpoint::VarInt::from_u32(0), b"done");
    let (_send, mut recv) = timeout(WAIT, opening).await.unwrap().unwrap().unwrap();
    let elapsed = started.elapsed();
    let mut response = Vec::new();
    recv_read_to_end(&mut recv, &mut response).await;
    assert_eq!(body(&response), b"hello from the board\n");
    eprintln!("full laptop: first successful stream after {elapsed:?}");
    assert!(elapsed >= Duration::from_millis(950), "{elapsed:?}");
    assert!(elapsed < Duration::from_secs(5), "{elapsed:?}");
}

async fn recv_read_to_end(recv: &mut hand_android_core::StreamRecv, out: &mut Vec<u8>) {
    let mut chunk = [0u8; 4096];
    loop {
        match timeout(WAIT, recv.read(&mut chunk)).await.unwrap() {
            Ok(0) => return,
            Ok(n) => out.extend_from_slice(&chunk[..n]),
            Err(error) => panic!("{error:?}"),
        }
    }
}

#[tokio::test]
async fn a_wrong_secret_ends_the_stream_with_no_byte() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let board = Board::start(
        "different-secret-0123456789abcdef0123456789abcdef",
        Behavior::Http,
    )
    .await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()));
    let dialer = dialer(sidecar.target()).await;

    let (_send, mut recv) = dialer
        .open_with(b"GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    let mut buffer = [0u8; 64];
    match timeout(WAIT, recv.read(&mut buffer)).await.unwrap() {
        Ok(0) => {}
        Err(ReadError::Reset(code)) => assert_eq!(code.into_inner(), BOARD_ERROR),
        other => panic!("{other:?}"),
    }
}

#[tokio::test]
async fn an_answer_survives_the_board_closing_while_the_client_still_sends() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let secret = "0123456789abcdef0123456789abcdef0123456789abcdef";
    let board = Board::start(secret, Behavior::AnswerThenClose).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()));
    let dialer = dialer(sidecar.target()).await;

    let (mut send, mut recv) = dialer
        .open_with(b"POST /upload HTTP/1.1\r\nHost: localhost\r\nContent-Length: 8388608\r\n\r\n")
        .await
        .unwrap();
    let mut response = Vec::new();
    recv_read_to_end(&mut recv, &mut response).await;
    assert_eq!(body(&response), b"accepted before the body\n");
    let mut stopped = None;
    for _ in 0..64 {
        match send.write_all(&[b'u'; 64 << 10]).await {
            Ok(()) => {}
            Err(error) => {
                stopped = Some(error);
                break;
            }
        }
    }
    if let Some(WriteError::Stopped(code)) = stopped {
        assert_eq!(code.into_inner(), BOARD_ERROR);
    }
}

#[tokio::test]
async fn a_parseable_target_carries_the_relay_as_a_hint() {
    let id = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6";
    let target = parse_target(id, Some("https://relay.example.test:7842/")).unwrap();
    assert_eq!(target.relay_urls().count(), 1);
    assert!(target.ip_addrs().next().is_none());
}

#[tokio::test]
async fn the_proxy_delivers_an_answer_when_the_board_closes_while_the_client_sends() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let secret = "0123456789abcdef0123456789abcdef0123456789abcdef";
    let board = Board::start(secret, Behavior::AnswerThenClose).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()));
    let dialer = dialer(sidecar.target()).await;
    let (_dir, port) = proxy(dialer).await;

    let mut stream = tokio::net::TcpStream::connect(("127.0.0.1", port))
        .await
        .unwrap();
    stream
        .write_all(b"POST /upload HTTP/1.1\r\nHost: localhost\r\nContent-Length: 8388608\r\n\r\n")
        .await
        .unwrap();
    stream.write_all(&[b'u'; 64 << 10]).await.unwrap();
    let mut response = Vec::new();
    timeout(WAIT, stream.read_to_end(&mut response))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(body(&response), b"accepted before the body\n");
}
