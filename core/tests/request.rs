#![cfg(target_os = "linux")]

mod support;

use std::time::{Duration, Instant};

use hand_android_core::{Dialer, Method, Request, RequestError, Response, exchange};
use support::{
    Behavior, Board, FeedBoard, Reply, Seen, Sidecar, SidecarOptions, dialer, endpoint, sidecar_bin,
};
use tokio::time::sleep;

const FLEET: &str = "f0123456789ab";
const SECRET: &str = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210";
const COOKIES: &str =
    "session=zzz; hand_phone_f0123456789ab=abc.def; hand_phone_f999999999999=nope";

fn get(path: &str) -> Request {
    Request::new(Method::Get, FLEET, path, COOKIES, None, b"").unwrap()
}

async fn serve(
    bin: &std::path::Path,
    responder: impl Fn(usize, &Seen) -> Reply + Send + Sync + 'static,
) -> (FeedBoard, Sidecar, Dialer) {
    let board = FeedBoard::start(SECRET, responder).await;
    let sidecar = Sidecar::start(bin, SidecarOptions::new(board.addr()).secret(SECRET));
    let dialer = dialer(sidecar.target()).await;
    (board, sidecar, dialer)
}

async fn ask(dialer: &Dialer, request: Request) -> Result<Response, RequestError> {
    exchange(dialer, &request).await
}

