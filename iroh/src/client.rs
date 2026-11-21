use std::io;
use std::net::{Ipv4Addr, SocketAddr};
use std::pin::pin;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use iroh::address_lookup::{DnsAddressLookup, PkarrResolver};
use iroh::endpoint::{
    Builder, ConnectError, ConnectingError, Connection, ConnectionError, Endpoint,
    PortmapperConfig, QuicTransportConfig, ReadError, RecvStream, SendStream, TransportErrorCode,
    VarInt, WriteError, presets,
};
use iroh::{EndpointAddr, SecretKey};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::tcp::{OwnedReadHalf, OwnedWriteHalf};
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::{Mutex as AsyncMutex, Semaphore};
use tokio::time::{Instant, sleep, sleep_until, timeout};

use crate::args::{ClientConfig, Relay};
use crate::backoff::Backoff;
use crate::forward::{
    ALPN, BOARD_ERROR_CODE, BOARD_UNREACHABLE_CODE, FULL_CODE, IDLE_CODE, STREAMS_PER_CONNECTION,
};

pub const HANDSHAKE_DEADLINE: Duration = Duration::from_secs(20);
pub const BACKOFF_RESET: Duration = Duration::from_secs(10);
const CLOSE_WAIT: Duration = Duration::from_secs(3);
const CHUNK: usize = 16 << 10;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum State {
    Connecting,
    Connected,
    Busy,
    Unreachable,
    BoardDown,
    Lost,
}

impl State {
    pub fn name(self) -> &'static str {
        match self {
            Self::Connecting => "connecting",
            Self::Connected => "connected",
            Self::Busy => "busy",
            Self::Unreachable => "unreachable",
            Self::BoardDown => "board_down",
            Self::Lost => "lost",
        }
    }
}

pub fn builder(config: &ClientConfig) -> Builder {
    let transport = QuicTransportConfig::builder()
        .max_concurrent_bidi_streams(VarInt::from_u32(0))
        .max_concurrent_uni_streams(VarInt::from_u32(0))
        .build();
    let builder = match config.relay {
        Relay::None => crate::offline(SocketAddr::from((Ipv4Addr::LOCALHOST, 0))),
        _ => Endpoint::builder(presets::N0)
            .clear_address_lookup()
            .address_lookup(PkarrResolver::n0_dns())
            .address_lookup(DnsAddressLookup::n0_dns())
            .portmapper_config(PortmapperConfig::Disabled),
    };
    builder.transport_config(transport)
}

pub fn target(config: &ClientConfig) -> EndpointAddr {
    let mut addr = EndpointAddr::new(config.host);
    if let Relay::Custom(url) = &config.relay {
        addr = addr.with_relay_url(url.clone());
    }
    if let Some(direct) = config.direct {
        addr = addr.with_ip_addr(direct);
    }
    addr
}

pub async fn run(config: ClientConfig, key: SecretKey) -> Result<(), String> {
    let endpoint = builder(&config)
        .secret_key(key)
        .bind()
        .await
        .map_err(|e| format!("cannot open the endpoint: {e}"))?;
    let listener = TcpListener::bind(config.listen)
        .await
        .map_err(|e| format!("cannot listen on {}: {e}", config.listen))?;
    let stop = crate::stop_signals().map_err(|e| format!("cannot listen for signals: {e}"))?;
    let addr = listener.local_addr().map_err(|e| e.to_string())?;
    println!("listening addr={addr} id={}", endpoint.id());
    let dialer = Arc::new(Dialer::new(endpoint.clone(), target(&config)));
    let failed = tokio::select! {
        error = serve(listener, dialer) => Some(error),
        () = crate::stdin_closed() => None,
        () = stop => None,
    };
    let _ = timeout(CLOSE_WAIT, endpoint.close()).await;
    failed.map_or(Ok(()), |e| Err(format!("the listener failed: {e}")))
}

pub async fn serve(listener: TcpListener, dialer: Arc<Dialer>) -> io::Error {
    loop {
        match listener.accept().await {
            Ok((tcp, _)) => {
                tokio::spawn(handle(tcp, dialer.clone()));
            }
            Err(error) => return error,
        }
    }
}

