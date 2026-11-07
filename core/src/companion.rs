use std::fmt;
use std::io;
use std::net::Ipv4Addr;
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::Mutex as StdMutex;
use std::time::Duration;

use iroh::endpoint::{Endpoint, QuicTransportConfig, VarInt, presets};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tokio::sync::mpsc::{UnboundedReceiver, unbounded_channel};
use tokio::time::timeout;
use url::Url;

use crate::dialer::{ALPN, Dialer, OpenError, STREAMS_PER_CONNECTION, parse_target};
use crate::key::{self, KeyCipher};
use crate::link::LINK_REJECTED;
use crate::pairing::{Offer, OfferError, Pairing};
use crate::proxy::{self, Proxy, Signal};

const PROBE_TIMEOUT: Duration = Duration::from_secs(10);

pub struct CompanionConfig {
    pub data_dir: PathBuf,
    pub cipher: Arc<dyn KeyCipher>,
}

pub(crate) fn proxy_url(port: u16, path: &str) -> String {
    let path = if path.starts_with('/') {
        path.to_owned()
    } else {
        format!("/{path}")
    };
    format!("http://localhost:{port}{path}")
}

pub fn proxy_origin(port: u16) -> Url {
    Url::parse(&proxy_url(port, "/")).expect("the proxy URL parses")
}

#[derive(Clone)]
pub struct Companion {
    dialer: Dialer,
    port: u16,
    pairing: std::sync::Arc<StdMutex<Pairing>>,
    signals: std::sync::Arc<StdMutex<Option<UnboundedReceiver<Signal>>>>,
}

impl Companion {
    pub async fn start(config: CompanionConfig) -> Result<Self, StartError> {
        std::fs::create_dir_all(&config.data_dir)?;
        let secret_key = key::load_or_create(&config.data_dir, config.cipher.as_ref())?;
        let transport = QuicTransportConfig::builder()
            .max_concurrent_bidi_streams(VarInt::from_u32(STREAMS_PER_CONNECTION as u32))
            .max_concurrent_uni_streams(VarInt::from_u32(0))
            .build();
        let endpoint = Endpoint::builder(presets::N0)
            .secret_key(secret_key)
            .alpns(vec![ALPN.to_vec()])
            .transport_config(transport)
            .bind()
            .await
            .map_err(|error| StartError::Bind(error.to_string()))?;
        let (proxy, listeners) = Proxy::bind(&config.data_dir).await?;
        let dialer = Dialer::new(endpoint, None);
        let (signals, receiver) = unbounded_channel();
        for listener in listeners {
            tokio::spawn(proxy::serve(
                listener,
                dialer.clone(),
                Some(signals.clone()),
            ));
        }
        Ok(Self {
            dialer,
            port: proxy.port(),
            pairing: std::sync::Arc::new(StdMutex::new(Pairing::new(&config.data_dir))),
            signals: std::sync::Arc::new(StdMutex::new(Some(receiver))),
        })
    }

    pub fn port(&self) -> u16 {
        self.port
    }

    pub fn offer_link(&self, text: &str) -> Result<Offer, OfferError> {
        self.pairing.lock().unwrap().offer(text.trim())
    }

    pub fn url(&self, path: &str) -> String {
        proxy_url(self.port, path)
    }

    pub fn pending_offer(&self) -> Option<Offer> {
        self.pairing.lock().unwrap().pending_offer()
    }

    pub async fn open_board(&self) -> Result<Option<String>, ConfirmError> {
        let active = {
            let pairing = self.pairing.lock().unwrap();
            pairing.active().map(str::to_owned)
        };
        let Some(active) = active else {
            return Ok(None);
        };
        let target = parse_target(&active, None).map_err(ConfirmError::Target)?;
        self.dialer.set_target(Some(target)).await;
        Ok(Some(proxy_url(self.port, "/")))
    }

    pub fn cancel_pairing(&self) {
        self.pairing.lock().unwrap().clear_pending();
    }

    pub fn take_signals(&self) -> Option<UnboundedReceiver<Signal>> {
        self.signals.lock().unwrap().take()
    }

    pub async fn forget_laptop(&self) -> io::Result<()> {
        self.dialer.set_target(None).await;
        self.pairing.lock().unwrap().forget()
    }

    pub async fn confirm(
        &self,
        clear: impl FnOnce() -> io::Result<()>,
    ) -> Result<String, ConfirmError> {
        let link = {
            let pairing = self.pairing.lock().unwrap();
            pairing.pending().cloned()
        };
        let link = link.ok_or(ConfirmError::NoPending)?;
        let switched = self
            .pairing
            .lock()
            .unwrap()
            .clear_before_forgetting(clear)
            .map_err(ConfirmError::Io)?;
        if switched {
            self.dialer.set_target(None).await;
        }
        self.pairing
            .lock()
            .unwrap()
            .activate()
            .map_err(ConfirmError::Io)?;
        let target = link.target().map_err(ConfirmError::Target)?;
        self.dialer.set_target(Some(target)).await;
        self.dialer.attempt().await?;
        self.probe_pair_page(&link.fleet).await?;
        self.pairing.lock().unwrap().clear_pending();
        Ok(link.open_url(self.port))
    }

