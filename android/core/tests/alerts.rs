#![cfg(target_os = "linux")]

mod support;

use std::fs;
use std::io;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::mpsc;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use hand_android_core::{
    Alert, AlertSink, CookieSource, Dialer, Feed, Fleet, IDLE, Once, Stop, Supervisor,
};
use serde_json::{Value, json};
use support::{
    FeedBoard, Manual, ManualClock, MemoryJar, Reply, Seen, Sidecar, SidecarOptions, TempDir,
    dialer, endpoint, sessions, sidecar_bin,
};
use tokio::task::JoinHandle;
use tokio::time::{sleep, timeout};

const FLEET: &str = "f0123456789ab";
const OTHER: &str = "f999999999999";
const SECRET: &str = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210";
const COOKIES: &str = "session=zzz; hand_phone_f0123456789ab=abc123; hand_phone_f999999999999=nope";
const WAIT: Duration = Duration::from_secs(15);

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
    clock: Arc<ManualClock>,
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
        Self::start_on(bin, idle, ManualClock::new(), responder).await
    }

    async fn start_on(
        bin: &std::path::Path,
        idle: u64,
        clock: Arc<ManualClock>,
        responder: impl Fn(usize, &Seen) -> Reply + Send + Sync + 'static,
    ) -> Self {
        let board = FeedBoard::start(SECRET, responder).await;
        let sidecar = Sidecar::start(
            bin,
            SidecarOptions::new(board.addr()).secret(SECRET).idle(idle),
        );
        let dialer = dialer(sidecar.target()).await;
        Self {
            clock,
            board,
            dialer,
            dir: TempDir::new(),
            sink: Sink::default(),
            _sidecar: sidecar,
        }
    }

    fn feed(&self) -> Feed {
        Feed::new(self.dialer.clone(), self.dir.path().join("alerts"))
            .with_clock(Arc::new(Manual(self.clock.clone())))
    }

    async fn still(&self, mut condition: impl FnMut() -> bool) -> bool {
        timeout(WAIT, async {
            while !condition() {
                sleep(Duration::from_millis(1)).await;
            }
        })
        .await
        .is_ok()
    }

    async fn until(&self, mut condition: impl FnMut() -> bool) -> bool {
        timeout(WAIT, async {
            while !condition() {
                if self.clock.advance_to_next() {
                    tokio::task::yield_now().await;
                } else {
                    sleep(Duration::from_millis(1)).await;
                }
            }
        })
        .await
        .is_ok()
    }

    async fn parked(&self) -> Duration {
        let mut left = Vec::new();
        assert!(
            self.still(|| {
                left = self.clock.waiting();
                left.len() == 1
            })
            .await,
            "{left:?}"
        );
        left[0]
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
        _ => answer(7, vec![]).unanswered(),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(
        harness
            .until(|| harness.board.seen().len() >= 3 && harness.cursor().as_deref() == Some("7"))
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
        _ => answer(2, vec![]).unanswered(),
    })
    .await;
    let task = harness.run(harness.feed());

    let first = harness.parked().await;
    harness.clock.advance(first);
    assert!(harness.still(|| harness.sink.alerts().len() == 2).await);
    task.abort();
    let seen = harness.board.seen();
    assert_eq!(seen[2].target, format!("/{FLEET}/alerts/feed?after=1"));
}

#[tokio::test]
async fn an_empty_heartbeat_is_followed_by_the_next_poll_at_once() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let clock = ManualClock::new();
    let held = clock.clone();
    let harness = Harness::start_on(&bin, 60, clock, move |n, _| {
        if n > 0 {
            held.advance(Duration::from_secs(1));
        }
        answer(3, vec![])
    })
    .await;
    let task = harness.run(harness.feed().with_wait(Duration::from_secs(1)));

    let first = harness.parked().await;
    assert_eq!(first, Duration::from_secs(1));
    harness.clock.advance(first);
    assert!(harness.still(|| harness.board.seen().len() >= 4).await);
    task.abort();
    let seen = harness.board.seen();
    for request in &seen[1..] {
        assert_eq!(
            request.target,
            format!("/{FLEET}/alerts/feed?after=3&wait=1")
        );
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
        _ => answer(1, vec![]).unanswered(),
    })
    .await;
    let task = harness.run(harness.feed());

    let mut pauses = Vec::new();
    for _ in 0..4 {
        let pause = harness.parked().await;
        pauses.push(pause);
        harness.clock.advance(pause);
    }
    assert!(harness.until(|| harness.board.seen().len() >= 5).await);
    task.abort();
    assert!(
        pauses[0] >= Duration::from_secs(1) && pauses[0] <= Duration::from_millis(1250),
        "{pauses:?}"
    );
    assert!(
        pauses[1] >= Duration::from_secs(2) && pauses[1] <= Duration::from_millis(2500),
        "{pauses:?}"
    );
    assert_eq!(pauses[2], Duration::from_secs(1), "{pauses:?}");
    assert!(
        pauses[3] >= Duration::from_secs(1) && pauses[3] <= Duration::from_millis(1250),
        "{pauses:?}"
    );
}

