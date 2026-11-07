use std::collections::HashMap;
use std::io;
use std::net::{IpAddr, Ipv6Addr, SocketAddr};
use std::pin::Pin;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};
use std::time::Duration;

use iroh::EndpointId;
use iroh::endpoint::{
    Connection, Endpoint, Incoming, IncomingAddr, RecvStream, SendStream, VarInt,
};
use tokio::io::{AsyncWrite, AsyncWriteExt, copy};
use tokio::net::TcpStream;
use tokio::net::tcp::{OwnedReadHalf, OwnedWriteHalf};
use tokio::sync::{OwnedSemaphorePermit, Semaphore};
use tokio::task::JoinSet;
use tokio::time::{Instant, sleep_until, timeout};

use crate::args::Config;
use crate::preface;

pub const ALPN: &[u8] = b"hand-board";
pub const FULL_CODE: VarInt = VarInt::from_u32(1);
pub const IDLE_CODE: VarInt = VarInt::from_u32(2);
pub const BOARD_UNREACHABLE_CODE: VarInt = VarInt::from_u32(3);
pub const STREAM_IDLE_CODE: VarInt = VarInt::from_u32(4);
pub const BOARD_ERROR_CODE: VarInt = VarInt::from_u32(5);
pub const STREAMS_PER_CONNECTION: u32 = 12;
const STREAMS_PER_SECOND: f64 = 20.0;

#[derive(Clone, Copy)]
pub struct Limits {
    pub handshakes: usize,
    pub handshake_wait: Duration,
    pub per_source: usize,
}

impl Default for Limits {
    fn default() -> Self {
        Self {
            handshakes: 8,
            handshake_wait: Duration::from_secs(3),
            per_source: 4,
        }
    }
}

pub async fn serve(endpoint: Endpoint, config: Arc<Config>, limits: Limits) {
    let slots = Arc::new(Semaphore::new(config.max_connections));
    let handshakes = Arc::new(Semaphore::new(limits.handshakes));
    let sources = Arc::new(Sources::default());
    while let Some(incoming) = endpoint.accept().await {
        if !incoming.remote_addr_validated() {
            let _ = incoming.retry();
            continue;
        }
        let Some((handshake, source)) =
            admit(&incoming, &slots, &handshakes, &sources, limits.per_source)
        else {
            incoming.refuse();
            continue;
        };
        tokio::spawn(connect(
            incoming,
            handshake,
            source,
            slots.clone(),
            config.clone(),
            limits.handshake_wait,
        ));
    }
}

fn admit(
    incoming: &Incoming,
    slots: &Semaphore,
    handshakes: &Arc<Semaphore>,
    sources: &Arc<Sources>,
    per_source: usize,
) -> Option<(OwnedSemaphorePermit, Held)> {
    let source = Source::of(&incoming.remote_addr())?;
    if slots.available_permits() == 0 {
        return None;
    }
    let handshake = handshakes.clone().try_acquire_owned().ok()?;
    let held = sources.take(source, per_source)?;
    Some((handshake, held))
}

async fn connect(
    incoming: Incoming,
    handshake: OwnedSemaphorePermit,
    _source: Held,
    slots: Arc<Semaphore>,
    config: Arc<Config>,
    handshake_wait: Duration,
) {
    let Ok(accepting) = incoming.accept() else {
        return;
    };
    let Ok(Ok(connection)) = timeout(handshake_wait, accepting).await else {
        return;
    };
    drop(handshake);
    let Ok(_slot) = slots.try_acquire_owned() else {
        connection.close(FULL_CODE, b"full");
        return;
    };
    serve_connection(connection, config).await;
}

#[derive(Clone, PartialEq, Eq, Hash)]
enum Source {
    Network(IpAddr),
    Relayed(EndpointId),
}

impl Source {
    fn of(addr: &IncomingAddr) -> Option<Self> {
        match addr {
            IncomingAddr::Ip(addr) => Some(Self::Network(network(addr.ip()))),
            IncomingAddr::Relay { endpoint_id, .. } => Some(Self::Relayed(*endpoint_id)),
            _ => None,
        }
    }
}

fn network(ip: IpAddr) -> IpAddr {
    match ip.to_canonical() {
        IpAddr::V6(v6) => IpAddr::V6(Ipv6Addr::from(u128::from(v6) & (!0u128 << 64))),
        v4 => v4,
    }
}