#[tokio::test]
async fn a_get_sends_one_cookie_and_returns_the_status_headers_and_body() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (board, _sidecar, dialer) = serve(&bin, |_, _| {
        Reply::json(r#"{"ok":true}"#.to_owned()).header("X-Hand-Cursor", "c9")
    })
    .await;

    let response = ask(&dialer, get("/api/v1/status?since=c8")).await.unwrap();

    assert_eq!(response.status, 200);
    assert_eq!(
        response.content_type.as_deref(),
        Some("application/json; charset=utf-8")
    );
    assert_eq!(response.cursor.as_deref(), Some("c9"));
    assert_eq!(response.body, br#"{"ok":true}"#);
    let seen = board.seen();
    assert_eq!(seen.len(), 1);
    assert_eq!(seen[0].method, "GET");
    assert_eq!(seen[0].target, format!("/{FLEET}/api/v1/status?since=c8"));
    assert_eq!(seen[0].host.as_deref(), Some("localhost"));
    assert_eq!(seen[0].connection.as_deref(), Some("close"));
    assert_eq!(
        seen[0].cookie.as_deref(),
        Some("hand_phone_f0123456789ab=abc.def")
    );
    assert_eq!(seen[0].csrf, None);
}

#[tokio::test]
async fn a_post_carries_its_csrf_token_and_body() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (board, _sidecar, dialer) =
        serve(&bin, |_, _| Reply::json(r#"{"accepted":true}"#.to_owned())).await;
    let request = Request::new(
        Method::Post,
        FLEET,
        "/api/v1/message",
        COOKIES,
        Some("tok123"),
        br#"{"text":"hi"}"#,
    )
    .unwrap();

    let response = ask(&dialer, request).await.unwrap();

    assert_eq!(response.body, br#"{"accepted":true}"#);
    let seen = board.seen();
    assert_eq!(seen[0].method, "POST");
    assert_eq!(seen[0].csrf.as_deref(), Some("tok123"));
    assert_eq!(seen[0].body, br#"{"text":"hi"}"#);
}

#[tokio::test]
async fn a_chunked_answer_is_decoded() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (_board, _sidecar, dialer) = serve(&bin, |_, _| {
        Reply::raw(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6\r\n, Hand\r\n0\r\n\r\n")
    })
    .await;

    let response = ask(&dialer, get("/api/v1/status")).await.unwrap();

    assert_eq!(response.body, b"hello, Hand");
}

#[tokio::test]
async fn a_304_and_an_error_envelope_are_answers_and_no_longer_paired_is_an_error() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (_board, _sidecar, dialer) = serve(&bin, |n, _| match n {
        0 => Reply::status(304)
            .declaring(12)
            .header("X-Hand-Cursor", "c9"),
        1 => Reply {
            status: 403,
            ..Reply::json(r#"{"error":{"code":"csrf_required"}}"#.to_owned())
        },
        _ => Reply::status(403).header(
            "X-Hand-Error",
            "This%20phone%20is%20no%20longer%20paired%20with%20this%20fleet",
        ),
    })
    .await;

    let unchanged = ask(&dialer, get("/api/v1/status")).await.unwrap();
    assert_eq!((unchanged.status, unchanged.body.len()), (304, 0));
    assert_eq!(unchanged.cursor.as_deref(), Some("c9"));

    let refused = ask(&dialer, get("/api/v1/status")).await.unwrap();
    assert_eq!(refused.status, 403);
    assert_eq!(refused.body, br#"{"error":{"code":"csrf_required"}}"#);

    assert_eq!(
        ask(&dialer, get("/api/v1/status")).await,
        Err(RequestError::Unpaired)
    );
}

#[tokio::test]
async fn a_large_post_body_arrives_whole() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (board, _sidecar, dialer) =
        serve(&bin, |_, _| Reply::json(r#"{"accepted":true}"#.to_owned())).await;
    let body = "x".repeat(100_000);
    let request = Request::new(
        Method::Post,
        FLEET,
        "/api/v1/message",
        COOKIES,
        Some("tok123"),
        body.as_bytes(),
    )
    .unwrap();

    assert!(ask(&dialer, request).await.is_ok());

    assert_eq!(board.seen()[0].body, body.as_bytes());
}

#[tokio::test]
async fn a_full_laptop_is_busy_when_the_deadline_comes() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let board = Board::start(SECRET, Behavior::Http).await;
    let sidecar = Sidecar::start(
        &bin,
        SidecarOptions::new(board.addr())
            .secret(SECRET)
            .max_connections(1),
    );
    let holder_endpoint = endpoint().await;
    let _held = holder_endpoint
        .connect(sidecar.target(), hand_android_core::ALPN)
        .await
        .unwrap();
    sleep(Duration::from_millis(100)).await;
    let dialer = dialer(sidecar.target()).await;

    let started = Instant::now();
    let request = get("/api/v1/status").within(Duration::from_secs(2));

    assert_eq!(ask(&dialer, request).await, Err(RequestError::Busy));
    assert!(started.elapsed() < Duration::from_secs(4));
}

#[tokio::test]
async fn a_body_over_the_cap_is_too_large() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (_board, _sidecar, dialer) = serve(&bin, |_, _| Reply::json("x".repeat(2000))).await;

    let small = get("/api/v1/status").up_to(1999);
    assert_eq!(ask(&dialer, small).await, Err(RequestError::TooLarge));
    let fits = get("/api/v1/status").up_to(2000);
    assert_eq!(ask(&dialer, fits).await.unwrap().body.len(), 2000);
}

#[tokio::test]
async fn an_answer_that_is_not_http_is_malformed() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (_board, _sidecar, dialer) =
        serve(&bin, |_, _| Reply::raw(b"not http at all\r\n\r\n")).await;

    assert_eq!(
        ask(&dialer, get("/api/v1/status")).await,
        Err(RequestError::Malformed)
    );
}

#[tokio::test]
async fn an_answer_cut_short_is_lost() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (_board, _sidecar, dialer) =
        serve(&bin, |_, _| Reply::json("abc".to_owned()).declaring(10)).await;

    assert_eq!(
        ask(&dialer, get("/api/v1/status")).await,
        Err(RequestError::Lost)
    );
}

#[tokio::test]
async fn a_board_that_does_not_answer_in_time_times_out() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (_board, _sidecar, dialer) = serve(&bin, |_, _| {
        Reply::json("late".to_owned()).held(Duration::from_secs(5))
    })
    .await;

    let request = get("/api/v1/status").within(Duration::from_millis(500));
    assert_eq!(ask(&dialer, request).await, Err(RequestError::TimedOut));
}

#[tokio::test]
async fn a_stream_the_sidecar_resets_for_idling_times_out() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let board = Board::start(SECRET, Behavior::Hold).await;
    let sidecar = Sidecar::start(
        &bin,
        SidecarOptions::new(board.addr()).secret(SECRET).idle(1),
    );
    let dialer = dialer(sidecar.target()).await;
    let (mut keepalive, _recv) = dialer.open_with(b"a").await.unwrap();
    let heartbeat = tokio::spawn(async move {
        while keepalive.write_all(b"a").await.is_ok() {
            sleep(Duration::from_millis(200)).await;
        }
    });
    sleep(Duration::from_millis(100)).await;

    assert_eq!(
        ask(&dialer, get("/api/v1/status")).await,
        Err(RequestError::TimedOut)
    );
    heartbeat.abort();
}

#[tokio::test]
async fn a_board_that_errors_is_lost() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let board = Board::start(SECRET, Behavior::Reset).await;
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(board.addr()).secret(SECRET));
    let dialer = dialer(sidecar.target()).await;

    assert_eq!(
        ask(&dialer, get("/api/v1/status")).await,
        Err(RequestError::Lost)
    );
}

#[tokio::test]
async fn a_board_that_is_down_is_lost_once_and_not_running_the_second_time() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let closed = listener.local_addr().unwrap();
    drop(listener);
    let sidecar = Sidecar::start(&bin, SidecarOptions::new(closed).secret(SECRET));
    let dialer = dialer(sidecar.target()).await;

    assert_eq!(
        ask(&dialer, get("/api/v1/status")).await,
        Err(RequestError::Lost)
    );
    assert_eq!(
        ask(&dialer, get("/api/v1/status")).await,
        Err(RequestError::BoardNotRunning)
    );
}
