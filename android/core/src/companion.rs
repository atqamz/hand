use std::fmt;
use std::future::Future;
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
use crate::http::{Request, RequestError, Response, exchange};
use crate::key::{self, KeyCipher};
use crate::link::short_id;
use crate::link::{LINK_REJECTED, Link};
use crate::open::{NOT_PAIRED, OPEN_REJECTED, parse_open};
use crate::pair::{PairError, redeem};
use crate::pairing::{Offer, OfferError, PAIRING_PENDING, Pairing};
use crate::proxy::{self, Proxy, Signal};
use crate::sessions::{Jar, Paired, SaveError, Sessions};

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
    store: std::sync::Arc<StdMutex<()>>,
    signals: std::sync::Arc<StdMutex<Option<UnboundedReceiver<Signal>>>>,
    #[cfg(test)]
    gate: std::sync::Arc<StdMutex<Option<std::sync::Arc<Gate>>>>,
}

#[cfg(test)]
struct Gate {
    reached: tokio::sync::Notify,
    resume: tokio::sync::Notify,
}

impl Companion {
    pub fn dialer(&self) -> Dialer {
        self.dialer.clone()
    }

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
            store: std::sync::Arc::new(StdMutex::new(())),
            signals: std::sync::Arc::new(StdMutex::new(Some(receiver))),
            #[cfg(test)]
            gate: std::sync::Arc::new(StdMutex::new(None)),
        })
    }

    pub async fn request(&self, request: &Request) -> Result<Response, RequestError> {
        exchange(&self.dialer, request).await
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
            pairing.active().map(|active| {
                (
                    active.to_owned(),
                    pairing.active_relay().map(str::to_owned),
                    pairing.generation(),
                )
            })
        };
        let Some((active, relay, generation)) = active else {
            return Ok(None);
        };
        let target = parse_target(&active, relay.as_deref()).map_err(ConfirmError::Target)?;
        #[cfg(test)]
        self.pass_gate().await;
        self.dialer.set_target(Some(target)).await;
        if self.pairing.lock().unwrap().generation() != generation {
            self.dialer.set_target(None).await;
            return Ok(None);
        }
        self.dialer.attempt().await?;
        Ok(Some(proxy_url(self.port, "/")))
    }

    pub fn cant_reach(&self) -> String {
        match self.pairing.lock().unwrap().active() {
            Some(active) => format!("Can't reach {}.", short_id(active)),
            None => "Can't reach your laptop.".to_owned(),
        }
    }

    pub fn cancel_pairing(&self) {
        self.pairing.lock().unwrap().clear_pending();
    }

    pub fn take_signals(&self) -> Option<UnboundedReceiver<Signal>> {
        self.signals.lock().unwrap().take()
    }

    pub async fn forget_laptop(&self, clear: impl FnOnce() -> io::Result<()>) -> io::Result<()> {
        let forgotten = {
            let _store = self.store.lock().unwrap();
            clear()?;
            self.pairing.lock().unwrap().forget()
        };
        self.dialer.set_target(None).await;
        forgotten
    }

    #[cfg(test)]
    fn arm_gate(&self, gate: std::sync::Arc<Gate>) {
        *self.gate.lock().unwrap() = Some(gate);
    }

    #[cfg(test)]
    async fn pass_gate(&self) {
        let gate = self.gate.lock().unwrap().take();
        if let Some(gate) = gate {
            gate.reached.notify_one();
            gate.resume.notified().await;
        }
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
            .activate(&link)
            .map_err(ConfirmError::Io)?;
        let target = link.target().map_err(ConfirmError::Target)?;
        self.dialer.set_target(Some(target)).await;
        self.dialer.attempt().await?;
        self.probe_pair_page(&link.fleet).await?;
        self.pairing.lock().unwrap().clear_pending();
        Ok(link.open_url(self.port))
    }

    pub async fn pair<J: Jar>(
        &self,
        sessions: &Sessions<J>,
        name: &str,
        replacing: impl FnOnce(),
    ) -> Result<Vec<Paired>, PairError> {
        sessions.check().map_err(|_| PairError::Jar)?;
        let (link, generation) = {
            let mut pairing = self.pairing.lock().unwrap();
            let generation = pairing.generation();
            (pairing.take(), generation)
        };
        let link = link.ok_or(PairError::NoPending)?;
        let target = link.target().map_err(|_| PairError::NoPending)?;
        let dialer = Dialer::new(self.dialer.endpoint().clone(), Some(target));
        let redeemed = redeem(&dialer, &link, name).await;
        dialer.disconnect().await;
        match redeemed {
            Ok(paired) => {
                self.settle(sessions, &link, generation, paired, replacing)
                    .await
            }
            Err(error) => {
                self.pairing.lock().unwrap().restore(link);
                Err(error)
            }
        }
    }

    async fn settle<J: Jar>(
        &self,
        sessions: &Sessions<J>,
        link: &Link,
        generation: u64,
        paired: Vec<Paired>,
        replacing: impl FnOnce(),
    ) -> Result<Vec<Paired>, PairError> {
        #[cfg(test)]
        self.pass_gate().await;
        let target = link.target().map_err(|_| PairError::NoPending)?;
        {
            let _store = self.store.lock().unwrap();
            let mut pairing = self.pairing.lock().unwrap();
            if pairing.generation() != generation {
                return Err(PairError::NoPending);
            }
            sessions
                .pair(&link.endpoint_id, &paired, replacing)
                .map_err(|error| match error {
                    SaveError::Jar | SaveError::Store => PairError::NotSaved,
                })?;
            if pairing.activate(link).is_err() {
                sessions.mark_unsaved();
            }
        }
        self.dialer.set_target(Some(target)).await;
        if self.pairing.lock().unwrap().generation() != generation {
            self.dialer.set_target(None).await;
            return Err(PairError::NoPending);
        }
        Ok(paired)
    }

    pub async fn open_link(&self, text: &str) -> Result<String, OpenLinkError> {
        self.open_link_after(text, |fleet| async move {
            self.dialer.attempt().await.map_err(ConfirmError::Open)?;
            self.probe_pair_page(&fleet).await
        })
        .await
    }

    async fn open_link_after<Fut>(
        &self,
        text: &str,
        reach: impl FnOnce(String) -> Fut,
    ) -> Result<String, OpenLinkError>
    where
        Fut: Future<Output = Result<(), ConfirmError>>,
    {
        let active = {
            let pairing = self.pairing.lock().unwrap();
            if pairing.pending().is_some() {
                return Err(OpenLinkError::Pending);
            }
            pairing.active().map(|active| {
                (
                    active.to_owned(),
                    pairing.active_relay().map(str::to_owned),
                    pairing.generation(),
                )
            })
        };
        let link = parse_open(text).ok_or(OpenLinkError::Rejected)?;
        let (active, relay, generation) = active.ok_or(OpenLinkError::NotPaired)?;
        let target = parse_target(&active, relay.as_deref()).map_err(ConfirmError::Target)?;
        #[cfg(test)]
        self.pass_gate().await;
        self.dialer.set_target(Some(target)).await;
        if self.pairing.lock().unwrap().generation() != generation {
            self.dialer.set_target(None).await;
            return Err(OpenLinkError::NotPaired);
        }
        reach(link.fleet.clone()).await?;
        Ok(proxy_url(self.port, &link.path()))
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
pub enum OpenLinkError {
    Pending,
    Rejected,
    NotPaired,
    Failed(ConfirmError),
}

impl OpenLinkError {
    pub fn message(&self) -> &'static str {
        match self {
            Self::Pending => PAIRING_PENDING,
            Self::Rejected => OPEN_REJECTED,
            Self::NotPaired => NOT_PAIRED,
            Self::Failed(ConfirmError::Open(OpenError::Busy)) => {
                "Your laptop is busy. Tap the link again."
            }
            Self::Failed(error) => error.message(),
        }
    }
}

