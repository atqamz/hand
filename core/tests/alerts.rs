#![cfg(target_os = "linux")]

mod support;

use std::fs;
use std::io;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use hand_android_core::{Alert, AlertSink, CookieSource, Dialer, Feed, Fleet, Stop, Supervisor};
use serde_json::{Value, json};
use support::{FeedBoard, Reply, Seen, Sidecar, SidecarOptions, TempDir, dialer, sidecar_bin};
use tokio::task::JoinHandle;
use tokio::time::sleep;

const FLEET: &str = "f0123456789ab";
const OTHER: &str = "f999999999999";
const SECRET: &str = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210";
const COOKIES: &str = "session=zzz; hand_phone_f0123456789ab=abc123; hand_phone_f999999999999=nope";
const WAIT: Duration = Duration::from_secs(15);

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

#[derive(Clone, Default)]
struct Sink(Arc<Mutex<Delivered>>);

#[derive(Default)]
struct Delivered {
    alerts: Vec<(Fleet, Alert)>,
    unpaired: Vec<(String, Option<String>)>,
    fail_on: Option<u64>,
}

impl Sink {
    fn alerts(&self) -> Vec<(Fleet, Alert)> {
        self.0.lock().unwrap().alerts.clone()
    }

    fn fail_on(&self, seq: u64) {
        self.0.lock().unwrap().fail_on = Some(seq);
    }

    fn unpaired(&self) -> Vec<(String, Option<String>)> {
        self.0.lock().unwrap().unpaired.clone()
    }
}

impl AlertSink for Sink {
    async fn alert(&self, fleet: &Fleet, alert: &Alert) -> io::Result<()> {
        let mut delivered = self.0.lock().unwrap();
        if delivered.fail_on == Some(alert.seq) {
            return Err(io::Error::other("the notification was refused"));
        }
        delivered.alerts.push((fleet.clone(), alert.clone()));
        Ok(())
    }

    async fn unpaired(&self, fleet: &str, name: Option<&str>) {
        self.0
            .lock()
            .unwrap()
            .unpaired
            .push((fleet.to_owned(), name.map(str::to_owned)));
    }
}

struct Cookies;

impl CookieSource for Cookies {
    async fn cookies(&self) -> io::Result<Option<String>> {
        Ok(Some(COOKIES.to_owned()))
    }
}

#[derive(Clone, Default)]
struct Shared(Arc<Mutex<Option<String>>>);

impl Shared {
    fn set(&self, header: Option<&str>) {
        *self.0.lock().unwrap() = header.map(str::to_owned);
    }
}

impl CookieSource for Shared {
    async fn cookies(&self) -> io::Result<Option<String>> {
        Ok(self.0.lock().unwrap().clone())
    }
}