#[tokio::test]
async fn a_connection_the_host_closed_with_code_two_is_dialed_again() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 1, |n, _| match n {
        0 => answer(0, vec![]),
        1 => answer(
            1,
            vec![alert(
                1,
                "attempt.failed",
                "a1",
                "a1 failed",
                &link("attempt/a1"),
            )],
        ),
        _ => answer(1, vec![]).unanswered(),
    })
    .await;
    let first = harness.dialer.connection().await.unwrap();
    let closed = timeout(WAIT, first.closed()).await.unwrap();
    assert!(matches!(
        closed,
        iroh::endpoint::ConnectionError::ApplicationClosed(close) if close.error_code.into_inner() == IDLE
    ));
    let task = harness.run(harness.feed());

    assert!(harness.until(|| harness.sink.alerts().len() == 1).await);
    task.abort();
    let second = harness.dialer.connection().await.unwrap();
    assert_ne!(first.stable_id(), second.stable_id());
    assert!(second.close_reason().is_none());
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
    let pacing = harness.parked().await;
    harness.clock.advance(pacing);

    let stop = tokio::time::timeout(WAIT, task).await.unwrap().unwrap();
    assert_eq!(stop, Stop::BoardGone);
    assert_eq!(
        harness.sink.unpaired(),
        vec![(FLEET.to_owned(), Some("hand".to_owned()))]
    );
    assert_eq!(harness.cursor().as_deref(), Some("4"));
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
        _ => answer(4, vec![]).unanswered(),
    })
    .await;
    let task = harness.run(harness.feed());

    let mut pauses = Vec::new();
    for _ in 0..4 {
        let pause = harness.parked().await;
        pauses.push(pause);
        harness.clock.advance(pause);
    }
    assert!(harness.until(|| harness.board.seen().len() >= 5).await);
    task.abort();
    assert!(harness.sink.unpaired().is_empty());
    assert_eq!(harness.cursor().as_deref(), Some("4"));
    assert_eq!(pauses[0], Duration::from_secs(1), "{pauses:?}");
    assert!(
        pauses[1] >= Duration::from_secs(1) && pauses[1] <= Duration::from_millis(1250),
        "{pauses:?}"
    );
    assert!(
        pauses[2] >= Duration::from_secs(2) && pauses[2] <= Duration::from_millis(2500),
        "{pauses:?}"
    );
    assert!(
        pauses[3] >= Duration::from_secs(4) && pauses[3] <= Duration::from_secs(5),
        "{pauses:?}"
    );
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
        _ => answer(1, vec![]).unanswered(),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(harness.until(|| harness.board.seen().len() >= 3).await);
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
        _ => answer(1, vec![]).unanswered(),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(harness.until(|| harness.board.seen().len() >= 3).await);
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
        _ => answer(1, vec![]).unanswered(),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(harness.until(|| harness.board.seen().len() >= 3).await);
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

    let pacing = harness.parked().await;
    harness.clock.advance(pacing);

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
    let harness = Harness::start(&bin, 60, |_, _| answer(1, vec![]).unanswered()).await;
    harness.keep_cursor(1);
    let task = harness.run_with(harness.feed(), FailsOnce(AtomicUsize::new(0)));

    let pause = harness.parked().await;
    assert!(
        pause >= Duration::from_secs(1) && pause <= Duration::from_millis(1250),
        "{pause:?}"
    );
    assert!(harness.board.seen().is_empty());
    harness.clock.advance(pause);
    assert!(harness.still(|| !harness.board.seen().is_empty()).await);
    task.abort();
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

    assert!(harness.until(|| harness.board.seen().len() >= 4).await);
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

    for round in 1..=3 {
        assert_eq!(harness.parked().await, Duration::from_secs(1));
        assert_eq!(harness.board.seen().len(), round);
        harness.clock.advance(Duration::from_millis(999));
        assert_eq!(harness.clock.waiting(), [Duration::from_millis(1)]);
        assert_eq!(harness.board.seen().len(), round);
        harness.clock.advance(Duration::from_millis(1));
        assert!(
            harness
                .still(|| harness.board.seen().len() == round + 1)
                .await
        );
    }
    task.abort();
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
        _ => answer(1, vec![]).unanswered(),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(harness.until(|| harness.board.seen().len() >= 3).await);
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
        _ => answer(6, vec![]).unanswered(),
    })
    .await;
    let first = harness.run(harness.feed());
    assert!(
        harness
            .until(|| harness.cursor().as_deref() == Some("6"))
            .await
    );
    first.abort();
    let _ = first.await;
    let before = harness.board.seen().len();

    let second = harness.run(harness.feed());
    assert!(harness.until(|| harness.board.seen().len() > before).await);
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
        _ => answer(4, vec![]).unanswered(),
    })
    .await;
    let task = harness.run(harness.feed());

    assert!(harness.until(|| harness.sink.alerts().len() == 3).await);
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
const PACE: Duration = Duration::from_secs(1);