async fn handle(mut tcp: TcpStream, dialer: Arc<Dialer>) {
    let mut first = vec![0u8; CHUNK];
    let n = match tcp.read(&mut first).await {
        Ok(0) | Err(_) => return,
        Ok(n) => n,
    };
    let Ok(_slot) = dialer.streams.clone().acquire_owned().await else {
        return;
    };
    let Some((send, recv)) = dialer.open_with(&first[..n]).await else {
        let _ = tcp.shutdown().await;
        return;
    };
    let mut relay = pin!(relay(tcp, send, recv, &dialer));
    tokio::select! {
        () = &mut relay => return,
        () = sleep(dialer.reset_after) => dialer.reset_backoff(),
    }
    relay.await;
}

async fn relay(tcp: TcpStream, mut send: SendStream, mut recv: RecvStream, dialer: &Dialer) {
    let (mut tcp_read, mut tcp_write) = tcp.into_split();
    let both = tokio::try_join!(
        upload(&mut tcp_read, &mut send),
        download(&mut recv, &mut tcp_write, dialer)
    );
    if both.is_err() {
        let _ = send.reset(VarInt::from_u32(0));
        let _ = recv.stop(VarInt::from_u32(0));
    }
    let _ = tcp_write.shutdown().await;
}

async fn upload(tcp: &mut OwnedReadHalf, send: &mut SendStream) -> Result<(), ()> {
    let mut buffer = vec![0u8; CHUNK];
    loop {
        match tcp.read(&mut buffer).await {
            Ok(0) => {
                let _ = send.finish();
                return Ok(());
            }
            Ok(n) => match send.write_all(&buffer[..n]).await {
                Ok(()) => {}
                Err(WriteError::Stopped(code)) if code == BOARD_ERROR_CODE => return Ok(()),
                Err(_) => return Err(()),
            },
            Err(_) => return Err(()),
        }
    }
}

async fn download(
    recv: &mut RecvStream,
    tcp: &mut OwnedWriteHalf,
    dialer: &Dialer,
) -> Result<(), ()> {
    let mut buffer = vec![0u8; CHUNK];
    loop {
        match recv.read(&mut buffer).await {
            Ok(Some(n)) => {
                dialer.moved();
                tcp.write_all(&buffer[..n]).await.map_err(|_| ())?;
            }
            Ok(None) => {
                let _ = tcp.shutdown().await;
                return Ok(());
            }
            Err(error) => {
                dialer.read_failed(&error);
                return Err(());
            }
        }
    }
}

pub struct Dialer {
    endpoint: Endpoint,
    target: EndpointAddr,
    deadline: Duration,
    reset_after: Duration,
    connection: AsyncMutex<Option<Connection>>,
    backoff: Mutex<Backoff>,
    retry_at: Mutex<Option<Instant>>,
    state: Mutex<Option<State>>,
    streams: Arc<Semaphore>,
    board_down: AtomicU32,
}

impl Dialer {
    pub fn new(endpoint: Endpoint, target: EndpointAddr) -> Self {
        Self {
            endpoint,
            target,
            deadline: HANDSHAKE_DEADLINE,
            reset_after: BACKOFF_RESET,
            connection: AsyncMutex::new(None),
            backoff: Mutex::new(Backoff::default()),
            retry_at: Mutex::new(None),
            state: Mutex::new(None),
            streams: Arc::new(Semaphore::new(STREAMS_PER_CONNECTION as usize)),
            board_down: AtomicU32::new(0),
        }
    }

    fn set(&self, state: State) {
        let mut held = self.state.lock().unwrap();
        if *held != Some(state) {
            *held = Some(state);
            println!("state {}", state.name());
        }
    }

    fn fail(&self, state: State) {
        self.set(state);
        let delay = self.backoff.lock().unwrap().next_delay();
        *self.retry_at.lock().unwrap() = Some(Instant::now() + delay);
    }