impl fmt::Display for OpenLinkError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.message())
    }
}

impl std::error::Error for OpenLinkError {}

impl From<ConfirmError> for OpenLinkError {
    fn from(error: ConfirmError) -> Self {
        Self::Failed(error)
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
    use crate::http::Method;
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
    async fn a_request_with_no_laptop_paired_says_so() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let request = Request::new(
            Method::Get,
            "f0123456789ab",
            "/api/v1/status",
            Some("hand_phone_f0123456789ab=abc"),
            None,
            b"",
        )
        .unwrap();
        assert_eq!(
            companion.request(&request).await,
            Err(RequestError::NoLaptop)
        );
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
    async fn a_link_with_surrounding_whitespace_is_accepted() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let padded = format!("\n\t {LINK} \r\n");
        let offer = companion.offer_link(&padded).unwrap();
        assert_eq!(offer.laptop, "4d92…6ed6");
        assert_eq!(companion.pending_offer().unwrap().laptop, offer.laptop);
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
    async fn a_relaunch_with_an_unreachable_laptop_fails_and_keeps_the_laptop() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let error = companion.open_board().await.unwrap_err();
        assert!(matches!(error, ConfirmError::Open(_)), "{error:?}");
        assert_eq!(
            companion
                .dialer
                .target()
                .await
                .map(|target| target.id.to_string()),
            Some(LAPTOP.to_owned())
        );
        assert_eq!(
            std::fs::read_to_string(dir.path().join("laptop")).unwrap(),
            format!("{LAPTOP}\n")
        );
    }