#[derive(Default)]
struct Sources(Mutex<HashMap<Source, usize>>);

impl Sources {
    fn take(self: &Arc<Self>, source: Source, max: usize) -> Option<Held> {
        let mut counts = self.0.lock().unwrap();
        let count = counts.entry(source.clone()).or_insert(0);
        if *count >= max {
            return None;
        }
        *count += 1;
        Some(Held {
            sources: self.clone(),
            source,
        })
    }
}

struct Held {
    sources: Arc<Sources>,
    source: Source,
}

impl Drop for Held {
    fn drop(&mut self) {
        let mut counts = self.sources.0.lock().unwrap();
        if let Some(count) = counts.get_mut(&self.source) {
            *count -= 1;
            if *count == 0 {
                counts.remove(&self.source);
            }
        }
    }
}

struct Bucket {
    tokens: f64,
    at: Instant,
}

impl Bucket {
    fn new() -> Self {
        Self {
            tokens: f64::from(STREAMS_PER_CONNECTION),
            at: Instant::now(),
        }
    }

    fn refill(&mut self) -> Instant {
        let now = Instant::now();
        let earned = now.duration_since(self.at).as_secs_f64() * STREAMS_PER_SECOND;
        self.tokens = (self.tokens + earned).min(f64::from(STREAMS_PER_CONNECTION));
        self.at = now;
        now
    }

    fn ready_at(&mut self) -> Instant {
        let now = self.refill();
        if self.tokens >= 1.0 {
            now
        } else {
            now + Duration::from_secs_f64((1.0 - self.tokens) / STREAMS_PER_SECOND)
        }
    }

    fn take(&mut self) {
        self.refill();
        self.tokens -= 1.0;
    }
}

async fn serve_connection(connection: Connection, config: Arc<Config>) {
    let peer = connection.remote_id();
    let activity = Arc::new(Activity::new(None));
    let mut bucket = Bucket::new();
    let mut streams = JoinSet::new();
    loop {
        let ready = bucket.ready_at();
        tokio::select! {
            accepted = async {
                sleep_until(ready).await;
                connection.accept_bi().await
            } => {
                let Ok((send, recv)) = accepted else { break };
                bucket.take();
                streams.spawn(forward(send, recv, peer, config.clone(), activity.clone()));
            }
            Some(_) = streams.join_next(), if !streams.is_empty() => {}
            () = activity.quiet_for(config.idle) => {
                connection.close(IDLE_CODE, b"idle");
                break;
            }
        }
    }
}

async fn forward(
    mut send: SendStream,
    mut recv: RecvStream,
    peer: EndpointId,
    config: Arc<Config>,
    connection_activity: Arc<Activity>,
) {
    let preface = preface::line(&peer, &config.secret);
    let opening = open_board(config.board, &preface);
    let Some(board) = timeout(config.idle, opening).await.ok().flatten() else {
        let _ = send.reset(BOARD_UNREACHABLE_CODE);
        let _ = recv.stop(BOARD_UNREACHABLE_CODE);
        return;
    };
    let activity = Arc::new(Activity::new(Some(connection_activity)));
    let (board_read, board_write) = board.into_split();
    let mut board_write = Touch::wrap(board_write, activity.clone());
    let mut send = Touch::wrap(send, activity.clone());
    let quiet = tokio::select! {
        _ = async {
            tokio::join!(
                upload(&mut recv, &mut board_write),
                download(board_read, &mut send),
            )
        } => false,
        () = activity.quiet_for(config.idle) => true,
    };
    if quiet {
        let _ = send.inner.reset(STREAM_IDLE_CODE);
        let _ = recv.stop(STREAM_IDLE_CODE);
    }
}

async fn upload(recv: &mut RecvStream, board: &mut Touch<OwnedWriteHalf>) {
    match copy(recv, board).await {
        Ok(_) => {
            let _ = board.shutdown().await;
        }
        Err(_) => {
            let _ = recv.stop(BOARD_ERROR_CODE);
        }
    }
}

async fn download(mut board: OwnedReadHalf, send: &mut Touch<SendStream>) {
    match copy(&mut board, send).await {
        Ok(_) => {
            let _ = send.inner.finish();
        }
        Err(_) => {
            let _ = send.inner.reset(BOARD_ERROR_CODE);
        }
    }
}

