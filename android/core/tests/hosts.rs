#![cfg(target_os = "linux")]

mod support;

use std::io;
use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use hand_android_core::{
    Alert, AlertSink, Cancel, Companion, CompanionConfig, Feed, Fleet, Live, LiveEnd, LiveSink,
    Method, Paired, Polled, Request, RequestError, Route, Sessions, Supervisor,
};
use serde_json::json;
use support::{
    FeedBoard, MemoryJar, PlainCipher, Reply, Seen, Sidecar, SidecarOptions, TempDir, route,
    sidecar_bin,
};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tokio::time::{sleep, timeout};

const FLEET_A: &str = "f0123456789ab";
const FLEET_B: &str = "f999999999999";
const SECRET_A: &str = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const SECRET_B: &str = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
const PEER_A: &str = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210";
const PEER_B: &str = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
const WAIT: Duration = Duration::from_secs(15);
const POLL: Duration = Duration::from_secs(3);
const TICK: Duration = Duration::from_millis(100);

#[derive(Clone, Default)]
struct Sink {
    alerts: Arc<Mutex<Vec<(String, u64)>>>,
    refuse: Arc<AtomicBool>,
}

impl Sink {
    fn alerts(&self) -> Vec<(String, u64)> {
        let mut alerts = self.alerts.lock().unwrap().clone();
        alerts.sort();
        alerts
    }
}

impl AlertSink for Sink {
    async fn alert(&self, fleet: &Fleet, alert: &Alert) -> io::Result<()> {
        if self.refuse.load(Ordering::SeqCst) {
            return Err(io::Error::other("the notification was not posted"));
        }
        self.alerts
            .lock()
            .unwrap()
            .push((fleet.id.clone(), alert.seq));
        Ok(())
    }

    async fn unpaired(&self, _fleet: &str, _name: Option<&str>) {}
}

#[derive(Clone, Default)]
struct Events(Arc<Mutex<Vec<(String, String)>>>);

impl LiveSink for Events {
    fn live(&self, fleet: &str, topic: &str, _json: &str) -> io::Result<()> {
        self.0
            .lock()
            .unwrap()
            .push((fleet.to_owned(), topic.to_owned()));
        Ok(())
    }

    fn link(&self, _fleet: &str, _state: &str, _since: u64) -> io::Result<()> {
        Ok(())
    }
}

fn answer(fleet: &str, cursor: u64, alerts: Vec<serde_json::Value>) -> Reply {
    Reply::json(
        json!({
            "version": 1,
            "fleet": {"id": fleet, "name": "hand"},
            "cursor": cursor,
            "more": false,
            "alerts": alerts,
        })
        .to_string(),
    )
}

fn sse() -> Vec<u8> {
    let block = "event: hello\ndata: {}\n\n";
    format!(
        "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n{:x}\r\n{block}\r\n",
        block.len()
    )
    .into_bytes()
}