    fn reset_backoff(&self) {
        self.backoff.lock().unwrap().reset();
        *self.retry_at.lock().unwrap() = None;
    }

    fn moved(&self) {
        self.board_down.store(0, Ordering::Relaxed);
        if *self.state.lock().unwrap() == Some(State::BoardDown) {
            self.set(State::Connected);
        }
    }

    fn read_failed(&self, error: &ReadError) {
        match error {
            ReadError::Reset(code) if *code == BOARD_UNREACHABLE_CODE => {
                if self.board_down.fetch_add(1, Ordering::Relaxed) >= 1 {
                    self.set(State::BoardDown);
                }
            }
            ReadError::ConnectionLost(ConnectionError::ApplicationClosed(close))
                if close.error_code == FULL_CODE =>
            {
                self.fail(State::Busy);
            }
            ReadError::ConnectionLost(ConnectionError::ApplicationClosed(close))
                if close.error_code == IDLE_CODE => {}
            ReadError::ConnectionLost(_) => self.set(State::Lost),
            _ => {}
        }
    }

    pub async fn open_with(&self, first: &[u8]) -> Option<(SendStream, RecvStream)> {
        for attempt in 0..2 {
            let connection = self.connect().await?;
            let opened = match connection.open_bi().await {
                Ok((mut send, recv)) => send.write_all(first).await.is_ok().then_some((send, recv)),
                Err(_) => None,
            };
            if closed_with(&connection, FULL_CODE) {
                self.forget(&connection).await;
                self.fail(State::Busy);
                return None;
            }
            if opened.is_some() {
                self.set(State::Connected);
                return opened;
            }
            self.forget(&connection).await;
            if attempt == 1 {
                self.fail(State::Lost);
            }
        }
        None
    }

    async fn connect(&self) -> Option<Connection> {
        let mut held = self.connection.lock().await;
        if let Some(connection) = held.as_ref().filter(|c| c.close_reason().is_none()) {
            return Some(connection.clone());
        }
        *held = None;
        let retry = self.retry_at.lock().unwrap().take();
        if let Some(at) = retry {
            sleep_until(at).await;
        }
        self.set(State::Connecting);
        let dialed = timeout(
            self.deadline,
            self.endpoint.connect(self.target.clone(), ALPN),
        )
        .await;
        match dialed {
            Ok(Ok(connection)) => {
                *held = Some(connection.clone());
                Some(connection)
            }
            Ok(Err(error)) if connect_error(&error).is_some_and(is_refusal) => {
                self.fail(State::Busy);
                None
            }
            Ok(Err(_)) => {
                self.fail(State::Lost);
                None
            }
            Err(_) => {
                self.fail(State::Unreachable);
                None
            }
        }
    }

    async fn forget(&self, connection: &Connection) {
        let mut held = self.connection.lock().await;
        if held
            .as_ref()
            .is_some_and(|c| c.stable_id() == connection.stable_id())
        {
            *held = None;
        }
    }
}

fn closed_with(connection: &Connection, code: VarInt) -> bool {
    matches!(
        connection.close_reason(),
        Some(ConnectionError::ApplicationClosed(close)) if close.error_code == code
    )
}

fn connect_error(error: &ConnectError) -> Option<&ConnectionError> {
    match error {
        ConnectError::Connecting {
            source: ConnectingError::ConnectionError { source, .. },
            ..
        } => Some(source),
        ConnectError::Connection { source, .. } => Some(source),
        _ => None,
    }
}