    async fn probe_pair_page(&self, fleet: &str) -> Result<(), ConfirmError> {
        let mut stream = TcpStream::connect((Ipv4Addr::LOCALHOST, self.port))
            .await
            .map_err(|_| ConfirmError::BoardDown)?;
        let request =
            format!("GET /{fleet}/pair HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n");
        let probe = async {
            stream.write_all(request.as_bytes()).await?;
            stream.shutdown().await?;
            let mut head = [0u8; 5];
            stream.read_exact(&mut head).await?;
            Ok::<_, io::Error>(())
        };
        match timeout(PROBE_TIMEOUT, probe).await {
            Ok(Ok(())) => Ok(()),
            _ => Err(ConfirmError::BoardDown),
        }
    }

    pub async fn reconnect_on_resume(&self) {
        self.dialer.disconnect().await;
    }
}

#[derive(Debug)]
pub enum ConfirmError {
    NoPending,
    Target(String),
    Io(io::Error),
    Open(OpenError),
    BoardDown,
}

impl ConfirmError {
    pub fn message(&self) -> &'static str {
        match self {
            Self::Open(OpenError::Busy) => crate::dialer::Notice::Busy.message(),
            Self::Open(OpenError::Unreachable) => crate::dialer::Notice::Unreachable.message(),
            Self::Open(OpenError::Lost) => crate::dialer::Notice::Lost.message(),
            Self::Open(OpenError::NotConfigured) => crate::dialer::Notice::NoTarget.message(),
            Self::BoardDown => crate::dialer::Notice::BoardUnreachable.message(),
            Self::NoPending | Self::Target(_) | Self::Io(_) => LINK_REJECTED,
        }
    }

    pub fn retry(&self) -> bool {
        matches!(self, Self::Open(OpenError::Busy | OpenError::Lost))
    }
}

impl fmt::Display for ConfirmError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Target(error) => f.write_str(error),
            Self::Io(error) => write!(f, "{error}"),
            other => f.write_str(other.message()),
        }
    }
}

impl std::error::Error for ConfirmError {}

impl From<OpenError> for ConfirmError {
    fn from(error: OpenError) -> Self {
        Self::Open(error)
    }
}

#[derive(Debug)]
pub enum StartError {
    Io(io::Error),
    Bind(String),
}

impl fmt::Display for StartError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Io(error) => write!(f, "{error}"),
            Self::Bind(error) => write!(f, "cannot open the iroh endpoint: {error}"),
        }
    }
}

impl std::error::Error for StartError {}

impl From<io::Error> for StartError {
    fn from(error: io::Error) -> Self {
        Self::Io(error)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::key::CipherError;
    use crate::testutil::TempDir;

    const LINK: &str = "handboard://c#v=1&e=4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6&f=f0123456789ab&c=K7QM3XRD2P";
    const SECOND_LINK: &str = "handboard://c#v=1&e=d6e458a66697094c1bc7b54cb83fea66a8db57c49f37514944579712cd7e43c5&f=fa1b2c3d4e5f6&c=0123456789";

    struct TestCipher;

    impl KeyCipher for TestCipher {
        fn probe(&self) -> Result<(), CipherError> {
            Ok(())
        }

        fn seal(&self, plaintext: &[u8]) -> Result<Vec<u8>, CipherError> {
            Ok(plaintext.to_vec())
        }

        fn open(&self, sealed: &[u8]) -> Result<Vec<u8>, CipherError> {
            Ok(sealed.to_vec())
        }
    }

    fn config(dir: &TempDir) -> CompanionConfig {
        CompanionConfig {
            data_dir: dir.path().to_owned(),
            cipher: Arc::new(TestCipher),
        }
    }

    #[tokio::test]
    async fn offering_a_link_dials_nothing_until_confirmed() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let offer = companion.offer_link(LINK).unwrap();
        assert_eq!(offer.laptop, "4d92…6ed6");
        assert!(companion.dialer.target().await.is_none());
    }

    #[tokio::test]
    async fn a_second_link_keeps_the_pending_confirmation() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let first = companion.offer_link(LINK).unwrap();
        assert_eq!(
            companion.offer_link(SECOND_LINK).unwrap_err(),
            OfferError::Pending
        );
        assert_eq!(companion.pending_offer().unwrap().laptop, first.laptop);
    }

    #[tokio::test]
    async fn a_relaunch_without_a_laptop_stays_on_the_pairing_screen() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        assert_eq!(companion.open_board().await.unwrap(), None);
        assert!(companion.dialer.target().await.is_none());
    }

    #[tokio::test]
    async fn a_relaunch_with_a_saved_laptop_opens_the_board() {
        let dir = TempDir::new();
        let laptop = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6";
        std::fs::write(dir.path().join("laptop"), format!("{laptop}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        assert_eq!(
            companion.open_board().await.unwrap(),
            Some(format!("http://localhost:{}/", companion.port()))
        );
        assert_eq!(
            companion
                .dialer
                .target()
                .await
                .map(|target| target.id.to_string()),
            Some(laptop.to_owned())
        );
    }
}
