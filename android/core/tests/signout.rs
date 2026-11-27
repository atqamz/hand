#![cfg(target_os = "linux")]

mod support;

use std::collections::HashMap;
use std::io;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use hand_android_core::{
    Alert, AlertSink, Companion, CompanionConfig, Fate, Feed, Fleet, Paired, Sessions, SignOut,
    Supervisor,
};
use serde_json::json;
use support::{
    FeedBoard, MemoryJar, PlainCipher, Reply, Seen, Sidecar, SidecarOptions, TempDir, route,
    sidecar_bin,
};
use tokio::time::sleep;

const PEER: &str = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210";
const A: &str = "f1111111111aa";
const B: &str = "f2222222222bb";
const C: &str = "f3333333333cc";
const WAIT: Duration = Duration::from_secs(15);
const TICK: Duration = Duration::from_millis(100);
const POLL_HOLD: Duration = Duration::from_millis(300);

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Mode {
    Live,
    Revoked,
    SessionGone401,
    SessionGoneWithError,
    SessionPlain403,
    RevokeRaced401,
    RevokeRacedWithError,
    RevokeAnswers(u16),
    Silent,
}

#[derive(Clone, Default)]
struct Sink {
    unpaired: Arc<Mutex<Vec<String>>>,
}

impl AlertSink for Sink {
    async fn alert(&self, _fleet: &Fleet, _alert: &Alert) -> io::Result<()> {
        Ok(())
    }

    async fn unpaired(&self, fleet: &str, _name: Option<&str>) {
        self.unpaired.lock().unwrap().push(fleet.to_owned());
    }
}

type Modes = Arc<Mutex<HashMap<&'static str, Mode>>>;

fn secret(fleet: &str) -> String {
    fleet.trim_start_matches('f').repeat(6)[..64].to_owned()
}

fn value(fleet: &str) -> String {
    format!("7.{}", secret(fleet))
}

fn csrf(fleet: &str) -> String {
    format!("csrf-{fleet}")
}

fn feed_answer(fleet: &str, cursor: u64) -> Reply {
    Reply::json(
        json!({
            "version": 1,
            "fleet": {"id": fleet, "name": "hand"},
            "cursor": cursor,
            "more": false,
            "alerts": [],
        })
        .to_string(),
    )
    .held(POLL_HOLD)
}

fn gone() -> Reply {
    Reply::status(403).header("X-Hand-Error", "no longer paired")
}

fn responder(
    modes: Modes,
    slow_revoke: Option<&'static str>,
) -> impl Fn(usize, &Seen) -> Reply + Send + Sync + 'static {
    move |_, seen| {
        let fleet = seen.target.split('/').nth(1).unwrap_or_default();
        let mode = *modes
            .lock()
            .unwrap()
            .iter()
            .find(|(id, _)| **id == fleet)
            .map(|(_, mode)| mode)
            .unwrap_or(&Mode::Live);
        let set = |next: Mode| {
            let mut modes = modes.lock().unwrap();
            if let Some(key) = modes.keys().find(|id| **id == fleet).copied() {
                modes.insert(key, next);
            }
        };
        if seen.target.contains("/alerts/feed") {
            return match mode {
                Mode::Revoked => gone(),
                _ => feed_answer(fleet, 5),
            };
        }
        if seen.target.ends_with("/api/v1/session") {
            return match mode {
                Mode::Silent => Reply::raw(b""),
                Mode::Revoked => gone(),
                Mode::SessionGone401 => Reply::status(401),
                Mode::SessionGoneWithError => gone(),
                Mode::SessionPlain403 => Reply::status(403),
                _ => Reply::json(json!({"data": {"csrf": csrf(fleet)}}).to_string()),
            };
        }
        if seen.target.ends_with("/api/v1/session/revoke") {
            let reply = match mode {
                Mode::RevokeRaced401 => Reply::status(401),
                Mode::RevokeRacedWithError => gone(),
                Mode::RevokeAnswers(status) => Reply::status(status),
                Mode::Live if seen.csrf.as_deref() == Some(csrf(fleet).as_str()) => {
                    set(Mode::Revoked);
                    Reply::json(json!({"data": {"accepted": true}}).to_string())
                }
                _ => Reply::status(403),
            };
            return match slow_revoke {
                Some(slow) if slow == fleet => reply.held(Duration::from_millis(900)),
                _ => reply,
            };
        }
        Reply::json(json!({}).to_string())
    }
}