fn is_refusal(error: &ConnectionError) -> bool {
    match error {
        ConnectionError::ApplicationClosed(close) => close.error_code == FULL_CODE,
        ConnectionError::ConnectionClosed(close) => {
            close.error_code == TransportErrorCode::CONNECTION_REFUSED
        }
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use std::path::PathBuf;

    use iroh::endpoint::ApplicationClose;
    use tokio::net::UdpSocket;
    use tokio::sync::mpsc;
    use tokio::task::JoinHandle;

    use super::*;
    use crate::args::{Config, Secret};
    use crate::forward;

    const SECRET: &str = "0123456789abcdef0123456789abcdef0123456789abcdef";
    const WAIT: Duration = Duration::from_secs(10);

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
                    let mut reader = tokio::io::BufReader::new(reader);
                    let mut line = String::new();
                    tokio::io::AsyncBufReadExt::read_line(&mut reader, &mut line)
                        .await
                        .unwrap();
                    let _ = sent.send(line);
                    let mut chunk = [0u8; 4096];
                    while let Ok(n) = reader.read(&mut chunk).await {
                        if n == 0 || writer.write_all(&chunk[..n]).await.is_err() {
                            break;
                        }
                    }
                    let _ = writer.shutdown().await;
                });
            }
        });
        Board { addr, prefaces }
    }

    async fn closed_port() -> SocketAddr {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        listener.local_addr().unwrap()
    }

    struct Rig {
        board: Board,
        server: Endpoint,
        dialer: Arc<Dialer>,
        listen: SocketAddr,
        client_id: String,
        _tasks: Vec<JoinHandle<()>>,
    }

    fn server_config(board: SocketAddr, max_connections: usize) -> Arc<Config> {
        Arc::new(Config {
            board,
            secret: Secret::parse(SECRET).unwrap(),
            key_dir: PathBuf::new(),
            relay: Relay::None,
            bind: SocketAddr::from((Ipv4Addr::LOCALHOST, 0)),
            max_connections,
            idle: WAIT,
        })
    }

    async fn rig_with(board: Board, max_connections: usize, change: fn(&mut Dialer)) -> Rig {
        let config = server_config(board.addr, max_connections);
        let server = crate::builder(&config)
            .secret_key(SecretKey::from_bytes(&[1; 32]))
            .bind()
            .await
            .unwrap();
        let served = tokio::spawn(forward::serve(
            server.clone(),
            config,
            forward::Limits::default(),
        ));
        let client = ClientConfig {
            host: server.id(),
            key_dir: PathBuf::new(),
            new_key: false,
            listen: SocketAddr::from((Ipv4Addr::LOCALHOST, 0)),
            relay: Relay::None,
            direct: Some(server.bound_sockets()[0]),
        };
        let endpoint = builder(&client)
            .secret_key(SecretKey::from_bytes(&[2; 32]))
            .bind()
            .await
            .unwrap();
        let mut dialer = Dialer::new(endpoint.clone(), target(&client));
        change(&mut dialer);
        let dialer = Arc::new(dialer);
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let listen = listener.local_addr().unwrap();
        let listening = tokio::spawn({
            let dialer = dialer.clone();
            async move {
                serve(listener, dialer).await;
            }
        });
        Rig {
            board,
            server,
            dialer,
            listen,
            client_id: endpoint.id().to_string(),
            _tasks: vec![served, listening],
        }
    }

    async fn rig() -> Rig {
        rig_with(board().await, 4, |_| {}).await
    }

    async fn exchange(listen: SocketAddr, request: &[u8]) -> Vec<u8> {
        let mut tcp = TcpStream::connect(listen).await.unwrap();
        tcp.write_all(request).await.unwrap();
        tcp.shutdown().await.unwrap();
        let mut answer = Vec::new();
        timeout(WAIT, tcp.read_to_end(&mut answer))
            .await
            .expect("no answer in time")
            .unwrap();
        answer
    }

    async fn held(listen: SocketAddr, word: &[u8]) -> TcpStream {
        let mut tcp = TcpStream::connect(listen).await.unwrap();
        tcp.write_all(word).await.unwrap();
        let mut echoed = vec![0u8; word.len()];
        timeout(WAIT, tcp.read_exact(&mut echoed))
            .await
            .expect("no echo in time")
            .unwrap();
        assert_eq!(echoed, word);
        tcp
    }

    fn state(dialer: &Dialer) -> Option<State> {
        *dialer.state.lock().unwrap()
    }

    #[tokio::test]
    async fn a_request_reaches_the_board_with_the_clients_id_and_the_secret() {
        let mut rig = rig().await;
        assert_eq!(exchange(rig.listen, b"ping").await, b"ping");
        let preface = rig.board.prefaces.recv().await.unwrap();
        assert_eq!(preface, format!("HAND-PEER 1 {} {SECRET}\n", rig.client_id));
        assert_eq!(state(&rig.dialer), Some(State::Connected));
    }

    #[tokio::test]
    async fn each_tcp_connection_is_one_stream_and_its_bytes_pass_unchanged() {
        let mut rig = rig().await;
        let bodies: Vec<Vec<u8>> = (0..5u8)
            .map(|n| (0..100_000usize).map(|i| (i % 251) as u8 ^ n).collect())
            .collect();
        let asked: Vec<_> = bodies
            .iter()
            .cloned()
            .map(|body| {
                let listen = rig.listen;
                tokio::spawn(async move {
                    let mut line = b"x".to_vec();
                    line.extend_from_slice(&body);
                    exchange(listen, &line).await
                })
            })
            .collect();
        for (task, body) in asked.into_iter().zip(&bodies) {
            let answer = task.await.unwrap();
            assert_eq!(&answer[1..], &body[..]);
        }
        for _ in 0..5 {
            assert!(
                rig.board
                    .prefaces
                    .recv()
                    .await
                    .unwrap()
                    .starts_with("HAND-PEER 1 ")
            );
        }
        assert!(rig.board.prefaces.try_recv().is_err());
    }

    #[tokio::test]
    async fn a_connection_that_sends_nothing_holds_no_stream_and_no_slot() {
        let mut rig = rig().await;
        let mut silent = Vec::new();
        for _ in 0..40 {
            silent.push(TcpStream::connect(rig.listen).await.unwrap());
        }
        assert_eq!(exchange(rig.listen, b"ping").await, b"ping");
        assert!(rig.board.prefaces.recv().await.is_some());
        assert!(rig.board.prefaces.try_recv().is_err());
        assert_eq!(rig.dialer.streams.available_permits(), 12);
        drop(silent);
    }

    #[tokio::test]
    async fn the_thirteenth_stream_waits_for_a_slot() {
        let mut rig = rig().await;
        let mut open = Vec::new();
        for n in 0..12u8 {
            open.push(held(rig.listen, &[b'a' + n]).await);
        }
        for _ in 0..12 {
            assert!(rig.board.prefaces.recv().await.is_some());
        }
        assert_eq!(rig.dialer.streams.available_permits(), 0);
        let waiting = tokio::spawn({
            let listen = rig.listen;
            async move { exchange(listen, b"late").await }
        });
        for _ in 0..STREAMS_PER_CONNECTION / 4 {
            drop(open.pop());
        }
        assert_eq!(waiting.await.unwrap(), b"late");
        assert!(rig.board.prefaces.recv().await.is_some());
    }

    #[tokio::test]
    async fn a_full_host_is_busy_and_the_next_dial_backs_off() {
        let rig = rig_with(board().await, 0, |_| {}).await;
        assert!(exchange(rig.listen, b"ping").await.is_empty());
        assert_eq!(state(&rig.dialer), Some(State::Busy));
        assert!(rig.dialer.retry_at.lock().unwrap().is_some());
        assert_eq!(rig.dialer.backoff.lock().unwrap().failures(), 1);
    }

    #[tokio::test]
    async fn a_host_that_never_answers_is_unreachable_at_the_deadline() {
        let silent = UdpSocket::bind("127.0.0.1:0").await.unwrap();
        let rig = rig_with(board().await, 4, |_| {}).await;
        let config = ClientConfig {
            host: rig.server.id(),
            key_dir: PathBuf::new(),
            new_key: false,
            listen: SocketAddr::from((Ipv4Addr::LOCALHOST, 0)),
            relay: Relay::None,
            direct: Some(silent.local_addr().unwrap()),
        };
        let endpoint = builder(&config)
            .secret_key(SecretKey::from_bytes(&[3; 32]))
            .bind()
            .await
            .unwrap();
        let mut dialer = Dialer::new(endpoint, target(&config));
        dialer.deadline = Duration::from_millis(300);
        assert!(dialer.open_with(b"ping").await.is_none());
        assert_eq!(state(&dialer), Some(State::Unreachable));
        assert!(dialer.retry_at.lock().unwrap().is_some());
    }

    #[tokio::test]
    async fn a_board_that_is_down_shows_after_two_resets() {
        let rig = rig_with(
            Board {
                addr: closed_port().await,
                prefaces: mpsc::unbounded_channel().1,
            },
            4,
            |_| {},
        )
        .await;
        assert!(exchange(rig.listen, b"one").await.is_empty());
        assert_eq!(state(&rig.dialer), Some(State::Connected));
        assert!(exchange(rig.listen, b"two").await.is_empty());
        assert_eq!(state(&rig.dialer), Some(State::BoardDown));
    }

    #[tokio::test]
    async fn board_down_needs_two_resets_with_no_traffic_between() {
        let rig = rig().await;
        let dialer = &rig.dialer;
        let reset = ReadError::Reset(BOARD_UNREACHABLE_CODE);
        dialer.set(State::Connected);
        dialer.read_failed(&reset);
        dialer.moved();
        dialer.read_failed(&reset);
        assert_eq!(state(dialer), Some(State::Connected));
        dialer.read_failed(&reset);
        assert_eq!(state(dialer), Some(State::BoardDown));
        dialer.moved();
        assert_eq!(state(dialer), Some(State::Connected));
    }

    #[tokio::test]
    async fn read_errors_move_the_state_by_their_code() {
        let rig = rig().await;
        let dialer = &rig.dialer;
        let closed = |code: VarInt| {
            ReadError::ConnectionLost(ConnectionError::ApplicationClosed(ApplicationClose {
                error_code: code,
                reason: Default::default(),
            }))
        };
        dialer.set(State::Connected);
        dialer.read_failed(&closed(IDLE_CODE));
        dialer.read_failed(&ReadError::Reset(BOARD_ERROR_CODE));
        assert_eq!(state(dialer), Some(State::Connected));
        dialer.read_failed(&ReadError::ConnectionLost(ConnectionError::LocallyClosed));
        assert_eq!(state(dialer), Some(State::Lost));
        dialer.read_failed(&closed(FULL_CODE));
        assert_eq!(state(dialer), Some(State::Busy));
        assert!(dialer.retry_at.lock().unwrap().is_some());
    }

    #[tokio::test]
    async fn traffic_for_the_reset_time_starts_the_backoff_again() {
        let rig = rig_with(board().await, 4, |d| {
            d.reset_after = Duration::from_millis(100)
        })
        .await;
        assert_eq!(exchange(rig.listen, b"ping").await, b"ping");
        for _ in 0..3 {
            rig.dialer.fail(State::Lost);
        }
        assert_eq!(rig.dialer.backoff.lock().unwrap().failures(), 3);
        let _open = held(rig.listen, b"x").await;
        let deadline = std::time::Instant::now() + WAIT;
        while rig.dialer.backoff.lock().unwrap().failures() != 0 {
            assert!(
                std::time::Instant::now() < deadline,
                "the backoff never reset"
            );
            tokio::task::yield_now().await;
        }
        assert!(rig.dialer.retry_at.lock().unwrap().is_none());
    }

    #[test]
    fn the_states_are_the_documented_words() {
        let words: Vec<_> = [
            State::Connecting,
            State::Connected,
            State::Busy,
            State::Unreachable,
            State::BoardDown,
            State::Lost,
        ]
        .map(State::name)
        .into();
        assert_eq!(
            words,
            [
                "connecting",
                "connected",
                "busy",
                "unreachable",
                "board_down",
                "lost"
            ]
        );
    }

    #[test]
    fn the_deadlines_are_the_documented_ones() {
        assert_eq!(HANDSHAKE_DEADLINE, Duration::from_secs(20));
        assert_eq!(BACKOFF_RESET, Duration::from_secs(10));
        assert_eq!(STREAMS_PER_CONNECTION, 12);
    }
}