    fn take(companion: &Companion) -> (Link, u64) {
        let mut pairing = companion.pairing.lock().unwrap();
        let generation = pairing.generation();
        (pairing.take().unwrap(), generation)
    }

    struct HeldJar(StdMutex<Option<&'static str>>);

    fn held(cookie: Option<&'static str>) -> HeldJar {
        HeldJar(StdMutex::new(cookie))
    }

    impl Jar for HeldJar {
        fn cookies(&self) -> io::Result<Option<String>> {
            Ok(self.0.lock().unwrap().map(str::to_owned))
        }

        fn clear(&self) -> io::Result<()> {
            *self.0.lock().unwrap() = None;
            Ok(())
        }
    }

    #[tokio::test]
    async fn pairing_without_a_pending_link_says_so() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = Sessions::open(dir.path(), held(None), Arc::new(TestCipher)).unwrap();
        let result = companion.pair(&sessions, "Pixel", || {}).await;
        assert_eq!(result.err(), Some(PairError::NoPending));
    }

    #[tokio::test]
    async fn a_pairing_that_cannot_reach_the_laptop_changes_nothing_and_stays_pending() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = Sessions::open(dir.path(), held(None), Arc::new(TestCipher)).unwrap();
        companion.open_board().await.unwrap_err();
        let offer = companion.offer_link(SECOND_LINK).unwrap();
        assert!(offer.replaces);

        let result = companion
            .pair(&sessions, "Pixel", || {
                panic!("the feeds stop only on success")
            })
            .await;