struct Header(Option<&'static str>);

impl CookieSource for Header {
    async fn cookies(&self) -> io::Result<Option<String>> {
        Ok(self.0.map(str::to_owned))
    }
}

struct FailsOnce(AtomicUsize);

impl CookieSource for FailsOnce {
    async fn cookies(&self) -> io::Result<Option<String>> {
        if self.0.fetch_add(1, Ordering::SeqCst) == 0 {
            return Err(io::Error::other("the cookie store is not readable"));
        }
        Ok(Some(COOKIES.to_owned()))
    }
}

const GONE: &str =
    "This%20phone%20is%20no%20longer%20paired%20with%20this%20fleet.%20Pair%20it%20again";

fn alert(seq: u64, kind: &str, reference: &str, text: &str, link: &str) -> Value {
    json!({"seq": seq, "at": "2026-11-10T03:12:00Z", "kind": kind, "ref": reference, "text": text, "link": link})
}

fn answer_for(fleet: &str, cursor: u64, more: bool, alerts: Vec<Value>) -> Reply {
    Reply::json(
        json!({"version": 1, "fleet": {"id": fleet, "name": "hand"}, "cursor": cursor, "more": more, "alerts": alerts})
            .to_string(),
    )
}

fn answer(cursor: u64, alerts: Vec<Value>) -> Reply {
    answer_for(FLEET, cursor, false, alerts)
}

fn link(page: &str) -> String {
    format!("handboard://open/{FLEET}/{page}")
}

struct Harness {
    board: FeedBoard,
    dialer: Dialer,
    dir: TempDir,
    sink: Sink,
    _sidecar: Sidecar,
}

impl Harness {
    async fn start(
        bin: &std::path::Path,
        idle: u64,
        responder: impl Fn(usize, &Seen) -> Reply + Send + Sync + 'static,
    ) -> Self {
        let board = FeedBoard::start(SECRET, responder).await;
        let sidecar = Sidecar::start(
            bin,
            SidecarOptions::new(board.addr()).secret(SECRET).idle(idle),
        );
        let dialer = dialer(sidecar.target()).await;
        Self {
            board,
            dialer,
            dir: TempDir::new(),
            sink: Sink::default(),
            _sidecar: sidecar,
        }
    }

    fn feed(&self) -> Feed {
        Feed::new(self.dialer.clone(), self.dir.path().join("alerts"))
    }

    fn run(&self, feed: Feed) -> JoinHandle<Stop> {
        self.run_with(feed, Cookies)
    }

    fn run_with(
        &self,
        feed: Feed,
        cookies: impl CookieSource + Send + 'static,
    ) -> JoinHandle<Stop> {
        self.run_after(feed, cookies, false)
    }

    fn run_after(
        &self,
        feed: Feed,
        cookies: impl CookieSource + Send + 'static,
        refused_before: bool,
    ) -> JoinHandle<Stop> {
        let sink = self.sink.clone();
        tokio::spawn(async move { feed.run(FLEET, &cookies, &sink, refused_before).await })
    }

    fn cursor_path(&self) -> std::path::PathBuf {
        self.dir
            .path()
            .join("alerts")
            .join(format!("{FLEET}.cursor"))
    }

    fn cursor(&self) -> Option<String> {
        fs::read_to_string(self.cursor_path()).ok()
    }

    fn keep_cursor(&self, cursor: u64) {
        fs::create_dir_all(self.dir.path().join("alerts")).unwrap();
        fs::write(self.cursor_path(), cursor.to_string()).unwrap();
    }
}

#[tokio::test]
async fn alerts_reach_the_sink_and_the_cursor_follows() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(5, vec![]),
        1 => answer(
            7,
            vec![
                alert(
                    6,
                    "decision.asked",
                    "d3",
                    "t3 decision: Keep it?",
                    &link("decision/d3"),
                ),
                alert(7, "future.kind", "supervisor", "s1 stalled", &link("")),
            ],
        ),
        _ => answer(7, vec![]).held(Duration::from_secs(2)),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(
        wait_for(|| harness.board.seen().len() >= 3 && harness.cursor().as_deref() == Some("7"))
            .await
    );
    task.abort();

    let seen = harness.board.seen();
    assert_eq!(seen[0].target, format!("/{FLEET}/alerts/feed?wait=25"));
    assert_eq!(
        seen[1].target,
        format!("/{FLEET}/alerts/feed?after=5&wait=25")
    );
    assert_eq!(
        seen[2].target,
        format!("/{FLEET}/alerts/feed?after=7&wait=25")
    );
    for request in &seen {
        assert_eq!(
            request.cookie.as_deref(),
            Some("hand_phone_f0123456789ab=abc123")
        );
        assert_eq!(request.host.as_deref(), Some("localhost"));
        assert_eq!(request.connection.as_deref(), Some("close"));
    }
    let delivered = harness.sink.alerts();
    assert_eq!(delivered.len(), 2);
    assert_eq!(
        delivered[0],
        (
            Fleet {
                id: FLEET.to_owned(),
                name: "hand".to_owned()
            },
            Alert {
                seq: 6,
                kind: "decision.asked".to_owned(),
                reference: "d3".to_owned(),
                text: "t3 decision: Keep it?".to_owned(),
                link: Some(link("decision/d3")),
            }
        )
    );
    assert_eq!(delivered[1].1.kind, "future.kind");
    assert_eq!(delivered[1].1.link, Some(link("")));
}

