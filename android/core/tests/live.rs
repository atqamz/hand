#![cfg(target_os = "linux")]

mod support;

use std::io;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use hand_android_core::{
    Alert, AlertSink, Cancel, CookieSource, Feed, Fleet, Live, LiveEnd, LiveSink,
    STREAMS_PER_CONNECTION,
};
use serde_json::json;
use support::{FeedBoard, Reply, Seen, Sidecar, SidecarOptions, TempDir, dialer, sidecar_bin};
use tokio::time::{sleep, timeout};

const FLEET: &str = "f0123456789ab";
const OTHERS: [&str; 3] = ["f111111111111", "f222222222222", "f333333333333"];
const SECRET: &str = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210";
const COOKIES: &str = "hand_phone_f0123456789ab=abc123; hand_phone_f111111111111=b; hand_phone_f222222222222=c; hand_phone_f333333333333=d";
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
struct Sink(Arc<Mutex<Record>>);

#[derive(Default)]
struct Record {
    events: Vec<(String, String, String)>,
    links: Vec<(String, String, u64)>,
}

impl Sink {
    fn events(&self) -> Vec<(String, String, String)> {
        self.0.lock().unwrap().events.clone()
    }

    fn links(&self) -> Vec<(String, String, u64)> {
        self.0.lock().unwrap().links.clone()
    }
}

impl LiveSink for Sink {
    fn live(&self, fleet: &str, topic: &str, json: &str) -> io::Result<()> {
        self.0
            .lock()
            .unwrap()
            .events
            .push((fleet.to_owned(), topic.to_owned(), json.to_owned()));
        Ok(())
    }

    fn link(&self, fleet: &str, state: &str, since: u64) -> io::Result<()> {
        self.0
            .lock()
            .unwrap()
            .links
            .push((fleet.to_owned(), state.to_owned(), since));
        Ok(())
    }
}

struct Cookies;

impl CookieSource for Cookies {
    async fn cookies(&self) -> io::Result<Option<String>> {
        Ok(Some(COOKIES.to_owned()))
    }
}

struct FeedSink;

impl AlertSink for FeedSink {
    async fn alert(&self, _fleet: &Fleet, _alert: &Alert) -> io::Result<()> {
        Ok(())
    }

    async fn unpaired(&self, _fleet: &str, _name: Option<&str>) {}
}

struct Harness {
    board: FeedBoard,
    dialer: hand_android_core::Dialer,
    sink: Sink,
    dir: TempDir,
    _sidecar: Sidecar,
}

impl Harness {
    async fn start(
        bin: &std::path::Path,
        responder: impl Fn(usize, &Seen) -> Reply + Send + Sync + 'static,
    ) -> Self {
        let board = FeedBoard::start(SECRET, responder).await;
        let sidecar = Sidecar::start(bin, SidecarOptions::new(board.addr()).secret(SECRET));
        let dialer = dialer(sidecar.target()).await;
        Self {
            board,
            dialer,
            sink: Sink::default(),
            dir: TempDir::new(),
            _sidecar: sidecar,
        }
    }

    fn live(&self) -> Live {
        Live::new(self.dialer.clone())
    }
}

fn sse(events: &[(&str, String)], close: bool) -> Vec<u8> {
    let mut out = b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n".to_vec();
    for (name, data) in events {
        let block = format!("event: {name}\ndata: {data}\n\n");
        out.extend(format!("{:x}\r\n{block}\r\n", block.len()).as_bytes());
    }
    if close {
        out.extend(b"0\r\n\r\n");
    }
    out
}

fn status() -> String {
    json!({"now": "2026-11-14T03:12:00Z", "agent_state": "working", "pill": "working"}).to_string()
}

fn timeline(from: u64, to: u64) -> String {
    json!({"snapshot": false, "from": from, "to": to, "older": 0, "live": true, "working": null, "entries": []})
        .to_string()
}

async fn run(harness: &Harness, live: Live) -> tokio::task::JoinHandle<LiveEnd> {
    let sink = harness.sink.clone();
    let cancel = Cancel::default();
    tokio::spawn(async move { live.run(FLEET, &Cookies, &sink, &cancel).await })
}