struct Rig {
    companion: Arc<Companion>,
    sessions: Arc<Sessions<MemoryJar>>,
    supervisor: Arc<Supervisor<Arc<Sessions<MemoryJar>>, Sink>>,
    sink: Sink,
    board: FeedBoard,
    _sidecar: Sidecar,
    host: String,
    dir: TempDir,
}

async fn rig(
    bin: &std::path::Path,
    fleets: &[(&'static str, Mode)],
    slow: Option<&'static str>,
) -> Rig {
    let modes: Modes = Arc::new(Mutex::new(fleets.iter().copied().collect()));
    let board = FeedBoard::start(PEER, responder(modes, slow)).await;
    let sidecar = Sidecar::start(bin, SidecarOptions::new(board.addr()).secret(PEER));
    let host = sidecar.id().to_string();
    let dir = TempDir::new();
    let companion = Arc::new(
        Companion::start(CompanionConfig {
            data_dir: dir.path().to_path_buf(),
            cipher: Arc::new(PlainCipher),
        })
        .await
        .unwrap(),
    );
    let sessions =
        Arc::new(Sessions::open(dir.path(), MemoryJar::default(), Arc::new(PlainCipher)).unwrap());
    let paired: Vec<Paired> = fleets
        .iter()
        .map(|(id, _)| Paired {
            id: (*id).to_owned(),
            name: format!("fleet {id}"),
            value: value(id),
        })
        .collect();
    sessions.pair(&host, None, &paired).unwrap();
    companion.dialers().host(&host, sidecar.target()).await;
    let sink = Sink::default();
    let feed = Feed::routed(route(&companion, &sessions), dir.path().join("alerts"));
    let supervisor = Supervisor::new(feed, sessions.clone(), sink.clone(), TICK);
    Rig {
        companion,
        sessions,
        supervisor,
        sink,
        board,
        _sidecar: sidecar,
        host,
        dir,
    }
}

impl Rig {
    async fn sign_out(&self, cleared: &Arc<AtomicBool>) -> SignOut {
        let (sessions, supervisor, host) = (&self.sessions, &self.supervisor, &self.host);
        let cleared = cleared.clone();
        self.companion
            .sign_out_host(sessions, supervisor, host, || {
                cleared.store(true, Ordering::SeqCst);
                supervisor.forget_fleets(&[A.to_owned(), B.to_owned(), C.to_owned()]);
                sessions.forget_host(host)
            })
            .await
            .unwrap()
    }

    fn requests(&self, fleet: &str, part: &str) -> Vec<Seen> {
        self.board
            .seen()
            .into_iter()
            .filter(|seen| {
                seen.target.starts_with(&format!("/{fleet}/")) && seen.target.contains(part)
            })
            .collect()
    }

    fn cookie(&self, fleet: &str) -> String {
        format!("hand_phone_{fleet}={}", value(fleet))
    }

    fn cursor(&self, fleet: &str) -> Option<String> {
        std::fs::read_to_string(
            self.dir
                .path()
                .join("alerts")
                .join(format!("{fleet}.cursor")),
        )
        .ok()
    }

    fn held(&self) -> usize {
        self.sessions.listed().len()
    }
}

async fn until(mut condition: impl FnMut() -> bool) -> bool {
    let deadline = Instant::now() + WAIT;
    while Instant::now() < deadline {
        if condition() {
            return true;
        }
        sleep(Duration::from_millis(50)).await;
    }
    condition()
}

fn flag() -> Arc<AtomicBool> {
    Arc::new(AtomicBool::new(false))
}

fn left(outcome: &SignOut) -> Vec<(&str, Fate)> {
    match outcome {
        SignOut::Left(left) => left
            .iter()
            .map(|fleet| (fleet.id.as_str(), fleet.fate))
            .collect(),
        SignOut::Done => Vec::new(),
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn every_fleet_is_signed_out_with_its_own_cookie_and_token_and_then_the_host_is_gone() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(
        &bin,
        &[(A, Mode::Live), (B, Mode::Live), (C, Mode::Live)],
        None,
    )
    .await;
    let cookies: Vec<String> = [A, B, C].iter().map(|fleet| rig.cookie(fleet)).collect();
    let cleared = flag();

    let outcome = rig.sign_out(&cleared).await;

    assert_eq!(outcome, SignOut::Done);
    assert!(cleared.load(Ordering::SeqCst));
    assert_eq!(rig.held(), 0);
    assert!(rig.sessions.hosts().is_empty());
    assert!(rig.companion.dialers().get(&rig.host).is_none());
    for (fleet, cookie) in [A, B, C].iter().zip(&cookies) {
        let revokes = rig.requests(fleet, "/api/v1/session/revoke");
        assert_eq!(revokes.len(), 1, "{fleet}");
        assert_eq!(revokes[0].method, "POST");
        assert_eq!(revokes[0].csrf.as_deref(), Some(csrf(fleet).as_str()));
        let mine = rig.requests(fleet, "");
        assert_eq!(mine.len(), 2, "{fleet}: one read of the token, one revoke");
        for seen in mine {
            assert_eq!(seen.cookie.as_deref(), Some(cookie.as_str()), "{seen:?}");
        }
    }
    for seen in rig.board.seen() {
        let held = format!("{seen:?}");
        let own = seen.target.split('/').nth(1).unwrap();
        for other in [A, B, C].iter().filter(|fleet| **fleet != own) {
            assert!(!held.contains(other), "{held}");
            assert!(!held.contains(&secret(other)), "{held}");
        }
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_host_that_does_not_answer_keeps_the_store_and_the_loops_and_later_fleets_are_not_asked()
{
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(
        &bin,
        &[(A, Mode::Silent), (B, Mode::Live), (C, Mode::Live)],
        None,
    )
    .await;
    rig.supervisor.start();
    assert!(
        until(|| [A, B, C]
            .iter()
            .all(|fleet| !rig.requests(fleet, "/alerts/feed").is_empty()))
        .await
    );
    let before: Vec<String> = [A, B, C].iter().map(|fleet| rig.cookie(fleet)).collect();
    let cleared = flag();

    let outcome = rig.sign_out(&cleared).await;

    assert_eq!(
        left(&outcome),
        [
            (A, Fate::Unreachable),
            (B, Fate::Unreachable),
            (C, Fate::Unreachable)
        ]
    );
    assert!(!cleared.load(Ordering::SeqCst));
    assert_eq!(rig.held(), 3);
    let after: Vec<String> = [A, B, C].iter().map(|fleet| rig.cookie(fleet)).collect();
    assert_eq!(before, after);
    assert!(rig.requests(B, "/api/v1/session").is_empty());
    assert!(rig.requests(C, "/api/v1/session").is_empty());
    let polls: Vec<usize> = [A, B, C]
        .iter()
        .map(|fleet| rig.requests(fleet, "/alerts/feed").len())
        .collect();
    for (at, fleet) in [A, B, C].iter().enumerate() {
        assert!(
            until(|| rig.requests(fleet, "/alerts/feed").len() > polls[at] + 1).await,
            "the loop of {fleet} is not running again"
        );
    }
    assert!(rig.sink.unpaired.lock().unwrap().is_empty());
    rig.supervisor.stop();
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_fleet_that_answers_with_a_server_error_is_left_and_the_others_are_still_signed_out() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(
        &bin,
        &[
            (A, Mode::Live),
            (B, Mode::RevokeAnswers(500)),
            (C, Mode::Live),
        ],
        None,
    )
    .await;
    let cleared = flag();

    let outcome = rig.sign_out(&cleared).await;

    assert_eq!(left(&outcome), [(B, Fate::Unreachable)]);
    assert!(!cleared.load(Ordering::SeqCst));
    assert_eq!(rig.held(), 3);
    assert_eq!(rig.requests(A, "/api/v1/session/revoke").len(), 1);
    assert_eq!(rig.requests(B, "/api/v1/session/revoke").len(), 1);
    assert_eq!(rig.requests(C, "/api/v1/session/revoke").len(), 1);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_pairing_the_host_has_already_ended_counts_as_signed_out() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    for (name, mode) in [
        ("401 on the token read", Mode::SessionGone401),
        (
            "403 with X-Hand-Error on the token read",
            Mode::SessionGoneWithError,
        ),
        ("401 on the revoke", Mode::RevokeRaced401),
        (
            "403 with X-Hand-Error on the revoke",
            Mode::RevokeRacedWithError,
        ),
    ] {
        let rig = rig(&bin, &[(A, Mode::Live), (B, mode)], None).await;
        let cleared = flag();
        let outcome = rig.sign_out(&cleared).await;
        assert_eq!(outcome, SignOut::Done, "{name}");
        assert!(cleared.load(Ordering::SeqCst), "{name}");
        assert_eq!(rig.held(), 0, "{name}");
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_host_without_the_path_is_too_old() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    for status in [404, 405] {
        let rig = rig(&bin, &[(A, Mode::RevokeAnswers(status))], None).await;
        let cleared = flag();
        let outcome = rig.sign_out(&cleared).await;
        assert_eq!(left(&outcome), [(A, Fate::TooOld)], "{status}");
        assert!(!cleared.load(Ordering::SeqCst));
        assert_eq!(rig.held(), 1);
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_plain_403_means_the_host_no_longer_recognises_this_companion() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(&bin, &[(A, Mode::SessionPlain403)], None).await;
    let cleared = flag();

    let outcome = rig.sign_out(&cleared).await;

    assert_eq!(left(&outcome), [(A, Fate::Unrecognised)]);
    assert!(!cleared.load(Ordering::SeqCst));
    assert_eq!(rig.held(), 1);
    assert!(rig.requests(A, "/api/v1/session/revoke").is_empty());
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn the_feed_is_stopped_first_so_no_no_longer_paired_notice_is_posted() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(
        &bin,
        &[(A, Mode::Live), (B, Mode::Live), (C, Mode::Live)],
        Some(C),
    )
    .await;
    rig.supervisor.start();
    assert!(until(|| [A, B, C].iter().all(|fleet| rig.cursor(fleet).is_some())).await);
    let cleared = flag();

    let outcome = rig.sign_out(&cleared).await;
    sleep(Duration::from_secs(1)).await;

    assert_eq!(outcome, SignOut::Done);
    assert!(rig.sink.unpaired.lock().unwrap().is_empty());
    assert_eq!(rig.cursor(A), None);
    let polled = rig.requests(A, "/alerts/feed").len();
    sleep(Duration::from_secs(1)).await;
    assert_eq!(rig.requests(A, "/alerts/feed").len(), polled);
    rig.supervisor.stop();
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_fleet_paired_during_the_round_is_signed_out_too_and_never_dropped_unrevoked() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(&bin, &[(A, Mode::Live), (B, Mode::Live)], Some(A)).await;
    let cleared = flag();
    let late = Paired {
        id: C.to_owned(),
        name: format!("fleet {C}"),
        value: value(C),
    };

    let (outcome, _) = tokio::join!(rig.sign_out(&cleared), async {
        assert!(until(|| !rig.requests(A, "/api/v1/session/revoke").is_empty()).await);
        rig.sessions.pair(&rig.host, None, &[late]).unwrap();
    });

    assert_eq!(outcome, SignOut::Done);
    assert!(cleared.load(Ordering::SeqCst));
    assert_eq!(rig.held(), 0);
    let revokes = rig.requests(C, "/api/v1/session/revoke");
    assert_eq!(revokes.len(), 1);
    assert_eq!(revokes[0].csrf.as_deref(), Some(csrf(C).as_str()));
    assert_eq!(revokes[0].cookie.as_deref(), Some(rig.cookie(C).as_str()));
}