#[tokio::test]
async fn a_more_answer_is_followed_at_once_without_a_wait() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(0, vec![]),
        1 => answer_for(
            FLEET,
            1,
            true,
            vec![alert(
                1,
                "attempt.failed",
                "a1",
                "a1 failed",
                &link("attempt/a1"),
            )],
        ),
        2 => answer(
            2,
            vec![alert(
                2,
                "attempt.failed",
                "a2",
                "a2 failed",
                &link("attempt/a2"),
            )],
        ),
        _ => answer(2, vec![]).held(Duration::from_secs(2)),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.sink.alerts().len() == 2).await);
    task.abort();
    let seen = harness.board.seen();
    assert_eq!(seen[2].target, format!("/{FLEET}/alerts/feed?after=1"));
    assert!(seen[2].at - seen[1].at < Duration::from_millis(900));
}

#[tokio::test]
async fn an_empty_heartbeat_is_followed_by_the_next_poll_at_once() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(3, vec![]),
        _ => answer(3, vec![]).held(Duration::from_secs(1)),
    })
    .await;
    let task = harness.run(harness.feed().with_wait(Duration::from_secs(1)));

    assert!(wait_for(|| harness.board.seen().len() >= 4).await);
    task.abort();
    let seen = harness.board.seen();
    assert_eq!(
        seen[1].target,
        format!("/{FLEET}/alerts/feed?after=3&wait=1")
    );
    for pair in seen[1..].windows(2) {
        let gap = pair[1].at - pair[0].at;
        assert!(gap >= Duration::from_millis(950), "{gap:?}");
        assert!(gap < Duration::from_millis(1500), "{gap:?}");
    }
    assert!(harness.sink.alerts().is_empty());
    assert_eq!(harness.cursor().as_deref(), Some("3"));
}

#[tokio::test]
async fn a_server_error_or_throttle_backs_off_one_two_then_resets_on_success() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => Reply::status(503),
        1 => Reply::status(429),
        2 => answer(1, vec![]),
        3 => Reply::status(500),
        _ => answer(1, vec![]).held(Duration::from_secs(2)),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.board.seen().len() >= 5).await);
    task.abort();
    let seen = harness.board.seen();
    let gaps: Vec<Duration> = seen
        .windows(2)
        .map(|pair| pair[1].at - pair[0].at)
        .collect();
    assert!(
        gaps[0] >= Duration::from_secs(1) && gaps[0] < Duration::from_millis(1500),
        "{gaps:?}"
    );
    assert!(
        gaps[1] >= Duration::from_secs(2) && gaps[1] < Duration::from_millis(2800),
        "{gaps:?}"
    );
    assert!(
        gaps[3] >= Duration::from_secs(1) && gaps[3] < Duration::from_millis(1500),
        "{gaps:?}"
    );
}

#[tokio::test]
async fn a_connection_the_laptop_closed_with_code_two_is_dialed_again() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 1, |n, _| match n {
        0 => Reply::status(503),
        1 => Reply::status(503),
        2 => answer(0, vec![]),
        3 => answer(
            1,
            vec![alert(
                1,
                "attempt.failed",
                "a1",
                "a1 failed",
                &link("attempt/a1"),
            )],
        ),
        _ => answer(1, vec![]).held(Duration::from_millis(500)),
    })
    .await;
    let first = harness.dialer.connection().await.unwrap().stable_id();
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.sink.alerts().len() == 1).await);
    task.abort();
    let second = harness.dialer.connection().await.unwrap().stable_id();
    assert_ne!(first, second);
}