impl Harness {
    async fn quiet(&self) {
        assert_eq!(self.parked().await, TICK);
    }

    async fn polling(&self) {
        assert!(
            self.still(|| self.clock.waiting() == [TICK, PACE]).await,
            "{:?}",
            self.clock.waiting()
        );
    }

    async fn scan(&self) {
        self.clock.advance(TICK);
        self.quiet().await;
    }
}

#[tokio::test]
async fn the_supervisor_starts_a_fleet_when_its_cookie_appears_and_runs_one_loop() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| answer(5, vec![])).await;
    let cookies = Shared::default();
    let supervisor = Supervisor::new(harness.feed(), cookies.clone(), harness.sink.clone(), TICK);
    supervisor.start();
    supervisor.start();

    harness.quiet().await;
    for _ in 0..5 {
        harness.scan().await;
    }
    assert!(harness.board.seen().is_empty());
    cookies.set(Some(&format!("session=zzz; {OWN}")));
    harness.scan().await;
    harness.polling().await;
    assert_eq!(harness.board.seen().len(), 1);
    for round in 2..=4 {
        harness.clock.advance(PACE);
        harness.polling().await;
        assert_eq!(harness.board.seen().len(), round);
    }

    supervisor.stop();
    assert!(harness.still(|| harness.clock.waiting().is_empty()).await);
    harness.clock.advance(Duration::from_secs(600));
    assert_eq!(harness.board.seen().len(), 4);
}

#[tokio::test]
async fn a_nudge_through_the_supervisor_ends_the_backoff_wait_of_a_loop_that_keeps_failing() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0..=3 => Reply::status(503),
        _ => answer(1, vec![]).unanswered(),
    })
    .await;
    let cookies = Shared::default();
    cookies.set(Some(OWN));
    let supervisor = Supervisor::new(harness.feed(), cookies, harness.sink.clone(), TICK);
    supervisor.start();

    for failed in 1..=4 {
        assert!(
            harness
                .still(|| harness.board.seen().len() == failed && harness.clock.waiting().len() == 2)
                .await
        );
        supervisor.nudge();
    }
    assert!(harness.still(|| harness.board.seen().len() == 5).await);
    supervisor.stop();
}

#[tokio::test]
async fn forgetting_stops_the_loops_and_the_cursors_and_posts_nothing() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| answer(5, vec![])).await;
    let cookies = Shared::default();
    cookies.set(Some(OWN));
    let supervisor = Supervisor::new(harness.feed(), cookies.clone(), harness.sink.clone(), TICK);
    supervisor.start();
    harness.polling().await;
    assert_eq!(harness.cursor().as_deref(), Some("5"));

    supervisor.forget_all();
    assert_eq!(harness.cursor(), None);
    harness.quiet().await;
    for _ in 0..20 {
        harness.scan().await;
    }
    assert_eq!(harness.board.seen().len(), 1);
    assert!(harness.sink.unpaired().is_empty());

    cookies.set(None);
    harness.scan().await;
    cookies.set(Some(OWN));
    harness.scan().await;
    harness.polling().await;
    assert_eq!(harness.board.seen().len(), 2);
    supervisor.stop();
}