async fn open_board(board: SocketAddr, preface: &[u8]) -> Option<TcpStream> {
    let mut stream = TcpStream::connect(board).await.ok()?;
    stream.write_all(preface).await.ok()?;
    Some(stream)
}

pub struct Activity {
    start: Instant,
    last_ms: AtomicU64,
    parent: Option<Arc<Activity>>,
}

impl Activity {
    pub fn new(parent: Option<Arc<Activity>>) -> Self {
        Self {
            start: Instant::now(),
            last_ms: AtomicU64::new(0),
            parent,
        }
    }

    fn touch(&self) {
        self.last_ms
            .store(self.start.elapsed().as_millis() as u64, Ordering::Relaxed);
        if let Some(parent) = &self.parent {
            parent.touch();
        }
    }

    fn last(&self) -> Instant {
        self.start + Duration::from_millis(self.last_ms.load(Ordering::Relaxed))
    }

    pub async fn quiet_for(&self, idle: Duration) {
        loop {
            sleep_until(self.last() + idle).await;
            if self.last() + idle <= Instant::now() {
                return;
            }
        }
    }
}

pub struct Touch<T> {
    inner: T,
    activity: Arc<Activity>,
}

impl<T> Touch<T> {
    pub fn wrap(inner: T, activity: Arc<Activity>) -> Self {
        Self { inner, activity }
    }
}