        assert!(matches!(result.err(), Some(PairError::Request(_))));
        assert!(companion.pending_offer().is_some());
        assert_eq!(
            companion
                .dialer
                .target()
                .await
                .map(|target| target.id.to_string()),
            Some(LAPTOP.to_owned())
        );
        assert_eq!(
            std::fs::read_to_string(dir.path().join("laptop")).unwrap(),
            format!("{LAPTOP}\n")
        );
        assert_eq!(sessions.header(), None);
    }

    fn answered(id: &str) -> Vec<Paired> {
        vec![Paired {
            id: id.to_owned(),
            name: "orchard".to_owned(),
            value: format!("7.{}", "ab".repeat(32)),
        }]
    }

    #[tokio::test]
    async fn a_paired_laptop_becomes_the_laptop_and_its_fleets_are_stored_and_replace_the_old() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let old = "hand_phone_f0123456789ab=3.cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";
        let sessions = Sessions::open(dir.path(), held(Some(old)), Arc::new(TestCipher)).unwrap();
        assert!(sessions.header().is_some());
        let offer = companion.offer_link(SECOND_LINK).unwrap();
        assert!(offer.replaces);
        let (link, generation) = take(&companion);
        let mut stopped = 0;

        let fleets = companion
            .settle(
                &sessions,
                &link,
                generation,
                answered("fa1b2c3d4e5f6"),
                || stopped += 1,
            )
            .await
            .unwrap();

        assert_eq!(stopped, 1);
        assert_eq!(fleets.len(), 1);
        assert_eq!(fleets[0].name, "orchard");
        assert_eq!(
            sessions.header().as_deref(),
            Some(format!("hand_phone_fa1b2c3d4e5f6=7.{}", "ab".repeat(32)).as_str())
        );
        assert!(companion.pending_offer().is_none());
        assert_eq!(
            companion.pairing.lock().unwrap().active(),
            Some(link.endpoint_id.as_str())
        );
        assert_eq!(
            std::fs::read_to_string(dir.path().join("laptop")).unwrap(),
            format!("{}\n", link.endpoint_id)
        );
        assert_eq!(
            companion
                .dialer
                .target()
                .await
                .map(|target| target.id.to_string()),
            Some(link.endpoint_id.clone())
        );
        let again = companion.offer_link(SECOND_LINK).unwrap();
        assert!(!again.replaces);
    }

    #[tokio::test]
    async fn a_cancel_and_a_new_offer_during_a_pairing_do_not_redirect_what_it_commits() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = Sessions::open(dir.path(), held(None), Arc::new(TestCipher)).unwrap();
        companion.offer_link(SECOND_LINK).unwrap();
        let (link, generation) = take(&companion);
        assert!(companion.pending_offer().is_none());
        companion.cancel_pairing();
        companion.offer_link(LINK).unwrap();

        companion
            .settle(
                &sessions,
                &link,
                generation,
                answered("fa1b2c3d4e5f6"),
                || {},
            )
            .await
            .unwrap();

        assert_eq!(
            std::fs::read_to_string(dir.path().join("laptop")).unwrap(),
            format!("{}\n", link.endpoint_id)
        );
        assert_eq!(
            companion
                .dialer
                .target()
                .await
                .map(|target| target.id.to_string()),
            Some(link.endpoint_id.clone())
        );
        assert_eq!(companion.pending_offer().unwrap().laptop, "4d92…6ed6");
        assert!(sessions.header().is_some());
    }

    #[tokio::test]
    async fn a_laptop_file_that_cannot_be_written_still_ends_the_pairing_and_says_so() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = Sessions::open(dir.path(), held(None), Arc::new(TestCipher)).unwrap();
        std::fs::create_dir(dir.path().join("laptop")).unwrap();
        companion.offer_link(SECOND_LINK).unwrap();
        let (link, generation) = take(&companion);

        let paired = companion
            .settle(
                &sessions,
                &link,
                generation,
                answered("fa1b2c3d4e5f6"),
                || {},
            )
            .await
            .unwrap();

        assert_eq!(paired.len(), 1);
        assert!(sessions.unsaved());
        assert!(sessions.header().is_some());
        assert_eq!(
            companion.pairing.lock().unwrap().active(),
            Some(link.endpoint_id.as_str())
        );
        assert_eq!(
            companion
                .dialer
                .target()
                .await
                .map(|target| target.id.to_string()),
            Some(link.endpoint_id.clone())
        );
    }

    #[tokio::test]
    async fn a_pairing_taken_before_a_forget_does_not_keep_the_laptop() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = Sessions::open(dir.path(), held(None), Arc::new(TestCipher)).unwrap();
        companion.offer_link(SECOND_LINK).unwrap();
        let (link, generation) = take(&companion);

        companion.forget_laptop(|| Ok(())).await.unwrap();
        let result = companion
            .settle(
                &sessions,
                &link,
                generation,
                answered("fa1b2c3d4e5f6"),
                || {},
            )
            .await;

        assert_eq!(result.err(), Some(PairError::NoPending));
        assert!(companion.pairing.lock().unwrap().active().is_none());
        assert!(sessions.header().is_none());
        assert!(!dir.path().join("laptop").exists());
        assert!(companion.dialer.target().await.is_none());
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn a_forget_that_lands_while_a_pairing_commits_leaves_no_store_laptop_or_target() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let old = "hand_phone_f0123456789ab=3.cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";
        let sessions =
            Arc::new(Sessions::open(dir.path(), held(Some(old)), Arc::new(TestCipher)).unwrap());
        companion.offer_link(SECOND_LINK).unwrap();
        let (link, generation) = take(&companion);
        let (done, settled) = std::sync::mpsc::channel();
        let gate = Arc::new(Gate {
            reached: tokio::sync::Notify::new(),
            resume: tokio::sync::Notify::new(),
        });
        companion.arm_gate(gate.clone());
        let pairing = {
            let companion = companion.clone();
            let sessions = sessions.clone();
            tokio::spawn(async move {
                let result = companion
                    .settle(
                        &sessions,
                        &link,
                        generation,
                        answered("fa1b2c3d4e5f6"),
                        || {},
                    )
                    .await;
                let _ = done.send(());
                result
            })
        };

        gate.reached.notified().await;
        let cleared = {
            let sessions = sessions.clone();
            companion
                .forget_laptop(move || {
                    let cleared = sessions.forget();
                    gate.resume.notify_one();
                    let _ = settled.recv_timeout(Duration::from_secs(1));
                    cleared
                })
                .await
        };

        let result = pairing.await.unwrap();
        assert!(cleared.is_ok());
        assert_eq!(result.err(), Some(PairError::NoPending));
        assert!(sessions.header().is_none());
        assert!(companion.pairing.lock().unwrap().active().is_none());
        assert!(!dir.path().join("laptop").exists());
        assert!(companion.dialer.target().await.is_none());
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn a_forget_that_is_clearing_does_not_stop_an_offer_or_a_cancel() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let (entered, entered_rx) = std::sync::mpsc::channel();
        let (release, released) = std::sync::mpsc::channel();
        let forgetting = {
            let companion = companion.clone();
            tokio::spawn(async move {
                companion
                    .forget_laptop(move || {
                        let _ = entered.send(());
                        released
                            .recv_timeout(Duration::from_secs(30))
                            .map_err(|_| io::Error::other("the clear was not answered"))
                    })
                    .await
            })
        };
        entered_rx.recv_timeout(Duration::from_secs(5)).unwrap();
        let (offered, offer_rx) = std::sync::mpsc::channel();
        let tapping = {
            let companion = companion.clone();
            std::thread::spawn(move || {
                let tap = companion.offer_link(LINK);
                companion.cancel_pairing();
                let _ = offered.send(tap);
            })
        };
        let tapped = offer_rx.recv_timeout(Duration::from_secs(2));
        let _ = release.send(());
        tapping.join().unwrap();
        let forgotten = forgetting.await.unwrap();
        assert!(forgotten.is_ok());
        assert_eq!(
            tapped.expect("the offer and the cancel waited behind the clear"),
            Ok(Offer {
                laptop: "4d92…6ed6".to_owned(),
                replaces: false,
            })
        );
        assert!(companion.pending_offer().is_none());
        assert!(companion.pairing.lock().unwrap().active().is_none());
        assert!(companion.dialer.target().await.is_none());
    }

    #[tokio::test]
    async fn a_forget_between_reading_a_laptop_and_dialing_it_leaves_no_target() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let gate = std::sync::Arc::new(Gate {
            reached: tokio::sync::Notify::new(),
            resume: tokio::sync::Notify::new(),
        });
        companion.arm_gate(gate.clone());
        let opening = {
            let companion = companion.clone();
            tokio::spawn(async move { companion.open_board().await })
        };
        gate.reached.notified().await;
        companion.forget_laptop(|| Ok(())).await.unwrap();
        gate.resume.notify_one();

        assert_eq!(opening.await.unwrap().unwrap(), None);
        assert!(companion.dialer.target().await.is_none());
        assert!(!dir.path().join("laptop").exists());
    }

    #[tokio::test]
    async fn a_forget_between_reading_a_laptop_and_an_open_link_leaves_no_target() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let gate = std::sync::Arc::new(Gate {
            reached: tokio::sync::Notify::new(),
            resume: tokio::sync::Notify::new(),
        });
        companion.arm_gate(gate.clone());
        let opening = {
            let companion = companion.clone();
            tokio::spawn(async move { companion.open_link_after(OPEN, |_| async { Ok(()) }).await })
        };
        gate.reached.notified().await;
        companion.forget_laptop(|| Ok(())).await.unwrap();
        gate.resume.notify_one();

        assert!(matches!(
            opening.await.unwrap(),
            Err(OpenLinkError::NotPaired)
        ));
        assert!(companion.dialer.target().await.is_none());
        assert!(!dir.path().join("laptop").exists());
    }

    #[tokio::test]
    async fn a_store_that_cannot_be_read_stops_a_pairing_before_anything_is_taken_or_sent() {
        struct Unreadable;

        impl Jar for Unreadable {
            fn cookies(&self) -> io::Result<Option<String>> {
                Err(io::Error::other("no"))
            }

            fn clear(&self) -> io::Result<()> {
                Ok(())
            }
        }
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = Sessions::open(dir.path(), Unreadable, Arc::new(TestCipher)).unwrap();
        companion.offer_link(SECOND_LINK).unwrap();

        let result = companion.pair(&sessions, "Pixel", || {}).await;

        assert_eq!(result.err(), Some(PairError::Jar));
        assert!(companion.pending_offer().is_some());
        assert!(companion.dialer.target().await.is_none());
        let (link, generation) = take(&companion);
        let late = companion
            .settle(
                &sessions,
                &link,
                generation,
                answered("fa1b2c3d4e5f6"),
                || {},
            )
            .await;
        assert_eq!(late.err(), Some(PairError::NotSaved));
        assert!(!dir.path().join("laptop").exists());
    }

    #[tokio::test]
    async fn the_unreachable_message_names_the_saved_laptop() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        assert_eq!(companion.cant_reach(), "Can't reach your laptop.");
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        assert_eq!(companion.cant_reach(), "Can't reach 4d92…6ed6.");
    }

    const OPEN: &str = "handboard://open/f0123456789ab/task/t3#r5";
    const LAPTOP: &str = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6";
    const RELAY: &str = "https://relay.example.test:7842/";

    #[tokio::test]
    async fn a_saved_relay_is_dialed_when_a_link_reopens_the_board() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n{RELAY}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();

        companion
            .open_link_after(OPEN, |_| async { Ok(()) })
            .await
            .unwrap();

        let target = companion.dialer.target().await.unwrap();
        assert_eq!(target.id.to_string(), LAPTOP);
        assert_eq!(
            target.relay_urls().next().map(|relay| relay.to_string()),
            Some(RELAY.to_owned())
        );
    }

    #[tokio::test]
    async fn an_open_link_with_no_laptop_dials_nothing_and_says_to_pair_first() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let error = companion.open_link(OPEN).await.unwrap_err();
        assert!(matches!(error, OpenLinkError::NotPaired));
        assert_eq!(error.message(), "Pair this phone with your laptop first.");
        assert!(companion.dialer.target().await.is_none());
        assert!(!dir.path().join("laptop").exists());
    }

    #[tokio::test]
    async fn an_open_link_while_a_pairing_is_pending_is_dropped_and_the_offer_stays() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let offer = companion.offer_link(SECOND_LINK).unwrap();
        let error = companion.open_link(OPEN).await.unwrap_err();
        assert!(matches!(error, OpenLinkError::Pending));
        assert_eq!(error.message(), PAIRING_PENDING);
        assert_eq!(companion.pending_offer().unwrap().laptop, offer.laptop);
        assert!(companion.dialer.target().await.is_none());
        assert_eq!(
            std::fs::read_to_string(dir.path().join("laptop")).unwrap(),
            format!("{LAPTOP}\n")
        );
    }

    #[tokio::test]
    async fn a_hostile_open_link_dials_nothing() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        for text in [
            "handboard://open/f0123456789ab/task/t3/extra",
            "handboard://open/f0123456789ab/task%2Ft3",
            "handboard://open/f0123456789ab/task/t3?x=1",
            LINK,
            "",
        ] {
            let error = companion.open_link(text).await.unwrap_err();
            assert!(matches!(error, OpenLinkError::Rejected), "{text}");
            assert_eq!(error.message(), "This is not a link Hand can open.");
        }
        assert!(companion.dialer.target().await.is_none());
    }

    #[tokio::test]
    async fn an_open_link_to_a_reachable_board_returns_its_proxy_url_and_changes_nothing() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let reached = std::sync::Mutex::new(Vec::new());
        let url = companion
            .open_link_after(OPEN, |fleet| {
                reached.lock().unwrap().push(fleet);
                async { Ok(()) }
            })
            .await
            .unwrap();
        let port = companion.port();
        assert_eq!(
            url,
            format!("http://localhost:{port}/f0123456789ab/task/t3#r5")
        );
        assert_eq!(*reached.lock().unwrap(), ["f0123456789ab"]);
        let bundled = Url::parse("http://tauri.localhost/").unwrap();
        assert!(crate::navigation::may_load(
            &Url::parse(&url).unwrap(),
            &bundled,
            Some(&proxy_origin(port))
        ));
        assert_eq!(
            companion
                .dialer
                .target()
                .await
                .map(|target| target.id.to_string()),
            Some(LAPTOP.to_owned())
        );
        assert_eq!(
            std::fs::read(dir.path().join("laptop")).unwrap(),
            format!("{LAPTOP}\n").into_bytes()
        );
        assert!(companion.pending_offer().is_none());
    }

    #[tokio::test]
    async fn an_open_link_to_an_unreachable_laptop_fails_before_any_navigation() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let error = companion.open_link(OPEN).await.unwrap_err();
        assert!(
            matches!(error, OpenLinkError::Failed(ConfirmError::Open(_))),
            "{error:?}"
        );
        assert!(!error.message().is_empty());
        assert_eq!(
            companion
                .dialer
                .target()
                .await
                .map(|target| target.id.to_string()),
            Some(LAPTOP.to_owned())
        );
        assert_eq!(
            std::fs::read_to_string(dir.path().join("laptop")).unwrap(),
            format!("{LAPTOP}\n")
        );
        assert!(companion.pending_offer().is_none());
    }

    #[test]
    fn a_busy_laptop_asks_for_another_tap_and_the_other_failures_keep_their_messages() {
        assert_eq!(
            OpenLinkError::Failed(ConfirmError::Open(OpenError::Busy)).message(),
            "Your laptop is busy. Tap the link again."
        );
        assert_eq!(
            OpenLinkError::Failed(ConfirmError::BoardDown).message(),
            crate::dialer::Notice::BoardUnreachable.message()
        );
        assert_eq!(
            OpenLinkError::Failed(ConfirmError::Open(OpenError::Unreachable)).message(),
            crate::dialer::Notice::Unreachable.message()
        );
        assert_eq!(
            OpenLinkError::Failed(ConfirmError::Open(OpenError::Lost)).message(),
            crate::dialer::Notice::Lost.message()
        );
    }
}