#[tokio::test]
async fn a_stopped_supervisor_polls_a_fleet_forget_parked_when_its_cookie_stayed() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| answer(5, vec![])).await;
    let cookies = Shared::default();
    cookies.set(Some(OWN));
    let supervisor = Supervisor::new(harness.feed(), cookies.clone(), harness.sink.clone(), TICK);
    supervisor.start();
    harness.polling().await;
    assert_eq!(harness.cursor().as_deref(), Some("5"));

    supervisor.forget_all();
    harness.quiet().await;
    for _ in 0..20 {
        harness.scan().await;
    }
    assert_eq!(harness.board.seen().len(), 1);

    supervisor.stop();
    supervisor.start();
    harness.polling().await;
    assert_eq!(harness.board.seen().len(), 2);
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
    harness.polling().await;
    harness.clock.advance(PACE);
    assert!(harness.still(|| harness.sink.unpaired().len() == 1).await);
    harness.quiet().await;
    for _ in 0..10 {
        harness.scan().await;
    }
    assert_eq!(harness.sink.unpaired().len(), 1);
    assert_eq!(harness.board.seen().len(), 2);

    cookies.set(None);
    harness.scan().await;
    cookies.set(Some(OWN));
    harness.scan().await;
    harness.polling().await;
    assert_eq!(harness.board.seen().len(), 3);
    supervisor.stop();
}

type Release = Arc<Mutex<mpsc::Receiver<()>>>;

#[derive(Clone)]
struct Blocking {
    sink: Sink,
    entered: Arc<AtomicBool>,
    release: Release,
}

impl AlertSink for Blocking {
    async fn alert(&self, fleet: &Fleet, alert: &Alert) -> io::Result<()> {
        self.entered.store(true, Ordering::SeqCst);
        let _ = self.release.lock().unwrap().recv();
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
            _ => answer(5 + batch, vec![]),
        })
        .await;
        let (release, gate) = mpsc::channel();
        let entered = Arc::new(AtomicBool::new(false));
        let blocking = Blocking {
            sink: harness.sink.clone(),
            entered: entered.clone(),
            release: Arc::new(Mutex::new(gate)),
        };
        let feed = Arc::new(harness.feed());
        let task = {
            let feed = feed.clone();
            tokio::spawn(async move { feed.run(FLEET, &Cookies, &blocking, false).await })
        };
        assert!(harness.until(|| entered.load(Ordering::SeqCst)).await);

        feed.forget_cursors();
        release.send(()).unwrap();
        drop(release);

        assert_eq!(
            timeout(WAIT, task).await.unwrap().unwrap(),
            Stop::Forgotten,
            "batch of {batch}"
        );
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
        _ => answer(4, vec![]),
    })
    .await;
    let cookies = Shared::default();
    cookies.set(Some(OWN));
    let supervisor = Supervisor::new(harness.feed(), cookies.clone(), harness.sink.clone(), TICK);
    supervisor.start();
    harness.polling().await;
    harness.clock.advance(PACE);
    assert!(harness.still(|| harness.sink.unpaired().len() == 1).await);
    harness.quiet().await;
    assert_eq!(harness.board.seen().len(), 2);

    cookies.set(Some("hand_phone_f0123456789ab=def456"));
    harness.scan().await;
    harness.polling().await;
    supervisor.stop();
    let seen = harness.board.seen();
    assert_eq!(seen.len(), 3);
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
        _ => answer(4, vec![]),
    })
    .await;
    let cookies = Shared::default();
    cookies.set(Some(OWN));
    let supervisor = Supervisor::new(harness.feed(), cookies.clone(), harness.sink.clone(), TICK);
    supervisor.start();
    harness.polling().await;
    harness.clock.advance(PACE);
    assert!(harness.still(|| harness.sink.unpaired().len() == 1).await);
    assert_eq!(harness.cursor().as_deref(), Some("4"));
    harness.quiet().await;
    for _ in 0..10 {
        harness.scan().await;
    }
    assert_eq!(harness.board.seen().len(), 2);

    for _ in 0..30 {
        harness.scan().await;
    }
    assert!(harness.still(|| harness.cursor().is_none()).await);
    assert_eq!(harness.board.seen().len(), 3);
    assert_eq!(harness.sink.unpaired().len(), 1);

    assert!(harness.until(|| harness.board.seen().len() >= 4).await);
    supervisor.stop();
    assert_eq!(harness.sink.unpaired().len(), 1);
}