#[tokio::test]
async fn a_no_longer_paired_refusal_stops_the_fleet_and_the_second_one_forgets_its_cursor() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(4, vec![]),
        _ => Reply::status(403)
            .header("X-Hand-Error", GONE)
            .header("Transfer-Encoding", "chunked"),
    })
    .await;
    let task = harness.run(harness.feed());

    let stop = tokio::time::timeout(WAIT, task).await.unwrap().unwrap();
    assert_eq!(stop, Stop::BoardGone);
    assert_eq!(
        harness.sink.unpaired(),
        vec![(FLEET.to_owned(), Some("hand".to_owned()))]
    );
    assert_eq!(harness.cursor().as_deref(), Some("4"));
    sleep(Duration::from_millis(1500)).await;
    assert_eq!(harness.board.seen().len(), 2);

    let again = harness.run_after(harness.feed(), Cookies, true);
    let stop = tokio::time::timeout(WAIT, again).await.unwrap().unwrap();
    assert_eq!(stop, Stop::BoardGone);
    assert_eq!(harness.sink.unpaired().len(), 1);
    assert_eq!(harness.cursor(), None);
}

#[tokio::test]
async fn a_first_poll_refused_names_no_fleet() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| {
        Reply::status(403).header("X-Hand-Error", GONE)
    })
    .await;
    let task = harness.run(harness.feed());

    let stop = tokio::time::timeout(WAIT, task).await.unwrap().unwrap();
    assert_eq!(stop, Stop::BoardGone);
    assert_eq!(harness.sink.unpaired(), vec![(FLEET.to_owned(), None)]);
}

#[tokio::test]
async fn any_other_refusal_backs_off_and_keeps_the_cursor() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(4, vec![]),
        1 => Reply::status(404),
        2 => Reply::status(403),
        3 => Reply::status(403).header("X-Hand-Error", "Pair%20this%20phone%20first"),
        _ => answer(4, vec![]).held(Duration::from_secs(2)),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.board.seen().len() >= 5).await);
    task.abort();
    assert!(harness.sink.unpaired().is_empty());
    assert_eq!(harness.cursor().as_deref(), Some("4"));
    let seen = harness.board.seen();
    let gap = seen[3].at - seen[2].at;
    assert!(gap >= Duration::from_millis(1400), "{gap:?}");
}

#[tokio::test]
async fn a_chunked_answer_is_not_read() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(1, vec![]),
        1 => answer(2, vec![alert(2, "attempt.failed", "a1", "a1 failed", "")])
            .header("Transfer-Encoding", "chunked"),
        _ => answer(1, vec![]).held(Duration::from_secs(2)),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.board.seen().len() >= 3).await);
    task.abort();
    assert!(harness.sink.alerts().is_empty());
    assert_eq!(harness.cursor().as_deref(), Some("1"));
}

#[tokio::test]
async fn a_well_formed_chunked_answer_is_not_read_either() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let body =
        String::from_utf8(answer(2, vec![alert(2, "attempt.failed", "a1", "a1 failed", "")]).body)
            .unwrap();
    let chunked = format!(
        "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n{:x}\r\n{body}\r\n0\r\n\r\n",
        body.len()
    );
    let harness = Harness::start(&bin, 60, move |n, _| match n {
        0 => answer(1, vec![]),
        1 => Reply::raw(chunked.as_bytes()),
        _ => answer(1, vec![]).held(Duration::from_secs(2)),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.board.seen().len() >= 3).await);
    task.abort();
    assert!(harness.sink.alerts().is_empty());
    assert_eq!(harness.cursor().as_deref(), Some("1"));
}

#[tokio::test]
async fn a_200_without_a_length_is_not_read() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let body =
        String::from_utf8(answer(2, vec![alert(2, "attempt.failed", "a1", "a1 failed", "")]).body)
            .unwrap();
    let unframed = format!("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{body}");
    let harness = Harness::start(&bin, 60, move |n, _| match n {
        0 => answer(1, vec![]),
        1 => Reply::raw(unframed.as_bytes()),
        _ => answer(1, vec![]).held(Duration::from_secs(2)),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.board.seen().len() >= 3).await);
    task.abort();
    assert!(harness.sink.alerts().is_empty());
    assert_eq!(harness.cursor().as_deref(), Some("1"));
}

