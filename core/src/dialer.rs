use std::str::FromStr;
use std::sync::atomic::{AtomicBool, AtomicU32, AtomicU64, Ordering};
use std::sync::{Arc, Mutex as StdMutex};
use std::time::Duration;

use iroh::endpoint::{
    ConnectError, ConnectingError, Connection, ConnectionError, ReadError, VarInt, WriteError,
};
use iroh::{Endpoint, EndpointAddr, EndpointId, RelayUrl};
use tokio::sync::{Mutex, OwnedSemaphorePermit, RwLock, Semaphore};
use tokio::time::{Instant, sleep, timeout};

use crate::backoff::Backoff;
use crate::codes;

pub const ALPN: &[u8] = b"hand-board";
pub const STREAMS_PER_CONNECTION: usize = 12;
pub const HANDSHAKE_DEADLINE: Duration = Duration::from_secs(20);
pub const BACKOFF_RESET: Duration = Duration::from_secs(10);
const LOST_TRIES: u32 = 3;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Notice {
    None,
    Connected,
    NoTarget,
    Busy,
    Idle,
    BoardUnreachable,
    StreamIdle,
    BoardError,
    Lost,
    Unreachable,
}

impl Notice {
    pub fn message(self) -> &'static str {
        match self {
            Self::None => "",
            Self::Connected => "Connected.",
            Self::NoTarget => "No laptop selected.",
            Self::Busy => "Your laptop is busy. Trying again.",
            Self::Idle => "",
            Self::BoardUnreachable => {
                "Your laptop answers, but the Board is not running. Start it on the laptop."
            }
            Self::StreamIdle => "",
            Self::BoardError => "",
            Self::Lost => {
                "Your laptop closed the connection. If you removed this phone there, pair it again."
            }
            Self::Unreachable => {
                "Can't reach your laptop. Check that it is on, that Hand's Board is running, and that this phone has internet."
            }
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum OpenError {
    NotConfigured,
    Unreachable,
    Lost,
}

pub fn parse_target(endpoint_id: &str, relay: Option<&str>) -> Result<EndpointAddr, String> {
    if endpoint_id.len() != 64
        || !endpoint_id
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    {
        return Err("the endpoint ID must be 64 lowercase hexadecimal characters".to_owned());
    }
    let id = EndpointId::from_str(endpoint_id)
        .map_err(|_| "the endpoint ID is not a valid iroh key".to_owned())?;
    let mut addr = EndpointAddr::new(id);
    if let Some(relay) = relay.map(str::trim).filter(|relay| !relay.is_empty()) {
        let relay =
            RelayUrl::from_str(relay).map_err(|_| "the relay URL is not valid".to_owned())?;
        addr = addr.with_relay_url(relay);
    }
    Ok(addr)
}

#[derive(Clone)]
pub struct Dialer {
    inner: Arc<Inner>,
}

struct Inner {
    endpoint: Endpoint,
    target: RwLock<Option<EndpointAddr>>,
    state: Mutex<State>,
    dial: Mutex<()>,
    backoff: StdMutex<Backoff>,
    retry_at: StdMutex<Option<Instant>>,
    notice: StdMutex<Notice>,
    streams: Arc<Semaphore>,
    generation: AtomicU64,
    lost: AtomicU32,
    board_unreachable: AtomicU32,
}

struct State {
    connection: Option<Active>,
}

struct Active {
    connection: Connection,
    generation: u64,
}

impl Dialer {
    pub fn new(endpoint: Endpoint, target: Option<EndpointAddr>) -> Self {
        Self {
            inner: Arc::new(Inner {
                endpoint,
                target: RwLock::new(target),
                state: Mutex::new(State { connection: None }),
                dial: Mutex::new(()),
                backoff: StdMutex::new(Backoff::default()),
                retry_at: StdMutex::new(None),
                notice: StdMutex::new(Notice::None),
                streams: Arc::new(Semaphore::new(STREAMS_PER_CONNECTION)),
                generation: AtomicU64::new(0),
                lost: AtomicU32::new(0),
                board_unreachable: AtomicU32::new(0),
            }),
        }
    }

    pub fn endpoint(&self) -> &Endpoint {
        &self.inner.endpoint
    }

    pub fn endpoint_id(&self) -> EndpointId {
        self.inner.endpoint.id()
    }

    pub fn notice(&self) -> Notice {
        *self.inner.notice.lock().unwrap()
    }

    pub fn set_notice(&self, notice: Notice) {
        *self.inner.notice.lock().unwrap() = notice;
    }

    pub async fn target(&self) -> Option<EndpointAddr> {
        self.inner.target.read().await.clone()
    }

    pub async fn set_target(&self, target: Option<EndpointAddr>) {
        {
            let mut current = self.inner.target.write().await;
            if *current == target {
                return;
            }
            *current = target;
            let mut state = self.inner.state.lock().await;
            if let Some(active) = state.connection.take() {
                active
                    .connection
                    .close(VarInt::from_u32(0), b"target changed");
            }
            self.inner.lost.store(0, Ordering::Relaxed);
            self.inner.board_unreachable.store(0, Ordering::Relaxed);
            *self.inner.backoff.lock().unwrap() = Backoff::default();
            *self.inner.retry_at.lock().unwrap() = None;
            *self.inner.notice.lock().unwrap() = Notice::None;
        }
    }

    pub async fn connection(&self) -> Result<Connection, OpenError> {
        self.connect().await
    }

    pub async fn disconnect(&self) {
        let mut state = self.inner.state.lock().await;
        if let Some(active) = state.connection.take() {
            active.connection.close(VarInt::from_u32(0), b"resume");
        }
    }

    pub async fn open_with(&self, first: &[u8]) -> Result<(StreamSend, StreamRecv), OpenError> {
        let permit = self
            .inner
            .streams
            .clone()
            .acquire_owned()
            .await
            .map_err(|_| OpenError::Lost)?;
        loop {
            let connection = self.connect().await?;
            let (mut send, recv) = match connection.open_bi().await {
                Ok(parts) => parts,
                Err(error) => {
                    self.inner.clear(&connection).await;
                    if codes::connection_code(&error) == Some(codes::FULL) {
                        self.inner.busy_wait().await;
                        continue;
                    }
                    return Err(OpenError::Lost);
                }
            };
            match send.write_all(first).await {
                Ok(()) => {
                    if connection
                        .close_reason()
                        .as_ref()
                        .and_then(codes::connection_code)
                        == Some(codes::FULL)
                    {
                        self.inner.busy_wait().await;
                        continue;
                    }
                    self.inner.lost.store(0, Ordering::Relaxed);
                    self.set_notice(Notice::Connected);
                    return Ok(self.inner.wrap(send, recv, permit));
                }
                Err(error) => {
                    let code = codes::write_code(&error);
                    self.inner.note_code(code);
                    if code == Some(codes::FULL)
                        || connection
                            .close_reason()
                            .as_ref()
                            .and_then(codes::connection_code)
                            == Some(codes::FULL)
                    {
                        self.inner.busy_wait().await;
                        continue;
                    }
                    return Err(OpenError::Lost);
                }
            }
        }
    }

    async fn connect(&self) -> Result<Connection, OpenError> {
        if let Some(connection) = self.live().await {
            return Ok(connection);
        }
        let _dial = self.inner.dial.lock().await;
        if let Some(connection) = self.live().await {
            return Ok(connection);
        }
        loop {
            let Some(target) = self.target().await else {
                self.set_notice(Notice::NoTarget);
                return Err(OpenError::NotConfigured);
            };
            self.inner.wait_retry().await;
            let attempted = timeout(
                HANDSHAKE_DEADLINE,
                self.inner.endpoint.connect(target, ALPN),
            )
            .await;
            match attempted {
                Ok(Ok(connection)) => {
                    let generation = self.inner.generation.fetch_add(1, Ordering::Relaxed) + 1;
                    {
                        let mut state = self.inner.state.lock().await;
                        state.connection = Some(Active {
                            connection: connection.clone(),
                            generation,
                        });
                    }
                    self.inner.lost.store(0, Ordering::Relaxed);
                    *self.inner.retry_at.lock().unwrap() = None;
                    self.inner.watch(connection.clone(), generation);
                    self.set_notice(Notice::Connected);
                    return Ok(connection);
                }
                Ok(Err(error)) => {
                    if connect_error(&error).is_some_and(codes::is_refusal) {
                        self.inner.busy_wait().await;
                        continue;
                    }
                    let lost = self.inner.lost.fetch_add(1, Ordering::Relaxed) + 1;
                    if lost >= LOST_TRIES {
                        self.set_notice(Notice::Lost);
                        self.inner.lost.store(0, Ordering::Relaxed);
                        return Err(OpenError::Lost);
                    }
                    self.set_notice(Notice::Lost);
                    self.inner.schedule_retry();
                    self.inner.wait_retry().await;
                }
                Err(_) => {
                    self.set_notice(Notice::Unreachable);
                    return Err(OpenError::Unreachable);
                }
            }
        }
    }

    async fn live(&self) -> Option<Connection> {
        let mut state = self.inner.state.lock().await;
        match &state.connection {
            Some(active) if active.connection.close_reason().is_none() => {
                Some(active.connection.clone())
            }
            Some(_) => {
                state.connection = None;
                None
            }
            None => None,
        }
    }
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

impl Inner {
    fn next_delay(&self) -> Duration {
        self.backoff.lock().unwrap().next_delay()
    }

    async fn busy_wait(&self) {
        *self.notice.lock().unwrap() = Notice::Busy;
        self.schedule_retry();
        self.wait_retry().await;
    }

    fn schedule_retry(&self) {
        let delay = self.next_delay();
        *self.retry_at.lock().unwrap() = Some(Instant::now() + delay);
    }

    async fn wait_retry(&self) {
        let at = *self.retry_at.lock().unwrap();
        if let Some(at) = at {
            let now = Instant::now();
            if at > now {
                sleep(at - now).await;
            }
            *self.retry_at.lock().unwrap() = None;
        }
    }

    async fn clear(&self, connection: &Connection) {
        let mut state = self.state.lock().await;
        if state
            .connection
            .as_ref()
            .is_some_and(|active| active.connection.stable_id() == connection.stable_id())
        {
            state.connection = None;
        }
    }

    fn note_code(&self, code: Option<u64>) {
        match code {
            Some(codes::BOARD_UNREACHABLE) => {
                let seen = self.board_unreachable.fetch_add(1, Ordering::Relaxed) + 1;
                *self.notice.lock().unwrap() = if seen >= 2 {
                    Notice::BoardUnreachable
                } else {
                    Notice::BoardError
                };
            }
            Some(codes::FULL) => {
                *self.notice.lock().unwrap() = Notice::Busy;
                self.schedule_retry();
            }
            Some(codes::STREAM_IDLE) => {
                *self.notice.lock().unwrap() = Notice::StreamIdle;
            }
            Some(codes::BOARD_ERROR) => {
                *self.notice.lock().unwrap() = Notice::BoardError;
            }
            Some(_) | None => {
                *self.notice.lock().unwrap() = Notice::Lost;
            }
        }
    }

    fn watch(self: &Arc<Self>, connection: Connection, generation: u64) {
        let inner = self.clone();
        tokio::spawn(async move {
            let error = connection.closed().await;
            let code = codes::connection_code(&error);
            {
                let mut state = inner.state.lock().await;
                if state
                    .connection
                    .as_ref()
                    .is_some_and(|active| active.generation == generation)
                {
                    state.connection = None;
                }
            }
            match code {
                Some(codes::IDLE) => {
                    *inner.retry_at.lock().unwrap() = None;
                    *inner.notice.lock().unwrap() = Notice::Idle;
                }
                Some(codes::FULL) => {
                    *inner.notice.lock().unwrap() = Notice::Busy;
                    inner.schedule_retry();
                }
                _ => {}
            }
        });
    }

    fn wrap(
        self: &Arc<Self>,
        send: iroh::endpoint::SendStream,
        recv: iroh::endpoint::RecvStream,
        permit: OwnedSemaphorePermit,
    ) -> (StreamSend, StreamRecv) {
        let life = Arc::new(AtomicBool::new(true));
        let guard = Arc::new(Guard {
            _permit: permit,
            life: life.clone(),
            inner: self.clone(),
        });
        let inner = self.clone();
        tokio::spawn(async move {
            sleep(BACKOFF_RESET).await;
            if life.load(Ordering::Relaxed) {
                inner.backoff.lock().unwrap().reset();
                *inner.retry_at.lock().unwrap() = None;
            }
        });
        (
            StreamSend {
                send,
                guard: guard.clone(),
            },
            StreamRecv { recv, guard },
        )
    }
}

struct Guard {
    _permit: OwnedSemaphorePermit,
    life: Arc<AtomicBool>,
    inner: Arc<Inner>,
}

impl Drop for Guard {
    fn drop(&mut self) {
        self.life.store(false, Ordering::Relaxed);
    }
}

pub struct StreamSend {
    send: iroh::endpoint::SendStream,
    guard: Arc<Guard>,
}

impl StreamSend {
    pub async fn write_all(&mut self, data: &[u8]) -> Result<(), WriteError> {
        match self.send.write_all(data).await {
            Ok(()) => {
                self.guard
                    .inner
                    .board_unreachable
                    .store(0, Ordering::Relaxed);
                Ok(())
            }
            Err(error) => {
                self.guard.inner.note_code(codes::write_code(&error));
                Err(error)
            }
        }
    }

    pub fn finish(&mut self) -> Result<(), iroh::endpoint::ClosedStream> {
        self.send.finish()
    }

    pub fn reset(&mut self) {
        let _ = self.send.reset(VarInt::from_u32(0));
    }
}

pub struct StreamRecv {
    recv: iroh::endpoint::RecvStream,
    guard: Arc<Guard>,
}

impl StreamRecv {
    pub async fn read(&mut self, buf: &mut [u8]) -> Result<usize, ReadError> {
        match self.recv.read(buf).await {
            Ok(Some(n)) => {
                self.guard
                    .inner
                    .board_unreachable
                    .store(0, Ordering::Relaxed);
                Ok(n)
            }
            Ok(None) => Ok(0),
            Err(error) => {
                self.guard.inner.note_code(codes::read_code(&error));
                Err(error)
            }
        }
    }

    pub fn stop(&mut self) {
        let _ = self.recv.stop(VarInt::from_u32(0));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_target_needs_a_lowercase_hex_endpoint_id_and_an_optional_relay() {
        let id = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6";
        assert!(parse_target(id, None).is_ok());
        assert!(parse_target(id, Some("https://relay.example.test:7842/")).is_ok());
        for bad in [
            "",
            "4D92ADF6FD9076DDD383C22E08F6AC6FB0CE48210B69C0F954E7E199B4D96ED6",
            "gd92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6",
            "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed",
        ] {
            assert!(parse_target(bad, None).is_err(), "{bad}");
        }
        assert!(parse_target(id, Some("not a relay")).is_err());
    }
}