struct BlocksThenNone(Arc<AtomicBool>, Release);

impl CookieSource for BlocksThenNone {
    async fn cookies(&self) -> io::Result<Option<String>> {
        self.0.store(true, Ordering::SeqCst);
        let _ = self.1.lock().unwrap().recv();
        Ok(None)
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn forgetting_while_a_refusal_is_in_flight_posts_no_unpaired_notice() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let gate = Arc::new(tokio::sync::Notify::new());
    let held = gate.clone();
    let harness = Harness::start(&bin, 60, move |n, _| match n {
        0 => answer(4, vec![]),
        _ => Reply::status(403)
            .header("X-Hand-Error", GONE)
            .header("Transfer-Encoding", "chunked")
            .gated(&held),
    })
    .await;
    let feed = Arc::new(harness.feed());
    let task = {
        let feed = feed.clone();
        let sink = harness.sink.clone();
        tokio::spawn(async move { feed.run(FLEET, &Cookies, &sink, false).await })
    };
    assert!(harness.until(|| harness.board.seen().len() >= 2).await);

    feed.forget_cursors();
    gate.notify_one();

    assert_eq!(timeout(WAIT, task).await.unwrap().unwrap(), Stop::Forgotten);
    assert!(harness.sink.unpaired().is_empty());
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn forgetting_while_the_cookie_read_blocks_posts_no_unpaired_notice() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| answer(4, vec![])).await;
    let feed = Arc::new(harness.feed());
    let reading = Arc::new(AtomicBool::new(false));
    let (release, gate) = mpsc::channel();
    let task = {
        let feed = feed.clone();
        let sink = harness.sink.clone();
        let cookies = BlocksThenNone(reading.clone(), Arc::new(Mutex::new(gate)));
        tokio::spawn(async move { feed.run(FLEET, &cookies, &sink, false).await })
    };
    assert!(harness.until(|| reading.load(Ordering::SeqCst)).await);

    feed.forget_cursors();
    release.send(()).unwrap();

    assert_eq!(timeout(WAIT, task).await.unwrap().unwrap(), Stop::Forgotten);
    assert!(harness.sink.unpaired().is_empty());
}

#[tokio::test]
async fn the_feed_sends_the_imported_cookie_and_keeps_it_when_the_jar_changes() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| answer(5, vec![])).await;
    let stored = format!("hand_phone_{FLEET}=7.{}", "ab".repeat(32));
    let jar = MemoryJar::default();
    jar.set(Some(&format!("session=zzz; {stored}")));
    let store = sessions(harness.dir.path(), harness._sidecar.id(), &jar);
    let task = harness.run_with(harness.feed(), store);

    assert!(harness.until(|| !harness.board.seen().is_empty()).await);
    jar.set(Some(&format!("hand_phone_{FLEET}=x.junk")));
    assert!(harness.until(|| harness.board.seen().len() >= 3).await);
    assert!(!task.is_finished());
    for request in harness.board.seen() {
        assert_eq!(request.cookie.as_deref(), Some(stored.as_str()));
    }

    jar.set(None);
    assert!(harness.until(|| harness.board.seen().len() >= 5).await);

    assert!(!task.is_finished());
    assert!(harness.sink.unpaired().is_empty());
    for request in harness.board.seen() {
        assert_eq!(request.cookie.as_deref(), Some(stored.as_str()));
    }
    task.abort();
}

impl Harness {
    async fn sleep_and_wake_for_a_poll(&self) {
        self.dialer.close().await;
        assert!(self.dialer.is_down());
        self.dialer.open(endpoint().await);
    }

    fn polled(&self) -> Vec<String> {
        self.board
            .seen()
            .into_iter()
            .map(|seen| seen.target)
            .collect()
    }

    fn seqs(&self) -> Vec<u64> {
        self.sink
            .alerts()
            .iter()
            .map(|(_, alert)| alert.seq)
            .collect()
    }
}