#[tokio::test]
async fn a_no_longer_paired_refusal_is_recognised_when_its_body_is_cut_short() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let cut =
        format!("HTTP/1.1 403 Forbidden\r\nX-Hand-Error: {GONE}\r\nContent-Length: 100\r\n\r\nabc");
    let harness = Harness::start(&bin, 60, move |n, _| match n {
        0 => answer(4, vec![]),
        _ => Reply::raw(cut.as_bytes()),
    })
    .await;
    let task = harness.run(harness.feed());

    let stop = tokio::time::timeout(WAIT, task).await.unwrap().unwrap();
    assert_eq!(stop, Stop::BoardGone);
    assert_eq!(
        harness.sink.unpaired(),
        vec![(FLEET.to_owned(), Some("hand".to_owned()))]
    );
    assert_eq!(harness.cursor().as_deref(), Some("4"));
}

#[tokio::test]
async fn no_cookie_header_and_no_own_cookie_stop_the_fleet_without_a_request() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    for header in [None, Some("session=zzz; hand_phone_f999999999999=nope")] {
        let harness = Harness::start(&bin, 60, |_, _| answer(4, vec![])).await;
        harness.keep_cursor(4);
        let task = harness.run_with(harness.feed(), Header(header));

        let stop = tokio::time::timeout(WAIT, task).await.unwrap().unwrap();
        assert_eq!(stop, Stop::CookieGone);
        assert_eq!(harness.sink.unpaired(), vec![(FLEET.to_owned(), None)]);
        assert_eq!(harness.cursor(), None, "{header:?}");
        assert!(harness.board.seen().is_empty(), "{header:?}");
    }
}

#[tokio::test]
async fn a_cookie_store_that_cannot_be_read_is_retried_with_backoff() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| {
        answer(1, vec![]).held(Duration::from_secs(2))
    })
    .await;
    harness.keep_cursor(1);
    let started = Instant::now();
    let task = harness.run_with(harness.feed(), FailsOnce(AtomicUsize::new(0)));

    assert!(wait_for(|| !harness.board.seen().is_empty()).await);
    task.abort();
    assert!(started.elapsed() >= Duration::from_millis(900));
    assert!(harness.sink.unpaired().is_empty());
    assert_eq!(harness.cursor().as_deref(), Some("1"));
}

#[tokio::test]
async fn a_sink_that_fails_leaves_the_cursor_and_the_next_request_repeats_it() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let batch = || {
        answer(
            3,
            (1..=3)
                .map(|seq| alert(seq, "attempt.failed", "a1", &format!("failed {seq}"), ""))
                .collect(),
        )
    };
    let harness = Harness::start(&bin, 60, move |n, _| match n {
        0 => answer(0, vec![]),
        _ => batch(),
    })
    .await;
    harness.sink.fail_on(2);
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.board.seen().len() >= 4).await);
    task.abort();
    let seen = harness.board.seen();
    for request in &seen[1..] {
        assert_eq!(
            request.target,
            format!("/{FLEET}/alerts/feed?after=0&wait=25")
        );
    }
    assert_eq!(harness.cursor().as_deref(), Some("0"));
    let seqs: Vec<u64> = harness
        .sink
        .alerts()
        .iter()
        .map(|(_, alert)| alert.seq)
        .collect();
    assert!(
        seqs.len() >= 2 && seqs.iter().all(|seq| *seq == 1),
        "{seqs:?}"
    );
}

#[tokio::test]
async fn an_immediate_answer_is_never_followed_within_a_second() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| answer(3, vec![])).await;
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.board.seen().len() >= 4).await);
    task.abort();
    let seen = harness.board.seen();
    for pair in seen[1..].windows(2) {
        let gap = pair[1].at - pair[0].at;
        assert!(gap >= Duration::from_millis(950), "{gap:?}");
    }
}

