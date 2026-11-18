#![cfg(target_os = "linux")]

mod support;

use std::sync::Arc;
use std::time::Duration;

use hand_android_core::{Companion, CompanionConfig};
use support::{
    Behavior, Board, PlainCipher, Sidecar, SidecarOptions, TempDir, body, request, sidecar_bin,
};
use tokio::net::TcpStream;
use tokio::time::timeout;

const SECRET: &str = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210";
const WAIT: Duration = Duration::from_secs(10);
const HELLO: &[u8] = b"hello from the board\n";

async fn served(port: u16, path: &str) -> bool {
    match timeout(WAIT, request(port, path)).await {
        Ok(Ok((response, _))) => response.starts_with(b"HTTP/1.1 200") && body(&response) == HELLO,
        _ => false,
    }
}

async fn start(bin: &std::path::Path) -> (Companion, Sidecar, TempDir, Board) {
    let board = Board::start(SECRET, Behavior::Http).await;
    let sidecar = Sidecar::start(bin, SidecarOptions::new(board.addr()).secret(SECRET));
    let dir = TempDir::new();
    let companion = Companion::start(CompanionConfig {
        data_dir: dir.path().to_path_buf(),
        cipher: Arc::new(PlainCipher),
    })
    .await
    .unwrap();
    companion.dialer().set_target(Some(sidecar.target())).await;
    (companion, sidecar, dir, board)
}

#[tokio::test]
async fn a_companion_asleep_keeps_its_port_and_identity_and_serves_again_after_wake() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (companion, _sidecar, _dir, _board) = start(&bin).await;
    let (port, phone) = (companion.port(), companion.phone_id());
    assert!(served(port, "/one").await);
    assert!(
        companion
            .report()
            .await
            .contains(" state=live sleeps=0 wakes=0 polls=0")
    );

    companion.sleep().await;

    assert!(companion.asleep());
    assert!(companion.dialer().is_down());
    assert!(companion.dialer().endpoint().is_closed());
    assert!(TcpStream::connect(("127.0.0.1", port)).await.is_ok());
    assert!(!served(port, "/two").await);
    assert!(
        companion
            .report()
            .await
            .contains(" state=asleep sleeps=1 wakes=0 polls=0")
    );

    companion.wake().await.unwrap();

    assert!(!companion.asleep());
    assert!(!companion.dialer().endpoint().is_closed());
    assert_eq!((companion.port(), companion.phone_id()), (port, phone));
    assert!(served(port, "/three").await);
    assert!(
        companion
            .report()
            .await
            .contains(" state=live sleeps=1 wakes=1 polls=0")
    );

    companion.sleep().await;
    companion.sleep().await;
    assert!(companion.report().await.contains(" sleeps=2 wakes=1 "));
}

#[tokio::test]
async fn a_poll_binds_for_its_work_and_sleeps_again_and_a_live_companion_is_not_polled() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let (companion, _sidecar, _dir, _board) = start(&bin).await;
    let port = companion.port();

    let ran = std::sync::atomic::AtomicBool::new(false);
    let live = companion
        .poll_asleep(async { ran.store(true, std::sync::atomic::Ordering::SeqCst) })
        .await
        .unwrap();
    assert!(!live && !ran.load(std::sync::atomic::Ordering::SeqCst));

    companion.sleep().await;
    let mut seen = false;
    let polled = companion
        .poll_asleep(async { seen = served(port, "/poll").await })
        .await
        .unwrap();

    assert!(polled && seen);
    assert!(companion.asleep());
    assert!(companion.dialer().is_down());
    assert!(
        companion
            .report()
            .await
            .contains(" state=asleep sleeps=1 wakes=0 polls=1")
    );
    assert!(!served(port, "/after").await);

    companion.wake().await.unwrap();
    assert!(served(port, "/live").await);
}