#[tokio::test]
async fn a_poll_asleep_continues_the_live_cursor_and_the_live_feed_continues_the_poll() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer(5, vec![]),
        1 => answer(
            7,
            vec![
                alert(6, "attempt.failed", "a6", "a6 failed", &link("attempt/a6")),
                alert(7, "attempt.failed", "a7", "a7 failed", &link("attempt/a7")),
            ],
        ),
        2 => answer(7, vec![]).unanswered(),
        3 => answer(
            9,
            vec![
                alert(8, "attempt.failed", "a8", "a8 failed", &link("attempt/a8")),
                alert(9, "attempt.failed", "a9", "a9 failed", &link("attempt/a9")),
            ],
        ),
        4 => answer(9, vec![]),
        5 => answer(
            10,
            vec![alert(
                10,
                "attempt.failed",
                "a10",
                "a10 failed",
                &link("attempt/a10"),
            )],
        ),
        _ => answer(10, vec![]).unanswered(),
    })
    .await;
    let live = harness.run(harness.feed());
    assert!(
        harness
            .until(|| harness.board.seen().len() >= 3 && harness.cursor().as_deref() == Some("7"))
            .await
    );
    live.abort();
    let _ = live.await;

    harness.sleep_and_wake_for_a_poll().await;
    let feed = harness.feed();
    assert_eq!(
        feed.once(FLEET, &Cookies, &harness.sink, false).await,
        Once::Answered
    );
    assert_eq!(harness.seqs(), [6, 7, 8, 9]);
    assert_eq!(harness.cursor().as_deref(), Some("9"));
    assert_eq!(
        feed.once(FLEET, &Cookies, &harness.sink, false).await,
        Once::Answered
    );
    assert_eq!(harness.seqs(), [6, 7, 8, 9]);

    harness.dialer.close().await;
    harness.dialer.open(endpoint().await);
    let live = harness.run(harness.feed());
    assert!(harness.until(|| harness.seqs().len() == 5).await);
    live.abort();

    assert_eq!(harness.seqs(), [6, 7, 8, 9, 10]);
    assert_eq!(harness.cursor().as_deref(), Some("10"));
    assert_eq!(
        harness.polled()[3..6],
        [
            format!("/{FLEET}/alerts/feed?after=7&wait=0"),
            format!("/{FLEET}/alerts/feed?after=9&wait=0"),
            format!("/{FLEET}/alerts/feed?after=9&wait=25"),
        ]
    );
}

#[tokio::test]
async fn a_poll_that_the_sink_fails_leaves_the_cursor_so_the_next_poll_delivers_again_at_least_once()
 {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let two = || {
        answer(
            12,
            vec![
                alert(
                    11,
                    "attempt.failed",
                    "a11",
                    "a11 failed",
                    &link("attempt/a11"),
                ),
                alert(
                    12,
                    "attempt.failed",
                    "a12",
                    "a12 failed",
                    &link("attempt/a12"),
                ),
            ],
        )
    };
    let harness = Harness::start(&bin, 60, move |_, _| two()).await;
    harness.keep_cursor(10);
    harness.sink.fail_on(12);
    harness.sleep_and_wake_for_a_poll().await;
    let feed = harness.feed();

    assert_eq!(
        feed.once(FLEET, &Cookies, &harness.sink, false).await,
        Once::Retry
    );
    assert_eq!(harness.cursor().as_deref(), Some("10"));
    assert_eq!(harness.seqs(), [11]);

    harness.sink.0.lock().unwrap().fail_on = None;
    assert_eq!(
        feed.once(FLEET, &Cookies, &harness.sink, false).await,
        Once::Answered
    );
    assert_eq!(harness.cursor().as_deref(), Some("12"));
    assert_eq!(harness.seqs(), [11, 11, 12]);
}

#[tokio::test]
async fn a_poll_follows_more_with_no_wait_and_a_failed_poll_changes_nothing() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => answer_for(
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
        1 => answer(
            2,
            vec![alert(
                2,
                "attempt.failed",
                "a2",
                "a2 failed",
                &link("attempt/a2"),
            )],
        ),
        _ => Reply::status(503),
    })
    .await;
    harness.keep_cursor(0);
    let feed = harness.feed();

    assert_eq!(
        feed.once(FLEET, &Cookies, &harness.sink, false).await,
        Once::Answered
    );
    assert_eq!(harness.seqs(), [1, 2]);
    assert_eq!(
        harness.polled(),
        [
            format!("/{FLEET}/alerts/feed?after=0&wait=0"),
            format!("/{FLEET}/alerts/feed?after=1"),
        ]
    );

    assert_eq!(
        feed.once(FLEET, &Cookies, &harness.sink, false).await,
        Once::Retry
    );
    assert_eq!(harness.cursor().as_deref(), Some("2"));
    assert_eq!(harness.seqs(), [1, 2]);
    assert_eq!(harness.board.seen().len(), 3);
}