#[tokio::test]
async fn a_valid_answer_longer_than_the_cap_is_not_read() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let padded = format!(
        "{}{}",
        String::from_utf8(answer(2, vec![alert(2, "attempt.failed", "a1", "a1 failed", "")]).body)
            .unwrap(),
        " ".repeat(70_000)
    );
    let harness = Harness::start(&bin, 60, move |n, _| match n {
        0 => answer(1, vec![]),
        1 => Reply::json(padded.clone()),
        _ => answer(1, vec![]).held(Duration::from_secs(2)),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.board.seen().len() >= 3).await);
    task.abort();
    let seen = harness.board.seen();
    assert_eq!(
        seen[2].target,
        format!("/{FLEET}/alerts/feed?after=1&wait=25")
    );
    assert!(harness.sink.alerts().is_empty());
    assert_eq!(harness.cursor().as_deref(), Some("1"));
}

#[tokio::test]
async fn the_cursor_survives_a_restart() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(5, vec![]),
        1 => answer(
            6,
            vec![alert(
                6,
                "attempt.failed",
                "a1",
                "a1 failed",
                &link("attempt/a1"),
            )],
        ),
        _ => answer(6, vec![]).held(Duration::from_secs(2)),
    })
    .await;
    let first = harness.run(harness.feed());
    assert!(wait_for(|| harness.cursor().as_deref() == Some("6")).await);
    first.abort();
    let _ = first.await;
    let before = harness.board.seen().len();

    let second = harness.run(harness.feed());
    assert!(wait_for(|| harness.board.seen().len() > before).await);
    second.abort();
    let seen = harness.board.seen();
    assert_eq!(
        seen[before].target,
        format!("/{FLEET}/alerts/feed?after=6&wait=25")
    );
    assert_eq!(harness.sink.alerts().len(), 1);
}

#[tokio::test]
async fn a_hostile_feed_moves_nothing_and_shows_no_foreign_link() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let oversized = "x".repeat(70_000);
    let harness = Harness::start(&bin, 60, move |n, _| match n {
        0 => answer(1, vec![]),
        1 => Reply::json(oversized.clone()),
        2 => answer(2, vec![alert(2, "attempt.failed", "a1", "cut", &link("attempt/a1"))]).declaring(500),
        3 => answer_for(OTHER, 9, false, vec![alert(2, "attempt.failed", "a1", "other fleet", &link("attempt/a1"))]),
        4 => answer(
            4,
            vec![
                alert(2, "attempt.failed", "a1", "pairing link", "handboard://c#v1.4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6"),
                alert(3, "attempt.failed", "a2", "other fleet link", &format!("handboard://open/{OTHER}/attempt/a2")),
                alert(4, "decision.asked", "d3", &format!("line one\nline two {}", "y".repeat(400)), &link("decision/d3")),
            ],
        ),
        _ => answer(4, vec![]).held(Duration::from_secs(2)),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(wait_for(|| harness.sink.alerts().len() == 3).await);
    task.abort();
    let seen = harness.board.seen();
    for request in &seen[1..=4] {
        assert_eq!(
            request.target,
            format!("/{FLEET}/alerts/feed?after=1&wait=25")
        );
    }
    let delivered = harness.sink.alerts();
    assert_eq!(
        delivered
            .iter()
            .map(|(_, alert)| alert.link.clone())
            .collect::<Vec<_>>(),
        vec![None, None, Some(link("decision/d3"))]
    );
    assert_eq!(delivered[2].1.text.chars().count(), 200);
    assert!(delivered[2].1.text.starts_with("line one line two "));
    assert_eq!(harness.cursor().as_deref(), Some("4"));
}

const OWN: &str = "hand_phone_f0123456789ab=abc123";
const TICK: Duration = Duration::from_millis(100);

#[tokio::test]
async fn the_supervisor_starts_a_fleet_when_its_cookie_appears_and_runs_one_loop() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(5, vec![]),
        _ => answer(5, vec![]).held(Duration::from_millis(500)),
    })
    .await;
    let cookies = Shared::default();
    let supervisor = Supervisor::new(harness.feed(), cookies.clone(), harness.sink.clone(), TICK);
    supervisor.start();
    supervisor.start();

    sleep(Duration::from_millis(600)).await;
    assert!(harness.board.seen().is_empty());
    cookies.set(Some(&format!("session=zzz; {OWN}")));
    assert!(wait_for(|| harness.board.seen().len() >= 2).await);
    sleep(Duration::from_millis(2000)).await;
    supervisor.stop();
    sleep(Duration::from_millis(500)).await;

    let requests = harness.board.seen().len();
    assert!(requests <= 5, "{requests} requests in about 3 seconds");
    sleep(Duration::from_millis(1500)).await;
    assert_eq!(harness.board.seen().len(), requests);
}

