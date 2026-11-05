use std::io;
use std::net::SocketAddr;
use std::pin::Pin;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::task::{Context, Poll};
use std::time::Duration;

use iroh::EndpointId;
use iroh::endpoint::{Connection, Endpoint, RecvStream, SendStream, VarInt};
use tokio::io::{AsyncRead, AsyncWrite, AsyncWriteExt, ReadBuf, copy_bidirectional};
use tokio::net::TcpStream;
use tokio::sync::Semaphore;
use tokio::task::JoinSet;
use tokio::time::{Instant, sleep, sleep_until, timeout};

use crate::args::Config;
use crate::preface;

const HANDSHAKE_WAIT: Duration = Duration::from_secs(10);
pub const IDLE_CODE: VarInt = VarInt::from_u32(2);
pub const BOARD_UNREACHABLE_CODE: VarInt = VarInt::from_u32(3);
pub const STREAM_IDLE_CODE: VarInt = VarInt::from_u32(4);
pub const STREAMS_PER_CONNECTION: u32 = 32;

pub async fn serve(endpoint: Endpoint, config: Arc<Config>) {
    let slots = Arc::new(Semaphore::new(config.max_connections));
    while let Some(incoming) = endpoint.accept().await {
        let Ok(slot) = slots.clone().try_acquire_owned() else {
            incoming.refuse();
            continue;
        };
        let config = config.clone();
        tokio::spawn(async move {
            let _slot = slot;
            let Ok(accepting) = incoming.accept() else {
                return;
            };
            let Ok(Ok(connection)) = timeout(HANDSHAKE_WAIT, accepting).await else {
                return;
            };
            serve_connection(connection, config).await;
        });
    }
}

async fn serve_connection(connection: Connection, config: Arc<Config>) {
    let peer = connection.remote_id();
    let mut streams = JoinSet::new();
    loop {
        tokio::select! {
            accepted = connection.accept_bi() => {
                let Ok((send, recv)) = accepted else { break };
                streams.spawn(forward(send, recv, peer, config.clone()));
            }
            Some(_) = streams.join_next(), if !streams.is_empty() => {}
            () = sleep(config.idle), if streams.is_empty() => {
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
) {
    let preface = preface::line(&peer, &config.secret);
    let opening = open_board(config.board, &preface);
    let Some(board) = timeout(config.idle, opening).await.ok().flatten() else {
        let _ = send.reset(BOARD_UNREACHABLE_CODE);
        let _ = recv.stop(BOARD_UNREACHABLE_CODE);
        return;
    };
    let activity = Arc::new(Activity::new());
    let mut board = Touch::wrap(board, activity.clone());
    let mut quic = Touch::wrap(tokio::io::join(recv, send), activity.clone());
    let finished = tokio::select! {
        copied = copy_bidirectional(&mut quic, &mut board) => copied.is_ok(),
        () = activity.quiet_for(config.idle) => false,
    };
    if !finished {
        let _ = quic.inner.writer_mut().reset(STREAM_IDLE_CODE);
        let _ = quic.inner.reader_mut().stop(STREAM_IDLE_CODE);
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
}

impl Activity {
    pub fn new() -> Self {
        Self {
            start: Instant::now(),
            last_ms: AtomicU64::new(0),
        }
    }

    fn touch(&self) {
        self.last_ms
            .store(self.start.elapsed().as_millis() as u64, Ordering::Relaxed);
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

impl<T: AsyncRead + Unpin> AsyncRead for Touch<T> {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        let polled = Pin::new(&mut self.inner).poll_read(cx, buf);
        if polled.is_ready() {
            self.activity.touch();
        }
        polled
    }
}

impl<T: AsyncWrite + Unpin> AsyncWrite for Touch<T> {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        let polled = Pin::new(&mut self.inner).poll_write(cx, buf);
        if polled.is_ready() {
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
    use std::net::SocketAddr;
    use std::path::PathBuf;

    use iroh::endpoint::{ConnectionError, PortmapperConfig, ReadError, ReadToEndError, presets};
    use iroh::{EndpointAddr, RelayMode, SecretKey};
    use tokio::io::{AsyncBufReadExt, AsyncReadExt, BufReader};
    use tokio::net::TcpListener;
    use tokio::sync::mpsc;

    use super::*;
    use crate::args::{Relay, Secret};

    const SECRET: &str = "0123456789abcdef0123456789abcdef0123456789abcdef";
    const ALPN: &[u8] = b"hand-board";
    const WAIT: Duration = Duration::from_secs(10);

    #[tokio::test(start_paused = true)]
    async fn quiet_for_waits_from_the_last_touch() {
        let activity = Arc::new(Activity::new());
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
        served: tokio::task::JoinHandle<()>,
    }

    async fn sidecar(board: SocketAddr, max_connections: usize, idle: Duration) -> Sidecar {
        let config = Arc::new(Config {
            board,
            secret: Secret::parse(SECRET).unwrap(),
            key_dir: PathBuf::new(),
            alpn: "hand-board".to_owned(),
            relay: Relay::None,
            max_connections,
            idle,
        });
        let endpoint = crate::builder(&config)
            .clear_ip_transports()
            .bind_addr("127.0.0.1:0")
            .unwrap()
            .secret_key(SecretKey::generate())
            .bind()
            .await
            .unwrap();
        let addr = EndpointAddr::new(endpoint.id()).with_ip_addr(endpoint.bound_sockets()[0]);
        let served = tokio::spawn(serve(endpoint.clone(), config));
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
    async fn closing_the_endpoint_ends_the_accept_loop() {
        let board = board().await;
        let sidecar = sidecar(board.addr, 4, WAIT).await;
        sidecar.endpoint.close().await;
        timeout(WAIT, sidecar.served).await.unwrap().unwrap();
    }
}