impl<T: AsyncWrite + Unpin> AsyncWrite for Touch<T> {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        let polled = Pin::new(&mut self.inner).poll_write(cx, buf);
        if matches!(polled, Poll::Ready(Ok(moved)) if moved > 0) {
            self.activity.touch();
        }
        polled
    }

    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_flush(cx)
    }

    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_shutdown(cx)
    }
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;
    use std::collections::hash_map::Entry;
    use std::net::SocketAddr;
    use std::path::PathBuf;

    use iroh::endpoint::{ConnectionError, PortmapperConfig, ReadError, ReadToEndError, presets};
    use iroh::{EndpointAddr, RelayMode, SecretKey};
    use tokio::io::{AsyncBufReadExt, AsyncReadExt, BufReader};
    use tokio::net::{TcpListener, UdpSocket};
    use tokio::sync::mpsc;
    use tokio::task::JoinHandle;
    use tokio::time::sleep;

    use super::*;
    use crate::args::{Relay, Secret};

    const SECRET: &str = "0123456789abcdef0123456789abcdef0123456789abcdef";
    const WAIT: Duration = Duration::from_secs(10);

    #[tokio::test(start_paused = true)]
    async fn quiet_for_waits_from_the_last_touch() {
        let activity = Arc::new(Activity::new(None));
        let waiting = tokio::spawn({
            let activity = activity.clone();
            async move { activity.quiet_for(Duration::from_secs(10)).await }
        });
        tokio::time::advance(Duration::from_secs(6)).await;
        activity.touch();
        tokio::time::advance(Duration::from_secs(6)).await;
        assert!(!waiting.is_finished());
        tokio::time::advance(Duration::from_secs(5)).await;
        waiting.await.unwrap();
    }

    #[tokio::test(start_paused = true)]
    async fn a_stream_touch_moves_its_connection_clock_and_an_empty_write_does_not() {
        let connection = Arc::new(Activity::new(None));
        let stream = Arc::new(Activity::new(Some(connection.clone())));
        let mut sink = Touch::wrap(tokio::io::sink(), stream.clone());
        let waiting = tokio::spawn({
            let connection = connection.clone();
            async move { connection.quiet_for(Duration::from_secs(10)).await }
        });
        tokio::time::advance(Duration::from_secs(6)).await;
        assert_eq!(sink.write(&[]).await.unwrap(), 0);
        tokio::time::advance(Duration::from_secs(6)).await;
        tokio::task::yield_now().await;
        assert!(waiting.is_finished());

        let connection = Arc::new(Activity::new(None));
        let stream = Arc::new(Activity::new(Some(connection.clone())));
        let mut sink = Touch::wrap(tokio::io::sink(), stream);
        let waiting = tokio::spawn({
            let connection = connection.clone();
            async move { connection.quiet_for(Duration::from_secs(10)).await }
        });
        tokio::time::advance(Duration::from_secs(6)).await;
        sink.write_all(b"x").await.unwrap();
        tokio::time::advance(Duration::from_secs(6)).await;
        tokio::task::yield_now().await;
        assert!(!waiting.is_finished());
    }

    #[tokio::test(start_paused = true)]
    async fn streams_are_accepted_in_a_burst_and_then_at_a_steady_rate() {
        let mut bucket = Bucket::new();
        let start = Instant::now();
        for _ in 0..STREAMS_PER_CONNECTION {
            assert_eq!(bucket.ready_at(), start);
            bucket.take();
        }
        let step = Duration::from_secs_f64(1.0 / STREAMS_PER_SECOND);
        let wait = bucket.ready_at() - start;
        assert!(wait.abs_diff(step) < Duration::from_millis(1), "{wait:?}");
        tokio::time::advance(step).await;
        assert!(bucket.ready_at() - Instant::now() < Duration::from_millis(1));
        bucket.take();
        assert!(bucket.ready_at() - Instant::now() > step / 2);
    }

    #[test]
    fn the_close_and_reset_codes_are_the_documented_distinct_ones() {
        let codes = [
            FULL_CODE,
            IDLE_CODE,
            BOARD_UNREACHABLE_CODE,
            STREAM_IDLE_CODE,
            BOARD_ERROR_CODE,
        ]
        .map(VarInt::into_inner);
        assert_eq!(codes, [1, 2, 3, 4, 5]);
    }

    #[test]
    fn a_source_is_an_ipv4_address_or_an_ipv6_prefix() {
        let ip = |text: &str| text.parse::<IpAddr>().unwrap();
        assert_eq!(
            network(ip("2001:db8:1:2:aaaa::1")),
            network(ip("2001:db8:1:2:bbbb::9"))
        );
        assert_ne!(
            network(ip("2001:db8:1:2::1")),
            network(ip("2001:db8:1:3::1"))
        );
        assert_eq!(network(ip("::ffff:192.0.2.1")), ip("192.0.2.1"));
        assert_ne!(network(ip("192.0.2.1")), network(ip("192.0.2.2")));
    }

    #[test]
    fn a_source_holds_at_most_its_share_and_gives_it_back() {
        let sources = Arc::new(Sources::default());
        let one = Source::Network("192.0.2.1".parse().unwrap());
        let other = Source::Network("192.0.2.2".parse().unwrap());
        let first = sources.take(one.clone(), 2).unwrap();
        let _second = sources.take(one.clone(), 2).unwrap();
        assert!(sources.take(one.clone(), 2).is_none());
        assert!(sources.take(other, 2).is_some());
        drop(first);
        assert!(sources.take(one, 2).is_some());
        drop(_second);
        assert_eq!(sources.0.lock().unwrap().len(), 0);
    }

    struct Board {
        addr: SocketAddr,
        prefaces: mpsc::UnboundedReceiver<String>,
    }

    async fn board() -> Board {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let (sent, prefaces) = mpsc::unbounded_channel();
        tokio::spawn(async move {
            while let Ok((stream, _)) = listener.accept().await {
                let sent = sent.clone();
                tokio::spawn(async move {
                    let (reader, mut writer) = stream.into_split();
                    let mut reader = BufReader::new(reader);
                    let mut line = String::new();
                    reader.read_line(&mut line).await.unwrap();
                    sent.send(line).unwrap();
                    let mut chunk = [0u8; 64];
                    loop {
                        match reader.read(&mut chunk).await {
                            Ok(n) if n > 0 => writer.write_all(&chunk[..n]).await.unwrap(),
                            _ => break,
                        }
                    }
                    writer.shutdown().await.ok();
                });
            }
        });
        Board { addr, prefaces }
    }

    async fn closed_port() -> SocketAddr {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        listener.local_addr().unwrap()
    }

    fn client_builder() -> iroh::endpoint::Builder {
        Endpoint::builder(presets::Minimal)
            .relay_mode(RelayMode::Disabled)
            .portmapper_config(PortmapperConfig::Disabled)
            .clear_ip_transports()
            .bind_addr("127.0.0.1:0")
            .unwrap()
    }

    struct Sidecar {
        endpoint: Endpoint,
        addr: EndpointAddr,
        served: JoinHandle<()>,
    }

    fn config(board: SocketAddr, max_connections: usize, idle: Duration) -> Arc<Config> {
        Arc::new(Config {
            board,
            secret: Secret::parse(SECRET).unwrap(),
            key_dir: PathBuf::new(),
            relay: Relay::None,
            max_connections,
            idle,
        })
    }

    async fn sidecar(board: SocketAddr, max_connections: usize, idle: Duration) -> Sidecar {
        sidecar_with(board, max_connections, idle, Limits::default()).await
    }

    async fn sidecar_with(
        board: SocketAddr,
        max_connections: usize,
        idle: Duration,
        limits: Limits,
    ) -> Sidecar {
        let config = config(board, max_connections, idle);
        let endpoint = crate::builder(&config)
            .secret_key(SecretKey::generate())
            .bind()
            .await
            .unwrap();
        let addr = EndpointAddr::new(endpoint.id()).with_ip_addr(endpoint.bound_sockets()[0]);
        let served = tokio::spawn(serve(endpoint.clone(), config, limits));
        Sidecar {
            endpoint,
            addr,
            served,
        }
    }

    async fn dialer() -> Endpoint {
        client_builder()
            .secret_key(SecretKey::generate())
            .bind()
            .await
            .unwrap()
    }

    async fn ask(connection: &Connection, request: &[u8]) -> Vec<u8> {
        let (mut send, mut recv) = connection.open_bi().await.unwrap();
        send.write_all(request).await.unwrap();
        send.finish().unwrap();
        recv.read_to_end(1 << 20).await.unwrap()
    }

    async fn dropping_proxy(to: SocketAddr, pass: u64) -> SocketAddr {
        let front = Arc::new(UdpSocket::bind("127.0.0.1:0").await.unwrap());
        let addr = front.local_addr().unwrap();
        tokio::spawn(async move {
            let mut upstreams: HashMap<SocketAddr, (Arc<UdpSocket>, u64)> = HashMap::new();
            let mut buf = vec![0u8; 65536];
            while let Ok((n, from)) = front.recv_from(&mut buf).await {
                let (up, sent) = match upstreams.entry(from) {
                    Entry::Occupied(known) => known.into_mut(),
                    Entry::Vacant(new) => {
                        let up = Arc::new(UdpSocket::bind("127.0.0.1:0").await.unwrap());
                        up.connect(to).await.unwrap();
                        tokio::spawn({
                            let (front, up) = (front.clone(), up.clone());
                            async move {
                                let mut buf = vec![0u8; 65536];
                                let mut seen = 0;
                                while let Ok(n) = up.recv(&mut buf).await {
                                    seen += 1;
                                    if seen <= pass {
                                        let _ = front.send_to(&buf[..n], from).await;
                                    }
                                }
                            }
                        });
                        new.insert((up, 0))
                    }
                };
                *sent += 1;
                if *sent <= pass {
                    let _ = up.send(&buf[..n]).await;
                }
            }
        });
        addr
    }

    fn half_open(through: SocketAddr, target: &Sidecar, count: usize) -> Vec<JoinHandle<()>> {
        let addr = EndpointAddr::new(target.endpoint.id()).with_ip_addr(through);
        (0..count)
            .map(|_| {
                let addr = addr.clone();
                tokio::spawn(async move {
                    let client = dialer().await;
                    let _ = timeout(WAIT, client.connect(addr, ALPN)).await;
                })
            })
            .collect()
    }

    async fn served_soon(sidecar: &Sidecar) -> bool {
        let late = dialer().await;
        match timeout(
            Duration::from_secs(2),
            late.connect(sidecar.addr.clone(), ALPN),
        )
        .await
        {
            Ok(Ok(connection)) => ask(&connection, b"ping").await == b"ping",
            _ => false,
        }
    }

    #[tokio::test]
    async fn a_peer_reaches_the_board_with_its_id_and_the_secret() {
        let mut board = board().await;
        let sidecar = sidecar(board.addr, 4, WAIT).await;
        let first = dialer().await;
        let second = dialer().await;

        let connection = first.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        assert_eq!(ask(&connection, b"ping").await, b"ping");
        assert_eq!(
            board.prefaces.recv().await.unwrap(),
            format!("HAND-PEER 1 {} {SECRET}\n", first.id())
        );

        let connection = second.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        assert_eq!(ask(&connection, b"pong").await, b"pong");
        assert_eq!(
            board.prefaces.recv().await.unwrap(),
            format!("HAND-PEER 1 {} {SECRET}\n", second.id())
        );
        assert_ne!(first.id(), second.id());
    }

    #[tokio::test]
    async fn one_connection_carries_many_streams_with_their_own_board_connections() {
        let mut board = board().await;
        let sidecar = sidecar(board.addr, 4, WAIT).await;
        let client = dialer().await;
        let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        let asked: Vec<_> = (0..5)
            .map(|n| {
                let connection = connection.clone();
                tokio::spawn(async move { ask(&connection, format!("req{n}").as_bytes()).await })
            })
            .collect();
        let mut got = Vec::new();
        for task in asked {
            got.push(String::from_utf8(task.await.unwrap()).unwrap());
        }
        got.sort();
        assert_eq!(got, ["req0", "req1", "req2", "req3", "req4"]);
        for _ in 0..5 {
            assert_eq!(
                board.prefaces.recv().await.unwrap(),
                format!("HAND-PEER 1 {} {SECRET}\n", client.id())
            );
        }
    }

    #[tokio::test]
    async fn an_unreachable_board_resets_the_stream_and_keeps_the_connection() {
        let sidecar = sidecar(closed_port().await, 4, WAIT).await;
        let client = dialer().await;
        let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        let (mut send, mut recv) = connection.open_bi().await.unwrap();
        send.write_all(b"ping").await.unwrap();
        let error = recv.read_to_end(1 << 20).await.unwrap_err();
        assert!(
            matches!(error, ReadToEndError::Read(ReadError::Reset(code)) if code == BOARD_UNREACHABLE_CODE),
            "{error:?}"
        );
        assert!(connection.close_reason().is_none());
    }

    #[tokio::test]
    async fn a_board_that_resets_its_connection_is_a_board_error_not_an_idle_stream() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let board = listener.local_addr().unwrap();
        tokio::spawn(async move {
            while let Ok((stream, _)) = listener.accept().await {
                let mut reader = BufReader::new(stream);
                let mut line = String::new();
                reader.read_line(&mut line).await.unwrap();
                reader.get_ref().set_zero_linger().unwrap();
            }
        });
        let sidecar = sidecar(board, 4, WAIT).await;
        let client = dialer().await;
        let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        let (mut send, mut recv) = connection.open_bi().await.unwrap();
        send.write_all(b"ping").await.unwrap();
        let error = timeout(WAIT, recv.read_to_end(1 << 20))
            .await
            .unwrap()
            .unwrap_err();
        assert!(
            matches!(error, ReadToEndError::Read(ReadError::Reset(code)) if code == BOARD_ERROR_CODE),
            "{error:?}"
        );
        assert!(connection.close_reason().is_none());
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn a_board_that_answers_and_closes_at_once_still_gets_its_answer_to_the_phone() {
        use iroh::endpoint::WriteError;
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let board = listener.local_addr().unwrap();
        tokio::spawn(async move {
            while let Ok((stream, _)) = listener.accept().await {
                tokio::spawn(async move {
                    let (reader, mut writer) = stream.into_split();
                    let mut reader = BufReader::new(reader);
                    let mut line = String::new();
                    reader.read_line(&mut line).await.unwrap();
                    writer.write_all(&vec![b'a'; 64 * 1024]).await.unwrap();
                });
            }
        });
        let sidecar = sidecar(board, 4, WAIT).await;
        let client = dialer().await;
        let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        let (mut send, mut recv) = connection.open_bi().await.unwrap();
        let uploading = tokio::spawn(async move { send.write_all(&vec![b'u'; 8 << 20]).await });
        let answer = timeout(WAIT, recv.read_to_end(1 << 20))
            .await
            .unwrap()
            .unwrap();
        assert_eq!(answer.len(), 64 * 1024);
        let stopped = timeout(WAIT, uploading)
            .await
            .unwrap()
            .unwrap()
            .unwrap_err();
        assert!(
            matches!(stopped, WriteError::Stopped(code) if code == BOARD_ERROR_CODE),
            "{stopped:?}"
        );
    }

    #[tokio::test]
    async fn dialers_past_the_cap_are_refused_until_a_slot_frees() {
        let board = board().await;
        let sidecar = sidecar(board.addr, 1, WAIT).await;
        let holder = dialer().await;
        let kept = holder.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        assert_eq!(ask(&kept, b"ping").await, b"ping");

        let late = dialer().await;
        let refused = timeout(WAIT, late.connect(sidecar.addr.clone(), ALPN))
            .await
            .unwrap();
        assert!(refused.is_err());

        kept.close(VarInt::from_u32(0), b"done");
        let admitted = timeout(WAIT, async {
            loop {
                if let Ok(connection) = late.connect(sidecar.addr.clone(), ALPN).await {
                    break connection;
                }
                sleep(Duration::from_millis(50)).await;
            }
        })
        .await
        .unwrap();
        assert_eq!(ask(&admitted, b"again").await, b"again");
    }

    #[tokio::test]
    async fn one_source_may_hold_only_its_share_of_the_connections() {
        let board = board().await;
        let limits = Limits {
            per_source: 2,
            ..Limits::default()
        };
        let sidecar = sidecar_with(board.addr, 8, WAIT, limits).await;
        let mut kept = Vec::new();
        for _ in 0..2 {
            let client = dialer().await;
            let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();
            assert_eq!(ask(&connection, b"ping").await, b"ping");
            kept.push((client, connection));
        }
        let late = dialer().await;
        let refused = timeout(WAIT, late.connect(sidecar.addr.clone(), ALPN))
            .await
            .unwrap();
        assert!(refused.is_err());

        let (leaver, connection) = kept.pop().unwrap();
        connection.close(VarInt::from_u32(0), b"done");
        leaver.close().await;
        let admitted = timeout(WAIT, async {
            loop {
                if let Ok(connection) = late.connect(sidecar.addr.clone(), ALPN).await {
                    break connection;
                }
                sleep(Duration::from_millis(50)).await;
            }
        })
        .await
        .unwrap();
        assert_eq!(ask(&admitted, b"again").await, b"again");
    }

    #[tokio::test]
    async fn a_handshake_that_never_finishes_frees_its_place_after_the_wait() {
        let board = board().await;
        let limits = Limits {
            handshakes: 2,
            handshake_wait: Duration::from_millis(1500),
            per_source: 100,
        };
        let sidecar = sidecar_with(board.addr, 8, WAIT, limits).await;
        let proxy = dropping_proxy(sidecar.endpoint.bound_sockets()[0], 2).await;
        let _stuck = half_open(proxy, &sidecar, 2);
        sleep(Duration::from_millis(300)).await;
        assert!(!served_soon(&sidecar).await);

        let admitted = timeout(WAIT, async {
            loop {
                if served_soon(&sidecar).await {
                    break;
                }
            }
        })
        .await;
        assert!(admitted.is_ok());
    }

    #[tokio::test]
    async fn half_open_handshakes_do_not_take_connection_slots() {
        let board = board().await;
        let limits = Limits {
            per_source: 100,
            ..Limits::default()
        };
        let sidecar = sidecar_with(board.addr, 1, WAIT, limits).await;
        let proxy = dropping_proxy(sidecar.endpoint.bound_sockets()[0], 2).await;
        let _stuck = half_open(proxy, &sidecar, 3);
        sleep(Duration::from_millis(300)).await;
        assert!(served_soon(&sidecar).await);
    }

    #[tokio::test]
    async fn a_first_packet_from_an_unproven_address_costs_the_sidecar_nothing() {
        let board = board().await;
        let limits = Limits {
            handshakes: 2,
            per_source: 100,
            ..Limits::default()
        };
        let sidecar = sidecar_with(board.addr, 8, WAIT, limits).await;
        let proxy = dropping_proxy(sidecar.endpoint.bound_sockets()[0], 1).await;
        let _stuck = half_open(proxy, &sidecar, 6);
        sleep(Duration::from_millis(300)).await;
        assert!(served_soon(&sidecar).await);
    }

    #[tokio::test]
    async fn a_connection_with_no_stream_is_closed_after_the_idle_time() {
        let board = board().await;
        let sidecar = sidecar(board.addr, 4, Duration::from_secs(1)).await;
        let client = dialer().await;
        let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        let closed = timeout(WAIT, connection.closed()).await.unwrap();
        assert!(
            matches!(closed, ConnectionError::ApplicationClosed(ref c) if c.error_code == IDLE_CODE),
            "{closed:?}"
        );
    }

    #[tokio::test]
    async fn empty_streams_do_not_keep_a_connection_open() {
        let board = board().await;
        let sidecar = sidecar(board.addr, 4, Duration::from_secs(1)).await;
        let client = dialer().await;
        let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        let opener = tokio::spawn({
            let connection = connection.clone();
            async move {
                while let Ok((mut send, mut recv)) = connection.open_bi().await {
                    let _ = send.finish();
                    let _ = recv.read_to_end(16).await;
                    sleep(Duration::from_millis(200)).await;
                }
            }
        });
        let closed = timeout(WAIT, connection.closed()).await.unwrap();
        opener.abort();
        assert!(
            matches!(closed, ConnectionError::ApplicationClosed(ref c) if c.error_code == IDLE_CODE),
            "{closed:?}"
        );
    }

    #[tokio::test]
    async fn a_silent_stream_is_reset_and_a_busy_one_is_not() {
        let board = board().await;
        let sidecar = sidecar(board.addr, 4, Duration::from_secs(1)).await;
        let client = dialer().await;
        let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();

        let (mut busy_send, mut busy_recv) = connection.open_bi().await.unwrap();
        let (mut quiet_send, mut quiet_recv) = connection.open_bi().await.unwrap();
        quiet_send.write_all(b"x").await.unwrap();
        let mut one = [0u8; 1];
        quiet_recv.read_exact(&mut one).await.unwrap();

        for round in 0..6u8 {
            busy_send.write_all(&[round]).await.unwrap();
            busy_recv.read_exact(&mut one).await.unwrap();
            assert_eq!(one[0], round);
            sleep(Duration::from_millis(400)).await;
        }
        let error = timeout(WAIT, quiet_recv.read_to_end(16))
            .await
            .unwrap()
            .unwrap_err();
        assert!(
            matches!(error, ReadToEndError::Read(ReadError::Reset(code)) if code == STREAM_IDLE_CODE),
            "{error:?}"
        );
        busy_send.finish().unwrap();
        assert!(busy_recv.read_to_end(16).await.unwrap().is_empty());
        assert!(connection.close_reason().is_none());
    }

    #[tokio::test]
    async fn a_connection_may_hold_only_so_many_streams_at_once() {
        let board = board().await;
        let sidecar = sidecar(board.addr, 4, WAIT).await;
        let client = dialer().await;
        let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        let mut held = Vec::new();
        for _ in 0..STREAMS_PER_CONNECTION {
            let (mut send, recv) = connection.open_bi().await.unwrap();
            send.write_all(b"x").await.unwrap();
            held.push((send, recv));
        }
        let blocked = timeout(Duration::from_millis(500), connection.open_bi()).await;
        assert!(blocked.is_err());
        for _ in 0..STREAMS_PER_CONNECTION / 4 {
            let (mut send, mut recv) = held.pop().unwrap();
            send.finish().unwrap();
            recv.read_to_end(16).await.unwrap();
        }
        timeout(WAIT, connection.open_bi()).await.unwrap().unwrap();
    }

    #[tokio::test]
    async fn a_connection_may_not_open_one_way_streams() {
        let board = board().await;
        let sidecar = sidecar(board.addr, 4, WAIT).await;
        let client = dialer().await;
        let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        let blocked = timeout(Duration::from_millis(500), connection.open_uni()).await;
        assert!(blocked.is_err());
    }

    #[tokio::test]
    async fn a_dialer_cannot_open_streams_faster_than_the_rate() {
        let board = board().await;
        let sidecar = sidecar(board.addr, 4, WAIT).await;
        let client = dialer().await;
        let connection = client.connect(sidecar.addr.clone(), ALPN).await.unwrap();
        let started = std::time::Instant::now();
        for n in 0..STREAMS_PER_CONNECTION * 2 {
            assert_eq!(ask(&connection, &[n as u8]).await, [n as u8]);
        }
        assert!(
            started.elapsed() >= Duration::from_millis(500),
            "{:?}",
            started.elapsed()
        );
    }

    #[tokio::test]
    async fn closing_the_endpoint_ends_the_accept_loop() {
        let board = board().await;
        let sidecar = sidecar(board.addr, 4, WAIT).await;
        sidecar.endpoint.close().await;
        timeout(WAIT, sidecar.served).await.unwrap().unwrap();
    }
}