#[tokio::test]
async fn every_sse_event_crosses_with_the_api_json_and_the_fleets_cookie() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, |n, _| match n {
        0 => {
            let hello = json!({
                "version": 1,
                "fleet": {"id": FLEET, "name": "hand"},
                "tick_ms": 1000,
                "beat_ms": 15000,
                "now": "2026-11-14T03:12:00Z"
            })
            .to_string();
            let touched =
                json!({"topics":["tasks"],"tasks":["t40"],"decisions":["d12"]}).to_string();
            let resync = json!({"topics":["status"]}).to_string();
            Reply::raw(&sse(
                &[
                    ("hello", hello),
                    ("status", status()),
                    ("timeline", timeline(5, 8)),
                    ("touched", touched),
                    ("beat", String::new()),
                    ("resync", resync),
                ],
                true,
            ))
        }
        _ => Reply::raw(&sse(&[], false)).held_after(Duration::from_secs(30)),
    })
    .await;

    let task = run(&harness, harness.live()).await;
    assert!(wait_for(|| harness.sink.events().len() >= 6).await);
    task.abort();

    let seen = harness.board.seen();
    assert_eq!(seen[0].target, format!("/{FLEET}/api/v1/events"));
    assert_eq!(seen[0].host.as_deref(), Some("localhost"));
    assert_eq!(
        seen[0].cookie.as_deref(),
        Some("hand_phone_f0123456789ab=abc123")
    );
    assert_eq!(seen[0].connection.as_deref(), Some("close"));

    let events = harness.sink.events();
    let topics: Vec<&str> = events.iter().map(|(_, topic, _)| topic.as_str()).collect();
    assert_eq!(
        topics,
        ["hello", "status", "timeline", "touched", "beat", "resync"]
    );
    assert!(events.iter().all(|(fleet, _, _)| fleet == FLEET));
    assert!(events[0].2.contains("\"fleet\":{\"id\":\"f0123456789ab\""));
    assert_eq!(
        serde_json::from_str::<serde_json::Value>(&events[3].2).unwrap(),
        json!({"topics":["tasks"],"tasks":["t40"],"decisions":["d12"]})
    );
    assert_eq!(events[4].2, "");
    assert_eq!(events[5].2, r#"{"topics":["status"]}"#);
    assert_eq!(harness.sink.links()[0].0, FLEET);
    assert_eq!(harness.sink.links()[0].1, "live");
}

#[tokio::test]
async fn a_run_that_ends_without_a_stream_emits_a_stopped_link() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, |_, _| {
        Reply::raw(&sse(&[], false)).held_after(Duration::from_secs(30))
    })
    .await;
    let cancel = Cancel::default();
    cancel.cancel();
    let end = harness
        .live()
        .run(FLEET, &Cookies, &harness.sink, &cancel)
        .await;
    assert_eq!(end, LiveEnd::Stopped);
    let links = harness.sink.links();
    assert_eq!(links.len(), 1);
    assert_eq!(links[0].0, FLEET);
    assert_eq!(links[0].1, "stopped");
    assert!(links[0].2 > 0);
    assert!(harness.board.seen().is_empty());
}

#[tokio::test]
async fn a_cancel_during_a_held_open_returns_stopped_and_emits_no_live() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, |_, _| {
        Reply::raw(&sse(&[("hello", json!({"version": 1}).to_string())], false))
            .held_after(Duration::from_secs(30))
    })
    .await;
    let mut held = Vec::new();
    for _ in 0..STREAMS_PER_CONNECTION {
        let opened = timeout(
            Duration::from_secs(5),
            harness
                .dialer
                .open_with(b"GET /f0123456789ab/api/v1/events HTTP/1.1\r\n\r\n"),
        )
        .await
        .expect("a held stream did not open")
        .expect("a held stream failed");
        held.push(opened);
        assert!(wait_for(|| harness.board.seen().len() >= held.len()).await);
    }

    let cancel = Cancel::default();
    let token = cancel.clone();
    let sink = harness.sink.clone();
    let live = harness.live();
    let task = tokio::spawn(async move { live.run(FLEET, &Cookies, &sink, &token).await });
    sleep(Duration::from_millis(200)).await;
    let started = Instant::now();
    cancel.cancel();
    let end = timeout(Duration::from_secs(2), task)
        .await
        .expect("the live run did not stop")
        .expect("the live task panicked");
    assert_eq!(end, LiveEnd::Stopped);
    assert!(started.elapsed() < Duration::from_secs(2));
    let links = harness.sink.links();
    assert_eq!(links.len(), 1);
    assert_eq!(links[0].0, FLEET);
    assert_eq!(links[0].1, "stopped");
    assert_eq!(harness.board.seen().len(), STREAMS_PER_CONNECTION);
}