#[tokio::test]
async fn forgetting_stops_the_loops_and_the_cursors_and_posts_nothing() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(5, vec![]),
        _ => answer(5, vec![]).held(Duration::from_millis(300)),
    })
    .await;
    let cookies = Shared::default();
    cookies.set(Some(OWN));
    let supervisor = Supervisor::new(harness.feed(), cookies.clone(), harness.sink.clone(), TICK);
    supervisor.start();
    assert!(wait_for(|| harness.cursor().as_deref() == Some("5")).await);

    supervisor.forget_all();
    assert_eq!(harness.cursor(), None);
    sleep(Duration::from_millis(500)).await;
    let requests = harness.board.seen().len();
    sleep(Duration::from_millis(2000)).await;
    assert_eq!(harness.board.seen().len(), requests);
    assert!(harness.sink.unpaired().is_empty());

    cookies.set(None);
    sleep(Duration::from_millis(500)).await;
    cookies.set(Some(OWN));
    assert!(wait_for(|| harness.board.seen().len() > requests).await);
    supervisor.stop();
}

#[tokio::test]
async fn a_fleet_the_board_refused_is_not_started_again_until_its_cookie_returns() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(4, vec![]),
        1 => Reply::status(403).header("X-Hand-Error", GONE),
        _ => answer(4, vec![]),
    })
    .await;
    let cookies = Shared::default();
    cookies.set(Some(OWN));
    let supervisor = Supervisor::new(harness.feed(), cookies.clone(), harness.sink.clone(), TICK);
    supervisor.start();
    assert!(wait_for(|| harness.sink.unpaired().len() == 1).await);
    sleep(Duration::from_millis(1500)).await;
    assert_eq!(harness.sink.unpaired().len(), 1);
    assert_eq!(harness.board.seen().len(), 2);

    cookies.set(None);
    sleep(Duration::from_millis(500)).await;
    cookies.set(Some(OWN));
    assert!(wait_for(|| harness.board.seen().len() > 2).await);
    supervisor.stop();
}

#[derive(Clone)]
struct Blocking {
    sink: Sink,
    entered: Arc<AtomicBool>,
}

impl AlertSink for Blocking {
    async fn alert(&self, fleet: &Fleet, alert: &Alert) -> io::Result<()> {
        self.entered.store(true, Ordering::SeqCst);
        std::thread::sleep(Duration::from_millis(400));
        self.sink.alert(fleet, alert).await
    }