fn responder(fleet: &'static str) -> impl Fn(usize, &Seen) -> Reply + Send + Sync + 'static {
    move |_, seen| {
        if seen.target.contains("/alerts/feed") {
            let after = seen
                .target
                .split("after=")
                .nth(1)
                .and_then(|rest| rest.split('&').next())
                .and_then(|after| after.parse::<u64>().ok());
            match after {
                None => answer(fleet, 5, vec![]),
                Some(5) => answer(
                    fleet,
                    6,
                    vec![
                        json!({"seq": 6, "at": "2026-11-10T03:12:00Z", "kind": "decision",
                        "ref": "d1", "text": format!("{fleet} needs you"), "link": ""}),
                    ],
                ),
                Some(after) => answer(fleet, after, vec![]),
            }
        } else if seen.target.contains("/keepalive") {
            Reply::raw(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
                .held_after(Duration::from_secs(60))
        } else if seen.target.contains("/api/v1/events") {
            Reply::raw(&sse()).held_after(Duration::from_secs(30))
        } else {
            Reply::json(json!({"ok": true}).to_string())
        }
    }
}

struct Side {
    board: FeedBoard,
    sidecar: Option<Sidecar>,
    fleet: &'static str,
    secret: &'static str,
    host: String,
}

impl Side {
    async fn start(
        bin: &Path,
        fleet: &'static str,
        secret: &'static str,
        peer: &'static str,
        live: bool,
    ) -> Self {
        let board = FeedBoard::start(peer, responder(fleet)).await;
        let sidecar = Sidecar::start(bin, SidecarOptions::new(board.addr()).secret(peer));
        let host = sidecar.id().to_string();
        Self {
            board,
            sidecar: live.then_some(sidecar),
            fleet,
            secret,
            host,
        }
    }

    fn cookie(&self) -> String {
        format!("hand_phone_{}={}", self.fleet, self.value())
    }

    fn value(&self) -> String {
        format!("7.{}", self.secret)
    }

    fn requests(&self, part: &str) -> Vec<Seen> {
        self.board
            .seen()
            .into_iter()
            .filter(|seen| seen.target.contains(part))
            .collect()
    }
}

struct Rig {
    companion: Arc<Companion>,
    sessions: Arc<Sessions<MemoryJar>>,
    a: Side,
    b: Side,
    dir: TempDir,
}

async fn rig(bin: &Path, b_reachable: bool) -> Rig {
    let a = Side::start(bin, FLEET_A, SECRET_A, PEER_A, true).await;
    let b = Side::start(bin, FLEET_B, SECRET_B, PEER_B, b_reachable).await;
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
    Rig {
        companion,
        sessions,
        a,
        b,
        dir,
    }
}

impl Rig {
    async fn pair(&self, side: &Side, target: iroh::EndpointAddr) {
        self.sessions
            .pair(
                &side.host,
                None,
                &[Paired {
                    id: side.fleet.to_owned(),
                    name: "hand".to_owned(),
                    value: side.value(),
                }],
            )
            .unwrap();
        self.companion.dialers().host(&side.host, target).await;
    }

    fn supervisor(&self, sink: &Sink) -> Arc<Supervisor<Arc<Sessions<MemoryJar>>, Sink>> {
        let feed = Feed::routed(
            route(&self.companion, &self.sessions),
            self.dir.path().join("alerts"),
        );
        Supervisor::new(feed, self.sessions.clone(), sink.clone(), TICK)
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

    async fn get(&self, side: &Side) -> Result<u16, RequestError> {
        let cookie = self.sessions.cookie(side.fleet).unwrap_or_default();
        let request = Request::new(
            Method::Get,
            side.fleet,
            "/api/v1/status",
            Some(&cookie),
            None,
            &[],
        )?;
        self.companion
            .request(&self.sessions, side.fleet, &cookie, &request)
            .await
            .map(|response| response.status)
    }
}

fn clean(side: &Side, other: &Side) {
    for seen in side.board.seen() {
        let held = format!("{seen:?}");
        assert!(!held.contains(other.secret), "{held}");
        assert!(!held.contains(other.fleet), "{held}");
        assert_eq!(
            seen.cookie.as_deref(),
            Some(side.cookie().as_str()),
            "{held}"
        );
    }
}

async fn through_proxy(port: u16, path: &str, cookie: &str) -> io::Result<Vec<u8>> {
    let mut stream = TcpStream::connect(("127.0.0.1", port)).await?;
    stream
        .write_all(
            format!("GET {path} HTTP/1.1\r\nHost: localhost\r\nCookie: {cookie}\r\nConnection: close\r\n\r\n")
                .as_bytes(),
        )
        .await?;
    let mut body = Vec::new();
    stream.read_to_end(&mut body).await?;
    Ok(body)
}

async fn until(mut condition: impl FnMut() -> bool) -> bool {
    let deadline = Instant::now() + WAIT;
    while Instant::now() < deadline {
        if condition() {
            return true;
        }
        sleep(Duration::from_millis(20)).await;
    }
    condition()
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_cookie_for_one_host_never_reaches_the_other_on_any_path() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(&bin, true).await;
    rig.pair(&rig.a, rig.a.sidecar.as_ref().unwrap().target())
        .await;
    rig.pair(&rig.b, rig.b.sidecar.as_ref().unwrap().target())
        .await;

    assert_eq!(rig.get(&rig.a).await.unwrap(), 200);
    assert_eq!(rig.get(&rig.b).await.unwrap(), 200);
    assert_eq!(rig.a.requests("/api/v1/status").len(), 1);
    assert_eq!(rig.b.requests("/api/v1/status").len(), 1);

    let port = rig.companion.port();
    let (_, cookie) = rig
        .companion
        .fallback(&rig.sessions, FLEET_A)
        .await
        .unwrap();
    assert_eq!(cookie, rig.a.cookie());
    through_proxy(port, &format!("/{FLEET_A}/page"), &cookie)
        .await
        .unwrap();
    let (_, cookie) = rig
        .companion
        .fallback(&rig.sessions, FLEET_B)
        .await
        .unwrap();
    assert_eq!(cookie, rig.b.cookie());
    through_proxy(port, &format!("/{FLEET_B}/page"), &cookie)
        .await
        .unwrap();
    assert_eq!(rig.a.requests("/page").len(), 1);
    assert_eq!(rig.b.requests("/page").len(), 1);

    let events = Events::default();
    let live = Live::routed(route(&rig.companion, &rig.sessions));
    let cancel = Cancel::default();
    let lives: Vec<_> = [FLEET_A, FLEET_B]
        .into_iter()
        .map(|fleet| {
            let (live, events, cancel, sessions) = (
                live.clone(),
                events.clone(),
                cancel.clone(),
                rig.sessions.clone(),
            );
            tokio::spawn(async move { live.run(fleet, &sessions, &events, &cancel).await })
        })
        .collect();
    assert!(
        until(|| events.0.lock().unwrap().len() >= 2).await,
        "{:?}",
        events.0.lock().unwrap()
    );
    cancel.cancel();
    for task in lives {
        let _ = timeout(WAIT, task).await;
    }
    assert_eq!(rig.a.requests("/api/v1/events").len(), 1);
    assert_eq!(rig.b.requests("/api/v1/events").len(), 1);

    let sink = Sink::default();
    let supervisor = rig.supervisor(&sink);
    assert!(supervisor.poll_once(POLL).await);
    assert!(supervisor.poll_once(POLL).await);
    assert_eq!(
        sink.alerts(),
        [(FLEET_A.to_owned(), 6), (FLEET_B.to_owned(), 6)]
    );
    assert_eq!(rig.a.requests("/alerts/feed").len(), 2);
    assert_eq!(rig.b.requests("/alerts/feed").len(), 2);

    clean(&rig.a, &rig.b);
    clean(&rig.b, &rig.a);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_proxy_with_no_host_chosen_serves_nothing_and_a_forgotten_host_is_not_served() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(&bin, true).await;
    rig.pair(&rig.a, rig.a.sidecar.as_ref().unwrap().target())
        .await;
    rig.pair(&rig.b, rig.b.sidecar.as_ref().unwrap().target())
        .await;
    let port = rig.companion.port();

    let none = through_proxy(port, &format!("/{FLEET_A}/page"), &rig.a.cookie()).await;
    assert!(none.unwrap().is_empty());

    rig.companion
        .fallback(&rig.sessions, FLEET_A)
        .await
        .unwrap();
    let served = through_proxy(port, &format!("/{FLEET_A}/page"), &rig.a.cookie())
        .await
        .unwrap();
    assert!(served.starts_with(b"HTTP/1.1 200"));

    rig.companion
        .forget_host(&rig.a.host, || rig.sessions.forget_host(&rig.a.host))
        .await
        .unwrap();
    let gone = through_proxy(port, &format!("/{FLEET_A}/page"), &rig.a.cookie()).await;
    assert!(gone.unwrap().is_empty());
    assert_eq!(rig.a.requests("/page").len(), 1);
    assert!(
        rig.companion
            .fallback(&rig.sessions, FLEET_A)
            .await
            .is_none()
    );
    assert_eq!(rig.get(&rig.a).await.err(), Some(RequestError::Unpaired));
    assert_eq!(rig.get(&rig.b).await.unwrap(), 200);
    clean(&rig.b, &rig.a);
}

#[tokio::test]
async fn a_live_stream_stops_on_cancel_while_its_route_is_still_resolving() {
    let dir = TempDir::new();
    let sessions =
        Arc::new(Sessions::open(dir.path(), MemoryJar::default(), Arc::new(PlainCipher)).unwrap());
    sessions
        .pair(
            "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6",
            None,
            &[Paired {
                id: FLEET_A.to_owned(),
                name: "hand".to_owned(),
                value: format!("7.{SECRET_A}"),
            }],
        )
        .unwrap();
    let pending: Route = Arc::new(|_, _| Box::pin(std::future::pending()));
    let live = Live::routed(pending);
    let cancel = Cancel::default();
    let events = Events::default();
    let task = {
        let cancel = cancel.clone();
        tokio::spawn(async move { live.run(FLEET_A, &sessions, &events, &cancel).await })
    };
    sleep(Duration::from_millis(100)).await;

    cancel.cancel();

    assert_eq!(
        timeout(Duration::from_secs(2), task)
            .await
            .unwrap()
            .unwrap(),
        LiveEnd::Stopped
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_kept_alive_proxy_connection_is_closed_when_another_host_is_chosen() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(&bin, true).await;
    rig.pair(&rig.a, rig.a.sidecar.as_ref().unwrap().target())
        .await;
    rig.pair(&rig.b, rig.b.sidecar.as_ref().unwrap().target())
        .await;
    let port = rig.companion.port();
    rig.companion
        .fallback(&rig.sessions, FLEET_A)
        .await
        .unwrap();
    let mut stream = TcpStream::connect(("127.0.0.1", port)).await.unwrap();
    let request = |fleet: &str, cookie: &str| {
        format!("GET /{fleet}/keepalive HTTP/1.1\r\nHost: localhost\r\nCookie: {cookie}\r\n\r\n")
    };
    stream
        .write_all(request(FLEET_A, &rig.a.cookie()).as_bytes())
        .await
        .unwrap();
    let mut first = [0u8; 12];
    timeout(WAIT, stream.read_exact(&mut first))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(&first, b"HTTP/1.1 200");

    rig.companion
        .fallback(&rig.sessions, FLEET_B)
        .await
        .unwrap();
    let _ = stream
        .write_all(request(FLEET_B, &rig.b.cookie()).as_bytes())
        .await;
    let mut rest = Vec::new();
    let _ = timeout(WAIT, stream.read_to_end(&mut rest)).await.unwrap();

    assert!(!String::from_utf8_lossy(&rest).contains(FLEET_B));
    assert_eq!(rig.a.requests("/keepalive").len(), 1);
    assert!(rig.b.requests("/keepalive").is_empty());
    clean(&rig.a, &rig.b);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn forgetting_one_host_keeps_the_other_hosts_cursor_and_its_alerts_flowing() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(&bin, true).await;
    rig.pair(&rig.a, rig.a.sidecar.as_ref().unwrap().target())
        .await;
    rig.pair(&rig.b, rig.b.sidecar.as_ref().unwrap().target())
        .await;
    let sink = Sink::default();
    let supervisor = rig.supervisor(&sink);
    assert!(supervisor.poll_once(POLL).await);
    assert_eq!(rig.cursor(FLEET_A).as_deref(), Some("5"));
    assert_eq!(rig.cursor(FLEET_B).as_deref(), Some("5"));
    assert!(sink.alerts().is_empty());

    supervisor.forget_fleets(&[FLEET_A.to_owned()]);
    rig.companion
        .forget_host(&rig.a.host, || rig.sessions.forget_host(&rig.a.host))
        .await
        .unwrap();

    assert_eq!(rig.cursor(FLEET_A), None);
    assert_eq!(rig.cursor(FLEET_B).as_deref(), Some("5"));
    assert!(supervisor.poll_once(POLL).await);
    assert_eq!(rig.a.requests("/alerts/feed").len(), 1);
    assert_eq!(rig.b.requests("/alerts/feed").len(), 2);
    assert_eq!(rig.cursor(FLEET_B).as_deref(), Some("6"));
    assert_eq!(sink.alerts(), [(FLEET_B.to_owned(), 6)]);
    assert_eq!(rig.sessions.hosts().len(), 1);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn sleep_wake_and_poll_move_every_host_to_the_new_endpoint() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(&bin, true).await;
    rig.pair(&rig.a, rig.a.sidecar.as_ref().unwrap().target())
        .await;
    rig.pair(&rig.b, rig.b.sidecar.as_ref().unwrap().target())
        .await;
    assert_eq!(rig.get(&rig.a).await.unwrap(), 200);
    assert_eq!(rig.get(&rig.b).await.unwrap(), 200);
    let first = rig.companion.dialers().endpoint().id();

    rig.companion.sleep().await;
    assert!(rig.companion.dialers().endpoint().is_closed());
    assert!(
        rig.companion
            .dialers()
            .all()
            .iter()
            .all(|dialer| dialer.is_down())
    );
    assert_eq!(rig.get(&rig.a).await.err(), Some(RequestError::Lost));
    assert_eq!(rig.get(&rig.b).await.err(), Some(RequestError::Lost));

    let sink = Sink::default();
    let supervisor = rig.supervisor(&sink);
    for _ in 0..2 {
        let polled = rig
            .companion
            .poll_asleep(supervisor.poll_once(POLL))
            .await
            .unwrap();
        assert_eq!(polled, Polled::Reached);
    }
    assert_eq!(
        sink.alerts(),
        [(FLEET_A.to_owned(), 6), (FLEET_B.to_owned(), 6)]
    );
    assert!(rig.companion.asleep());
    assert!(
        rig.companion
            .dialers()
            .all()
            .iter()
            .all(|dialer| dialer.is_down())
    );

    rig.companion.wake().await.unwrap();
    let live = rig.companion.dialers().endpoint();
    assert!(!live.is_closed());
    assert_eq!(live.id(), first);
    for dialer in rig.companion.dialers().all() {
        assert!(!dialer.is_down());
        assert!(!dialer.endpoint().is_closed());
    }
    assert_eq!(rig.get(&rig.a).await.unwrap(), 200);
    assert_eq!(rig.get(&rig.b).await.unwrap(), 200);
    clean(&rig.a, &rig.b);
    clean(&rig.b, &rig.a);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_dead_host_costs_the_live_host_neither_its_alert_nor_its_cursor() {
    let Some(bin) = sidecar_bin() else {
        return;
    };
    let rig = rig(&bin, false).await;
    let dead = {
        let sidecar = Sidecar::start(&bin, SidecarOptions::new(rig.b.board.addr()).secret(PEER_B));
        let target = sidecar.target();
        drop(sidecar);
        target
    };
    rig.pair(&rig.b, dead).await;
    rig.pair(&rig.a, rig.a.sidecar.as_ref().unwrap().target())
        .await;
    rig.companion.sleep().await;
    let sink = Sink::default();
    let supervisor = rig.supervisor(&sink);

    let baseline = rig
        .companion
        .poll_asleep_within(Duration::from_secs(10), supervisor.poll_once(POLL))
        .await
        .unwrap();
    assert_eq!(baseline, Polled::Reached);
    assert_eq!(rig.cursor(FLEET_A).as_deref(), Some("5"));

    sink.refuse.store(true, Ordering::SeqCst);
    let started = Instant::now();
    let refused = rig
        .companion
        .poll_asleep_within(Duration::from_secs(10), supervisor.poll_once(POLL))
        .await
        .unwrap();
    assert_eq!(refused, Polled::Missed);
    assert!(
        started.elapsed() < Duration::from_secs(9),
        "{:?}",
        started.elapsed()
    );
    assert!(sink.alerts().is_empty());
    assert_eq!(rig.cursor(FLEET_A).as_deref(), Some("5"));

    sink.refuse.store(false, Ordering::SeqCst);
    let started = Instant::now();
    let polled = rig
        .companion
        .poll_asleep_within(Duration::from_secs(10), supervisor.poll_once(POLL))
        .await
        .unwrap();
    assert_eq!(polled, Polled::Reached);
    assert!(
        started.elapsed() < Duration::from_secs(9),
        "{:?}",
        started.elapsed()
    );
    assert_eq!(sink.alerts(), [(FLEET_A.to_owned(), 6)]);
    assert_eq!(rig.cursor(FLEET_A).as_deref(), Some("6"));
    assert_eq!(rig.cursor(FLEET_B), None);
    assert!(rig.b.board.seen().is_empty());
    clean(&rig.a, &rig.b);
}