#[tokio::test]
async fn a_silent_stream_is_dropped_after_the_watchdog_and_reconnects_for_a_snapshot() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, |n, _| match n {
        0 => Reply::raw(&sse(
            &[
                ("hello", json!({"version": 1}).to_string()),
                ("status", status()),
            ],
            false,
        ))
        .held_after(Duration::from_secs(5)),
        _ => Reply::raw(&sse(
            &[
                ("hello", json!({"version": 1}).to_string()),
                ("status", status()),
                ("timeline", timeline(0, 2)),
            ],
            true,
        )),
    })
    .await;

    let task = run(
        &harness,
        harness.live().with_watchdog(Duration::from_millis(150)),
    )
    .await;
    assert!(wait_for(|| harness.board.seen().len() >= 2).await);
    assert!(
        wait_for(|| {
            let links = harness.sink.links();
            links
                .iter()
                .filter(|(fleet, state, _)| fleet == FLEET && state == "live")
                .count()
                >= 2
                && links.iter().any(|(fleet, state, since)| {
                    fleet == FLEET && state == "reconnecting" && *since > 0
                })
        })
        .await
    );
    assert!(wait_for(|| harness.sink.events().len() >= 5).await);
    task.abort();

    let seen = harness.board.seen();
    assert_eq!(seen[0].target, format!("/{FLEET}/api/v1/events"));
    assert_eq!(seen[1].target, format!("/{FLEET}/api/v1/events"));
    assert!(!seen[1].target.contains("since"));
    let links = harness.sink.links();
    let first_live = links
        .iter()
        .position(|(fleet, state, _)| fleet == FLEET && state == "live")
        .unwrap();
    let reconnect = links
        .iter()
        .position(|(fleet, state, _)| fleet == FLEET && state == "reconnecting")
        .unwrap();
    let second_live = links
        .iter()
        .rposition(|(fleet, state, _)| fleet == FLEET && state == "live")
        .unwrap();
    assert!(first_live < reconnect && reconnect < second_live);
    assert!(wait_for(|| harness.sink.events().len() >= 5).await);
    assert_eq!(harness.sink.events().last().unwrap().1, "timeline");
}

#[tokio::test]
async fn four_feeds_and_one_live_hold_five_streams_and_a_thirteenth_waits() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let harness = Harness::start(&bin, |_, seen| {
        if seen.target.starts_with("/f") && seen.target.contains("/alerts/feed") {
            Reply::json("{}".to_owned())
                .declaring(100)
                .held_after(Duration::from_secs(30))
        } else {
            Reply::raw(&sse(&[("hello", json!({"version": 1}).to_string())], false))
                .held_after(Duration::from_secs(30))
        }
    })
    .await;

    let mut feeds = Vec::new();
    for fleet in std::iter::once(FLEET).chain(OTHERS) {
        let feed = Feed::new(harness.dialer.clone(), harness.dir.path().join(fleet));
        let cookies = Cookies;
        let id = fleet.to_owned();
        feeds.push(tokio::spawn(async move {
            feed.run(&id, &cookies, &FeedSink, false).await
        }));
        assert!(wait_for(|| harness.board.seen().len() >= feeds.len()).await);
    }
    let live = run(&harness, harness.live()).await;
    assert!(wait_for(|| harness.board.seen().len() >= 5).await);
    assert!(
        wait_for(|| harness
            .sink
            .links()
            .iter()
            .any(|(fleet, state, _)| fleet == FLEET && state == "live"))
        .await
    );
    assert_eq!(harness.board.seen().len(), 5);

    let mut held = Vec::new();
    for _ in 0..STREAMS_PER_CONNECTION - 5 {
        let opened = timeout(
            Duration::from_secs(5),
            harness
                .dialer
                .open_with(b"GET /f0123456789ab/api/v1/events HTTP/1.1\r\n\r\n"),
        )
        .await
        .expect("a held stream did not open")
        .expect("a held stream failed");
        held.push(opened);
        assert!(wait_for(|| harness.board.seen().len() >= 5 + held.len()).await);
    }
    assert_eq!(harness.board.seen().len(), STREAMS_PER_CONNECTION);

    let thirteenth = harness
        .dialer
        .open_with(b"GET /f0123456789ab/api/v1/events HTTP/1.1\r\n\r\n");
    assert!(
        timeout(Duration::from_millis(300), thirteenth)
            .await
            .is_err()
    );
    assert_eq!(harness.board.seen().len(), STREAMS_PER_CONNECTION);

    live.abort();
    for feed in feeds {
        feed.abort();
    }
}