    async fn unpaired(&self, fleet: &str, name: Option<&str>) {
        AlertSink::unpaired(&self.sink, fleet, name).await;
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn forgetting_during_a_poll_in_flight_posts_nothing_more_and_leaves_no_cursor() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    for batch in [1u64, 2] {
        let harness = Harness::start(&bin, 60, move |n, _| match n {
            0 => answer(5, vec![]),
            1 => answer(
                5 + batch,
                (6..=5 + batch)
                    .map(|seq| alert(seq, "attempt.failed", "a1", "failed", ""))
                    .collect(),
            ),
            _ => answer(5 + batch, vec![]).held(Duration::from_millis(300)),
        })
        .await;
        let cookies = Shared::default();
        cookies.set(Some(OWN));
        let entered = Arc::new(AtomicBool::new(false));
        let blocking = Blocking {
            sink: harness.sink.clone(),
            entered: entered.clone(),
        };
        let supervisor = Supervisor::new(harness.feed(), cookies.clone(), blocking, TICK);
        supervisor.start();
        assert!(wait_for(|| entered.load(Ordering::SeqCst)).await);

        supervisor.forget_all();
        sleep(Duration::from_millis(1000)).await;
        supervisor.stop();

        assert_eq!(harness.cursor(), None, "batch of {batch}");
        assert_eq!(harness.sink.alerts().len(), 1, "batch of {batch}");
    }
}

#[tokio::test]
async fn a_changed_cookie_value_restarts_a_refused_fleet_at_once() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(4, vec![]),
        1 => Reply::status(403).header("X-Hand-Error", GONE),
        _ => answer(4, vec![]).held(Duration::from_millis(300)),
    })
    .await;
    let cookies = Shared::default();
    cookies.set(Some(OWN));
    let supervisor = Supervisor::new(harness.feed(), cookies.clone(), harness.sink.clone(), TICK);
    supervisor.start();
    assert!(wait_for(|| harness.sink.unpaired().len() == 1).await);
    sleep(Duration::from_millis(500)).await;
    assert_eq!(harness.board.seen().len(), 2);

    cookies.set(Some("hand_phone_f0123456789ab=def456"));
    sleep(Duration::from_millis(1000)).await;
    supervisor.stop();
    assert!(harness.board.seen().len() > 2);
    let seen = harness.board.seen();
    assert_eq!(
        seen[2].cookie.as_deref(),
        Some("hand_phone_f0123456789ab=def456")
    );
}

#[tokio::test]
async fn a_refused_fleet_is_tried_again_and_only_the_second_refusal_forgets_its_cursor() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(4, vec![]),
        1 | 2 => Reply::status(403).header("X-Hand-Error", GONE),
        _ => answer(4, vec![]).held(Duration::from_millis(300)),
    })
    .await;
    let cookies = Shared::default();
    cookies.set(Some(OWN));
    let supervisor = Supervisor::new(harness.feed(), cookies.clone(), harness.sink.clone(), TICK);
    supervisor.start();
    assert!(wait_for(|| harness.sink.unpaired().len() == 1).await);
    assert_eq!(harness.cursor().as_deref(), Some("4"));
    sleep(Duration::from_millis(1500)).await;
    assert_eq!(harness.board.seen().len(), 2);

    assert!(wait_for(|| harness.board.seen().len() == 3).await);
    assert!(wait_for(|| harness.cursor().is_none()).await);
    assert_eq!(harness.sink.unpaired().len(), 1);

    assert!(wait_for(|| harness.board.seen().len() >= 4).await);
    supervisor.stop();
    assert_eq!(harness.sink.unpaired().len(), 1);
}

struct BlocksThenNone(Arc<AtomicBool>);

impl CookieSource for BlocksThenNone {
    async fn cookies(&self) -> io::Result<Option<String>> {
        self.0.store(true, Ordering::SeqCst);
        std::thread::sleep(Duration::from_millis(400));
        Ok(None)
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn forgetting_while_the_cookie_read_blocks_posts_no_unpaired_notice() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| answer(4, vec![])).await;
    let feed = Arc::new(harness.feed());
    let reading = Arc::new(AtomicBool::new(false));
    let task = {
        let feed = feed.clone();
        let sink = harness.sink.clone();
        let cookies = BlocksThenNone(reading.clone());
        tokio::spawn(async move { feed.run(FLEET, &cookies, &sink, false).await })
    };
    assert!(wait_for(|| reading.load(Ordering::SeqCst)).await);

    feed.forget_cursors();

    assert_eq!(task.await.unwrap(), Stop::Forgotten);
    assert!(harness.sink.unpaired().is_empty());
}