fn refusing_supervisor(harness: &Harness, cookies: &Shared) -> Arc<Supervisor<Shared, Sink>> {
    cookies.set(Some(OWN));
    Supervisor::new(
        harness.feed(),
        cookies.clone(),
        harness.sink.clone(),
        Duration::from_millis(1),
    )
}

#[tokio::test]
async fn a_refused_fleet_is_polled_again_when_its_cookie_changes_and_not_when_it_is_gone() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| {
        Reply::status(403).header("X-Hand-Error", GONE)
    })
    .await;
    harness.keep_cursor(4);
    let cookies = Shared::default();
    let supervisor = refusing_supervisor(&harness, &cookies);

    assert!(supervisor.poll_once().await);
    assert_eq!(harness.sink.unpaired().len(), 1);

    cookies.set(Some("hand_phone_f0123456789ab=changed"));
    supervisor.poll_once().await;
    assert_eq!(harness.board.seen().len(), 2);
    assert_eq!(harness.sink.unpaired().len(), 2);
    assert_eq!(harness.cursor().as_deref(), Some("4"));

    cookies.set(None);
    supervisor.poll_once().await;
    assert_eq!(harness.board.seen().len(), 2);
    assert_eq!(harness.cursor().as_deref(), Some("4"));
}

#[tokio::test]
async fn a_refusal_a_failed_poll_and_a_refusal_announce_once_and_delete_the_cursor() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        1 => Reply::status(503),
        _ => Reply::status(403).header("X-Hand-Error", GONE),
    })
    .await;
    harness.keep_cursor(4);
    let cookies = Shared::default();
    let supervisor = refusing_supervisor(&harness, &cookies);

    assert!(supervisor.poll_once().await);
    assert_eq!(harness.sink.unpaired().len(), 1);
    assert_eq!(harness.cursor().as_deref(), Some("4"));
    assert!(!supervisor.poll_once().await);
    assert_eq!(harness.cursor().as_deref(), Some("4"));
    assert!(supervisor.poll_once().await);

    assert_eq!(harness.sink.unpaired().len(), 1);
    assert!(harness.cursor().is_none());
}

#[tokio::test]
async fn a_sleep_between_refused_polls_keeps_the_refusal_and_a_service_stop_forgets_it() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| {
        Reply::status(403).header("X-Hand-Error", GONE)
    })
    .await;
    harness.keep_cursor(4);
    let cookies = Shared::default();
    let supervisor = refusing_supervisor(&harness, &cookies);

    supervisor.poll_once().await;
    supervisor.pause();
    supervisor.poll_once().await;
    supervisor.pause();
    supervisor.poll_once().await;

    assert_eq!(harness.sink.unpaired().len(), 1);
    assert!(harness.cursor().is_none());

    harness.keep_cursor(4);
    supervisor.stop();
    supervisor.poll_once().await;
    assert_eq!(harness.sink.unpaired().len(), 2);
    assert_eq!(harness.cursor().as_deref(), Some("4"));
}

#[tokio::test]
async fn a_poll_the_board_never_answers_is_not_reached_and_one_it_answers_is() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |n, _| match n {
        0 => Reply::status(503),
        _ => answer(4, vec![]),
    })
    .await;
    harness.keep_cursor(4);
    let cookies = Shared::default();
    let supervisor = refusing_supervisor(&harness, &cookies);

    assert!(!supervisor.poll_once().await);
    assert!(supervisor.poll_once().await);
    cookies.set(None);
    assert!(supervisor.poll_once().await);
}

#[tokio::test]
async fn a_second_refusal_in_a_poll_deletes_the_cursor_without_a_notice() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, 60, |_, _| {
        Reply::status(403).header("X-Hand-Error", GONE)
    })
    .await;
    harness.keep_cursor(4);
    let feed = harness.feed();

    let stop = feed.once(FLEET, &Cookies, &harness.sink, true).await;

    assert_eq!(stop, Once::Stopped(Stop::BoardGone));
    assert!(harness.cursor().is_none());
    assert!(harness.sink.unpaired().is_empty());
}
