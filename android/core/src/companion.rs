use std::fmt;
use std::future::Future;
use std::io;
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::Mutex as StdMutex;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::time::Duration;

use iroh::SecretKey;
use iroh::address_lookup::{DnsAddressLookup, PkarrResolver};
use iroh::endpoint::{Builder, Endpoint, PortmapperConfig, QuicTransportConfig, VarInt, presets};
use tokio::task::JoinHandle;
use tokio::time::timeout;

use crate::dialer::{ALPN, Dialer, STREAMS_PER_CONNECTION, parse_target};
use crate::dialers::Dialers;
use crate::http::{Request, RequestError, Response, exchange};
use crate::key::{self, KeyCipher};
use crate::link::{Link, short_id};
use crate::pair::{PairError, redeem};
use crate::pairing::{Offer, OfferError, Pairing};
use crate::proxy::{self, Proxy, Slot};
use crate::sessions::{Jar, Paired, Refusal, Sessions};

pub const POLL_DEADLINE: Duration = Duration::from_secs(30);

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Polled {
    Live,
    Reached,
    Missed,
}

fn builder(transport: QuicTransportConfig) -> Builder {
    Endpoint::builder(presets::N0)
        .clear_address_lookup()
        .address_lookup(PkarrResolver::n0_dns())
        .address_lookup(DnsAddressLookup::n0_dns())
        .portmapper_config(PortmapperConfig::Disabled)
        .alpns(vec![ALPN.to_vec()])
        .transport_config(transport)
}

struct Binding {
    secret_key: SecretKey,
    transport: QuicTransportConfig,
}

impl Binding {
    async fn bind(&self) -> Result<Endpoint, StartError> {
        builder(self.transport.clone())
            .secret_key(self.secret_key.clone())
            .bind()
            .await
            .map_err(|error| StartError::Bind(error.to_string()))
    }
}

#[derive(Default)]
struct Duty {
    power: tokio::sync::Mutex<()>,
    asleep: AtomicBool,
    sleeps: AtomicU64,
    wakes: AtomicU64,
    polls: AtomicU64,
}

pub struct CompanionConfig {
    pub data_dir: PathBuf,
    pub cipher: Arc<dyn KeyCipher>,
}

#[derive(Clone)]
pub struct Companion {
    dialers: Dialers,
    slot: Slot,
    port: u16,
    pairing: std::sync::Arc<StdMutex<Pairing>>,
    // Lock order: store, then pairing, then the session store's data, then Dialers (a leaf, never
    // held across an await). Taking one while holding a later one deadlocks, and no test sees it.
    store: std::sync::Arc<StdMutex<()>>,
    network_changes: Arc<AtomicU64>,
    binding: Arc<Binding>,
    duty: Arc<Duty>,
    #[cfg(test)]
    gate: std::sync::Arc<StdMutex<Option<std::sync::Arc<Gate>>>>,
}

#[cfg(test)]
struct Gate {
    reached: tokio::sync::Notify,
    resume: tokio::sync::Notify,
}

impl Companion {
    pub fn dialers(&self) -> &Dialers {
        &self.dialers
    }

    pub async fn start(config: CompanionConfig) -> Result<Self, StartError> {
        std::fs::create_dir_all(&config.data_dir)?;
        let secret_key = key::load_or_create(&config.data_dir, config.cipher.as_ref())?;
        let transport = QuicTransportConfig::builder()
            .max_concurrent_bidi_streams(VarInt::from_u32(STREAMS_PER_CONNECTION as u32))
            .max_concurrent_uni_streams(VarInt::from_u32(0))
            .build();
        let binding = Arc::new(Binding {
            secret_key,
            transport,
        });
        let endpoint = binding.bind().await?;
        let (proxy, listeners) = Proxy::bind(&config.data_dir).await?;
        let slot = Slot::default();
        for listener in listeners {
            tokio::spawn(proxy::serve(listener, slot.clone()));
        }
        Ok(Self {
            dialers: Dialers::new(endpoint),
            slot,
            port: proxy.port(),
            pairing: std::sync::Arc::new(StdMutex::new(Pairing::default())),
            store: std::sync::Arc::new(StdMutex::new(())),
            network_changes: Arc::new(AtomicU64::new(0)),
            binding,
            duty: Arc::new(Duty::default()),
            #[cfg(test)]
            gate: std::sync::Arc::new(StdMutex::new(None)),
        })
    }

    pub async fn route<J: Jar>(&self, sessions: &Sessions<J>, fleet: &str) -> Option<Dialer> {
        let (endpoint, relay) = sessions.route_of(fleet)?;
        let target = parse_target(&endpoint, relay.as_deref()).ok()?;
        Some(self.dialers.host(&endpoint, target).await)
    }

    pub async fn request<J: Jar>(
        &self,
        sessions: &Sessions<J>,
        fleet: &str,
        request: &Request,
    ) -> Result<Response, RequestError> {
        let dialer = self
            .route(sessions, fleet)
            .await
            .ok_or(RequestError::NoHost)?;
        exchange(&dialer, request).await
    }

    pub async fn fallback<J: Jar>(
        &self,
        sessions: &Sessions<J>,
        fleet: &str,
    ) -> Option<(u16, String)> {
        let cookie = sessions.cookie(fleet)?;
        let dialer = self.route(sessions, fleet).await?;
        self.slot.set(Some(dialer));
        Some((self.port, cookie))
    }

    pub fn phone_id(&self) -> String {
        short_id(&self.dialers.endpoint().id().to_string())
    }

    pub fn port(&self) -> u16 {
        self.port
    }

    pub fn offer_link(&self, text: &str) -> Result<Offer, OfferError> {
        self.pairing.lock().unwrap().offer(text.trim())
    }

    pub fn pending_offer(&self) -> Option<Offer> {
        self.pairing.lock().unwrap().pending_offer()
    }

    pub fn cancel_pairing(&self) {
        self.pairing.lock().unwrap().clear_pending();
    }

    pub async fn forget_all(&self, clear: impl FnOnce() -> io::Result<()>) -> io::Result<()> {
        let cleared = {
            let _store = self.store.lock().unwrap();
            let cleared = clear();
            self.pairing.lock().unwrap().forget();
            cleared
        };
        self.slot.set(None);
        for dialer in self.dialers.all() {
            dialer.set_target(None).await;
        }
        cleared
    }

    pub async fn forget_host(
        &self,
        endpoint: &str,
        clear: impl FnOnce() -> io::Result<()>,
    ) -> io::Result<()> {
        let cleared = {
            let _store = self.store.lock().unwrap();
            let cleared = clear();
            self.pairing.lock().unwrap().forget();
            cleared
        };
        if let Some(dialer) = self.dialers.get(endpoint) {
            self.slot
                .set(self.slot.get().filter(|held| !held.same(&dialer)));
        }
        self.dialers.remove(endpoint).await;
        cleared
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

    pub async fn pair<J: Jar>(
        &self,
        sessions: &Sessions<J>,
        name: &str,
    ) -> Result<Vec<Paired>, PairError> {
        let (link, generation) = {
            let mut pairing = self.pairing.lock().unwrap();
            let generation = pairing.generation();
            (pairing.take(), generation)
        };
        let link = link.ok_or(PairError::NoPending)?;
        if sessions.held_elsewhere(&link.fleet, &link.endpoint_id) {
            self.pairing.lock().unwrap().restore(link);
            return Err(PairError::Held);
        }
        let target = link.target().map_err(|_| PairError::NoPending)?;
        let dialer = Dialer::new(self.dialers.endpoint(), Some(target));
        let redeemed = redeem(&dialer, &link, name).await;
        dialer.disconnect().await;
        match redeemed {
            Ok(paired) => self.settle(sessions, &link, generation, paired).await,
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
    ) -> Result<Vec<Paired>, PairError> {
        #[cfg(test)]
        self.pass_gate().await;
        let target = link.target().map_err(|_| PairError::NoPending)?;
        {
            let _store = self.store.lock().unwrap();
            let pairing = self.pairing.lock().unwrap();
            if pairing.generation() != generation {
                return Err(PairError::NoPending);
            }
            sessions
                .pair(&link.endpoint_id, link.relay.as_deref(), &paired)
                .map_err(|refusal| match refusal {
                    Refusal::Held => PairError::Held,
                    Refusal::NotSaved => PairError::NotSaved,
                })?;
        }
        self.dialers.host(&link.endpoint_id, target).await;
        if !sessions
            .hosts()
            .iter()
            .any(|host| host.endpoint == link.endpoint_id)
        {
            self.dialers.remove(&link.endpoint_id).await;
            return Err(PairError::NoPending);
        }
        Ok(paired)
    }

    pub async fn reconnect_on_resume(&self, then: impl FnOnce()) {
        for dialer in self.dialers.all() {
            dialer.disconnect().await;
        }
        then();
    }

    pub async fn network_change(&self, then: impl FnOnce()) {
        self.dialers.endpoint().network_change().await;
        self.network_changes.fetch_add(1, Ordering::Relaxed);
        then();
    }

    pub fn asleep(&self) -> bool {
        self.duty.asleep.load(Ordering::SeqCst)
    }

    pub async fn sleep(&self) {
        let _turn = self.duty.power.lock().await;
        if self.duty.asleep.swap(true, Ordering::SeqCst) {
            return;
        }
        self.dialers.close().await;
        self.duty.sleeps.fetch_add(1, Ordering::Relaxed);
    }

    pub async fn wake(&self) -> Result<(), StartError> {
        let _turn = self.duty.power.lock().await;
        if !self.asleep() {
            return Ok(());
        }
        self.dialers.open(self.binding.bind().await?);
        self.duty.asleep.store(false, Ordering::SeqCst);
        self.duty.wakes.fetch_add(1, Ordering::Relaxed);
        Ok(())
    }

    pub async fn poll_asleep(
        &self,
        poll: impl Future<Output = bool>,
    ) -> Result<Polled, StartError> {
        self.poll_asleep_within(POLL_DEADLINE, poll).await
    }

    pub async fn poll_asleep_within(
        &self,
        deadline: Duration,
        poll: impl Future<Output = bool>,
    ) -> Result<Polled, StartError> {
        let _turn = self.duty.power.lock().await;
        if !self.asleep() {
            return Ok(Polled::Live);
        }
        self.dialers.open(self.binding.bind().await?);
        let reached = timeout(deadline, poll).await.unwrap_or(false);
        self.dialers.close().await;
        self.duty.polls.fetch_add(1, Ordering::Relaxed);
        Ok(if reached {
            Polled::Reached
        } else {
            Polled::Missed
        })
    }

    pub async fn report(&self) -> String {
        let dialers = self.dialers.all();
        let mut connected = 0;
        for dialer in &dialers {
            connected += usize::from(dialer.connected().await);
        }
        let iroh = match dialers.first() {
            Some(dialer) => dialer.report().await,
            None => Dialer::new(self.dialers.endpoint(), None).report().await,
        };
        format!(
            "{iroh} hosts={} connected={connected} network_changes={} state={} sleeps={} wakes={} polls={}",
            dialers.len(),
            self.network_changes.load(Ordering::Relaxed),
            if self.asleep() { "asleep" } else { "live" },
            self.duty.sleeps.load(Ordering::Relaxed),
            self.duty.wakes.load(Ordering::Relaxed),
            self.duty.polls.load(Ordering::Relaxed),
        )
    }

    pub fn report_every(
        self: &Arc<Self>,
        period: Duration,
        emit: impl Fn(&str) + Send + Sync + 'static,
    ) -> Reporter {
        let this = self.clone();
        Reporter(tokio::spawn(async move {
            loop {
                emit(&this.report().await);
                tokio::time::sleep(period).await;
            }
        }))
    }
}

pub struct Reporter(JoinHandle<()>);

impl Drop for Reporter {
    fn drop(&mut self) {
        self.0.abort();
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
    use std::sync::atomic::{AtomicBool, Ordering};

    use super::*;
    use crate::http::Method;
    use crate::key::CipherError;
    use crate::testutil::TempDir;

    #[tokio::test]
    async fn the_endpoint_maps_no_ports_publishes_no_address_and_keeps_the_https_probes() {
        let builder = builder(QuicTransportConfig::default());
        let text = format!("{builder:?}");
        assert!(text.contains("portmapper_config: Disabled"), "{text}");
        assert!(text.contains("https_probes: true"), "{text}");
        let endpoint = builder.bind().await.unwrap();
        let lookup = endpoint.address_lookup().unwrap();
        assert_eq!(lookup.len(), 2, "{lookup:?}");
        assert!(!format!("{lookup:?}").contains("Publisher"), "{lookup:?}");
        endpoint.close().await;
    }

    #[tokio::test]
    async fn a_network_change_reaches_the_endpoint_and_then_the_hook_and_is_counted() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let hooks = AtomicU64::new(0);
        companion
            .network_change(|| {
                hooks.fetch_add(1, Ordering::SeqCst);
            })
            .await;
        assert_eq!(hooks.load(Ordering::SeqCst), 1);
        assert!(companion.report().await.contains(" network_changes=1"));
        companion.network_change(|| {}).await;
        assert!(companion.report().await.contains(" network_changes=2"));
    }

    #[tokio::test]
    async fn a_resume_drops_the_connection_and_then_runs_the_hook() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let hooks = AtomicU64::new(0);
        companion
            .reconnect_on_resume(|| {
                hooks.fetch_add(1, Ordering::SeqCst);
            })
            .await;
        assert_eq!(hooks.load(Ordering::SeqCst), 1);
    }

    #[tokio::test]
    async fn the_report_task_ticks_until_its_reporter_is_dropped() {
        let dir = TempDir::new();
        let companion = Arc::new(Companion::start(config(&dir)).await.unwrap());
        let lines = Arc::new(AtomicU64::new(0));
        let counted = lines.clone();
        let reporter = companion.report_every(Duration::from_millis(10), move |line| {
            assert!(line.starts_with("iroh "), "{line}");
            counted.fetch_add(1, Ordering::SeqCst);
        });
        let deadline = std::time::Instant::now() + Duration::from_secs(10);
        while lines.load(Ordering::SeqCst) < 3 && std::time::Instant::now() < deadline {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        assert!(lines.load(Ordering::SeqCst) >= 3);
        drop(reporter);
        while Arc::strong_count(&lines) > 1 && std::time::Instant::now() < deadline {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        assert_eq!(
            Arc::strong_count(&lines),
            1,
            "the task still owns its emitter"
        );
        let after = lines.load(Ordering::SeqCst);
        tokio::time::sleep(Duration::from_millis(100)).await;
        assert_eq!(lines.load(Ordering::SeqCst), after);
    }

    const HOST: &str = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6";
    const OTHER_HOST: &str = "d6e458a66697094c1bc7b54cb83fea66a8db57c49f37514944579712cd7e43c5";
    const FLEET: &str = "f0123456789ab";
    const OTHER_FLEET: &str = "fa1b2c3d4e5f6";
    const LINK: &str = "handboard://c#v=1&e=4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6&f=f0123456789ab&c=K7QM3XRD2P";
    const SECOND_LINK: &str = "handboard://c#v=1&e=d6e458a66697094c1bc7b54cb83fea66a8db57c49f37514944579712cd7e43c5&f=fa1b2c3d4e5f6&c=0123456789";

    #[derive(Default)]
    struct TestCipher(AtomicBool);

    impl KeyCipher for TestCipher {
        fn probe(&self) -> Result<(), CipherError> {
            Ok(())
        }

        fn seal(&self, plaintext: &[u8]) -> Result<Vec<u8>, CipherError> {
            if self.0.load(Ordering::SeqCst) {
                return Err(CipherError::Io(io::Error::other("the keystore refused")));
            }
            Ok(plaintext.to_vec())
        }

        fn open(&self, sealed: &[u8]) -> Result<Vec<u8>, CipherError> {
            Ok(sealed.to_vec())
        }
    }

    fn config(dir: &TempDir) -> CompanionConfig {
        CompanionConfig {
            data_dir: dir.path().to_owned(),
            cipher: Arc::new(TestCipher::default()),
        }
    }

    struct EmptyJar;

    impl Jar for EmptyJar {
        fn cookies(&self) -> io::Result<Option<String>> {
            Ok(None)
        }

        fn clear(&self) -> io::Result<()> {
            Ok(())
        }
    }

    struct UnreadableJar;

    impl Jar for UnreadableJar {
        fn cookies(&self) -> io::Result<Option<String>> {
            Err(io::Error::other("the jar is unreadable"))
        }

        fn clear(&self) -> io::Result<()> {
            Ok(())
        }
    }

    fn sessions(dir: &TempDir) -> Sessions<EmptyJar> {
        Sessions::open(dir.path(), EmptyJar, Arc::new(TestCipher::default())).unwrap()
    }

    fn answered(id: &str) -> Vec<Paired> {
        vec![Paired {
            id: id.to_owned(),
            name: "orchard".to_owned(),
            value: format!("7.{}", "ab".repeat(32)),
        }]
    }

    fn take(companion: &Companion) -> (Link, u64) {
        let mut pairing = companion.pairing.lock().unwrap();
        let generation = pairing.generation();
        (pairing.take().unwrap(), generation)
    }

    async fn target(companion: &Companion, host: &str) -> Option<String> {
        companion
            .dialers()
            .get(host)?
            .target()
            .await
            .map(|target| target.id.to_string())
    }

    async fn paired_with(
        companion: &Companion,
        sessions: &Sessions<EmptyJar>,
        link: &str,
        fleet: &str,
    ) {
        companion.offer_link(link).unwrap();
        let (link, generation) = take(companion);
        companion
            .settle(sessions, &link, generation, answered(fleet))
            .await
            .unwrap();
    }

    #[tokio::test]
    async fn the_phone_id_is_the_short_form_of_its_own_endpoint_id() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let endpoint = companion.dialers().endpoint().id().to_string();

        assert_eq!(endpoint.len(), 64);
        assert_eq!(
            companion.phone_id(),
            format!("{}…{}", &endpoint[..4], &endpoint[60..])
        );
    }

    #[tokio::test]
    async fn a_request_for_a_fleet_no_host_holds_says_so() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let request = Request::new(
            Method::Get,
            FLEET,
            "/api/v1/status",
            Some("hand_phone_f0123456789ab=abc"),
            None,
            &[],
        )
        .unwrap();

        let result = companion.request(&sessions(&dir), FLEET, &request).await;

        assert_eq!(result.err(), Some(RequestError::NoHost));
    }

    #[tokio::test]
    async fn offering_a_link_dials_nothing_until_it_is_paired() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let offer = companion.offer_link(LINK).unwrap();
        assert_eq!(offer.host, "4d92…6ed6");
        assert!(companion.dialers().all().is_empty());
    }

    #[tokio::test]
    async fn a_link_with_surrounding_whitespace_is_accepted() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let padded = format!("\n\t {LINK} \r\n");
        let offer = companion.offer_link(&padded).unwrap();
        assert_eq!(offer.host, "4d92…6ed6");
        assert_eq!(companion.pending_offer().unwrap().host, offer.host);
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
        assert_eq!(companion.pending_offer().unwrap().host, first.host);
    }

    #[tokio::test]
    async fn pairing_without_a_pending_link_says_so() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let result = companion.pair(&sessions(&dir), "Pixel").await;
        assert_eq!(result.err(), Some(PairError::NoPending));
    }

    #[tokio::test]
    async fn a_pairing_that_cannot_reach_the_host_changes_nothing_and_stays_pending() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        paired_with(&companion, &sessions, LINK, FLEET).await;
        companion.offer_link(SECOND_LINK).unwrap();

        let result = companion.pair(&sessions, "Pixel").await;

        assert!(matches!(result.err(), Some(PairError::Request(_))));
        assert!(companion.pending_offer().is_some());
        assert_eq!(sessions.hosts().len(), 1);
        assert_eq!(companion.dialers().all().len(), 1);
    }

    #[tokio::test]
    async fn a_fleet_held_through_another_host_is_refused_before_that_host_is_asked() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        paired_with(&companion, &sessions, LINK, FLEET).await;
        let copied = format!("handboard://c#v=1&e={OTHER_HOST}&f={FLEET}&c=0123456789");
        companion.offer_link(&copied).unwrap();

        let result = companion.pair(&sessions, "Pixel").await;

        assert_eq!(result.err(), Some(PairError::Held));
        assert!(companion.pending_offer().is_some());
        assert_eq!(sessions.hosts().len(), 1);
        assert!(companion.dialers().get(OTHER_HOST).is_none());
        assert!(PairError::Held.message().contains("fleet ID"));
    }

    #[tokio::test]
    async fn a_set_that_names_a_fleet_held_through_another_host_is_refused_whole() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        paired_with(&companion, &sessions, LINK, FLEET).await;
        companion.offer_link(SECOND_LINK).unwrap();
        let (link, generation) = take(&companion);
        let mut set = answered(OTHER_FLEET);
        set.extend(answered(FLEET));

        let result = companion.settle(&sessions, &link, generation, set).await;

        assert_eq!(result.err(), Some(PairError::Held));
        assert_eq!(sessions.hosts().len(), 1);
        assert!(companion.dialers().get(OTHER_HOST).is_none());
    }

    #[tokio::test]
    async fn a_second_host_is_added_beside_the_first_and_each_fleet_routes_to_its_own() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        paired_with(&companion, &sessions, LINK, FLEET).await;
        paired_with(&companion, &sessions, SECOND_LINK, OTHER_FLEET).await;

        assert!(companion.pending_offer().is_none());
        assert_eq!(sessions.hosts().len(), 2);
        assert_eq!(target(&companion, HOST).await.as_deref(), Some(HOST));
        assert_eq!(
            target(&companion, OTHER_HOST).await.as_deref(),
            Some(OTHER_HOST)
        );
        let first = companion.route(&sessions, FLEET).await.unwrap();
        let second = companion.route(&sessions, OTHER_FLEET).await.unwrap();
        assert!(!first.same(&second));
        assert_eq!(first.target().await.unwrap().id.to_string(), HOST);
        assert_eq!(second.target().await.unwrap().id.to_string(), OTHER_HOST);
        assert!(companion.route(&sessions, "f999999999999").await.is_none());
    }

    #[tokio::test]
    async fn a_relay_from_the_link_is_the_hosts_and_a_repair_takes_the_new_one() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        let relayed = format!("{LINK}&r=https%3A%2F%2Frelay.example.test%3A7842%2F");
        paired_with(&companion, &sessions, &relayed, FLEET).await;
        let dialer = companion.route(&sessions, FLEET).await.unwrap();
        assert_eq!(dialer.target().await.unwrap().relay_urls().count(), 1);

        paired_with(&companion, &sessions, LINK, FLEET).await;

        assert_eq!(sessions.hosts()[0].relay, None);
        let dialer = companion.route(&sessions, FLEET).await.unwrap();
        assert_eq!(dialer.target().await.unwrap().relay_urls().count(), 0);
        assert_eq!(companion.dialers().all().len(), 1);
    }

    #[tokio::test]
    async fn a_cancel_and_a_new_offer_during_a_pairing_do_not_redirect_what_it_commits() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        companion.offer_link(SECOND_LINK).unwrap();
        let (link, generation) = take(&companion);
        assert!(companion.pending_offer().is_none());
        companion.cancel_pairing();
        companion.offer_link(LINK).unwrap();

        companion
            .settle(&sessions, &link, generation, answered(OTHER_FLEET))
            .await
            .unwrap();

        assert_eq!(sessions.hosts()[0].endpoint, OTHER_HOST);
        assert_eq!(
            target(&companion, OTHER_HOST).await.as_deref(),
            Some(OTHER_HOST)
        );
        assert_eq!(companion.pending_offer().unwrap().host, "4d92…6ed6");
    }

    #[tokio::test]
    async fn a_pairing_that_cannot_be_saved_leaves_no_host_and_no_dialer() {
        let dir = TempDir::new();
        let cipher = Arc::new(TestCipher::default());
        let companion = Companion::start(CompanionConfig {
            data_dir: dir.path().to_owned(),
            cipher: cipher.clone(),
        })
        .await
        .unwrap();
        let sessions = Sessions::open(dir.path(), EmptyJar, cipher.clone()).unwrap();
        companion.offer_link(SECOND_LINK).unwrap();
        let (link, generation) = take(&companion);
        cipher.0.store(true, Ordering::SeqCst);

        let result = companion
            .settle(&sessions, &link, generation, answered(OTHER_FLEET))
            .await;

        assert_eq!(result.err(), Some(PairError::NotSaved));
        assert!(sessions.hosts().is_empty());
        assert!(companion.dialers().all().is_empty());
    }

    #[tokio::test]
    async fn a_pairing_whose_jar_cannot_be_read_is_not_saved_and_changes_nothing() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions =
            Sessions::open(dir.path(), UnreadableJar, Arc::new(TestCipher::default())).unwrap();
        companion.offer_link(SECOND_LINK).unwrap();
        let (link, generation) = take(&companion);

        let result = companion
            .settle(&sessions, &link, generation, answered(OTHER_FLEET))
            .await;

        assert_eq!(result.err(), Some(PairError::NotSaved));
        assert!(companion.dialers().all().is_empty());
    }

    #[tokio::test]
    async fn a_pairing_taken_before_a_forget_does_not_keep_the_host() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        companion.offer_link(SECOND_LINK).unwrap();
        let (link, generation) = take(&companion);

        companion.forget_all(|| Ok(())).await.unwrap();
        let result = companion
            .settle(&sessions, &link, generation, answered(OTHER_FLEET))
            .await;

        assert_eq!(result.err(), Some(PairError::NoPending));
        assert!(sessions.hosts().is_empty());
        assert!(companion.dialers().all().is_empty());
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn a_forget_that_lands_while_a_pairing_commits_leaves_no_store_host_or_dialer() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = Arc::new(sessions(&dir));
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
                    .settle(&sessions, &link, generation, answered(OTHER_FLEET))
                    .await;
                let _ = done.send(());
                result
            })
        };

        gate.reached.notified().await;
        let cleared = {
            let sessions = sessions.clone();
            companion
                .forget_all(move || {
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
        assert!(sessions.hosts().is_empty());
        assert!(companion.dialers().all().is_empty());
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
                    .forget_all(move || {
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
                host: "4d92…6ed6".to_owned(),
            })
        );
        assert!(companion.pending_offer().is_none());
    }

    #[tokio::test]
    async fn forgetting_one_host_drops_its_dialer_and_slot_and_leaves_the_other() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        paired_with(&companion, &sessions, LINK, FLEET).await;
        paired_with(&companion, &sessions, SECOND_LINK, OTHER_FLEET).await;
        let kept = companion.route(&sessions, OTHER_FLEET).await.unwrap();
        companion.fallback(&sessions, FLEET).await.unwrap();
        assert!(companion.slot.get().is_some());

        companion
            .forget_host(HOST, || sessions.forget_host(HOST))
            .await
            .unwrap();

        assert!(companion.dialers().get(HOST).is_none());
        assert!(companion.slot.get().is_none());
        assert!(companion.dialers().get(OTHER_HOST).unwrap().same(&kept));
        assert_eq!(kept.target().await.unwrap().id.to_string(), OTHER_HOST);
        assert!(companion.route(&sessions, FLEET).await.is_none());
        assert_eq!(sessions.hosts().len(), 1);
    }

    #[tokio::test]
    async fn forgetting_the_host_another_fallback_serves_keeps_that_slot() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        paired_with(&companion, &sessions, LINK, FLEET).await;
        paired_with(&companion, &sessions, SECOND_LINK, OTHER_FLEET).await;
        companion.fallback(&sessions, OTHER_FLEET).await.unwrap();

        companion
            .forget_host(HOST, || sessions.forget_host(HOST))
            .await
            .unwrap();

        let slot = companion.slot.get().unwrap();
        assert!(slot.same(&companion.dialers().get(OTHER_HOST).unwrap()));
    }

    #[tokio::test]
    async fn forgetting_everything_drops_every_dialer_target_and_the_slot() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        paired_with(&companion, &sessions, LINK, FLEET).await;
        paired_with(&companion, &sessions, SECOND_LINK, OTHER_FLEET).await;
        companion.fallback(&sessions, FLEET).await.unwrap();

        companion.forget_all(|| sessions.forget()).await.unwrap();

        assert!(companion.slot.get().is_none());
        for dialer in companion.dialers().all() {
            assert!(dialer.target().await.is_none());
        }
        assert!(sessions.hosts().is_empty());
    }

    #[tokio::test]
    async fn a_fallback_serves_the_host_of_its_fleet_with_only_that_fleets_cookie() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        assert!(companion.fallback(&sessions, FLEET).await.is_none());
        paired_with(&companion, &sessions, LINK, FLEET).await;
        paired_with(&companion, &sessions, SECOND_LINK, OTHER_FLEET).await;

        let (port, cookie) = companion.fallback(&sessions, FLEET).await.unwrap();
        assert_eq!(port, companion.port());
        assert_eq!(cookie, format!("hand_phone_{FLEET}=7.{}", "ab".repeat(32)));
        assert!(
            companion
                .slot
                .get()
                .unwrap()
                .same(&companion.dialers().get(HOST).unwrap())
        );

        let (_, cookie) = companion.fallback(&sessions, OTHER_FLEET).await.unwrap();
        assert!(cookie.starts_with(&format!("hand_phone_{OTHER_FLEET}=")));
        assert!(
            companion
                .slot
                .get()
                .unwrap()
                .same(&companion.dialers().get(OTHER_HOST).unwrap())
        );
    }

    #[tokio::test]
    async fn a_new_dialer_made_while_asleep_is_down_and_wake_opens_every_one() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        paired_with(&companion, &sessions, LINK, FLEET).await;
        companion.sleep().await;
        paired_with(&companion, &sessions, SECOND_LINK, OTHER_FLEET).await;

        let dialers = companion.dialers().all();
        assert_eq!(dialers.len(), 2);
        assert!(dialers.iter().all(Dialer::is_down));

        companion.wake().await.unwrap();

        assert!(dialers.iter().all(|dialer| !dialer.is_down()));
        let live = companion.dialers().endpoint();
        assert!(!live.is_closed());
        assert!(
            dialers
                .iter()
                .all(|dialer| dialer.endpoint().id() == live.id())
        );
    }

    #[test]
    fn a_poll_may_run_for_thirty_seconds() {
        assert_eq!(POLL_DEADLINE, Duration::from_secs(30));
    }

    #[tokio::test]
    async fn a_poll_that_outlasts_its_deadline_is_missed_and_the_endpoint_is_closed_again() {
        let dir = TempDir::new();
        let companion = Companion::start(config(&dir)).await.unwrap();
        let sessions = sessions(&dir);
        paired_with(&companion, &sessions, LINK, FLEET).await;
        paired_with(&companion, &sessions, SECOND_LINK, OTHER_FLEET).await;
        companion.sleep().await;

        let polled = companion
            .poll_asleep_within(Duration::from_millis(50), std::future::pending())
            .await
            .unwrap();

        assert_eq!(polled, Polled::Missed);
        assert!(companion.asleep() && companion.dialers().is_down());
        assert!(companion.dialers().all().iter().all(Dialer::is_down));
        assert!(companion.dialers().endpoint().is_closed());
        assert!(companion.report().await.contains(" polls=1"));
        companion.wake().await.unwrap();
        assert!(!companion.asleep());
    }

    #[tokio::test]
    async fn a_wake_that_lands_in_a_poll_waits_for_it() {
        let dir = TempDir::new();
        let companion = Arc::new(Companion::start(config(&dir)).await.unwrap());
        companion.sleep().await;
        let release = Arc::new(tokio::sync::Notify::new());
        let inside = Arc::new(tokio::sync::Notify::new());

        let polling = tokio::spawn({
            let (companion, release, inside) = (companion.clone(), release.clone(), inside.clone());
            async move {
                companion
                    .poll_asleep(async {
                        inside.notify_one();
                        release.notified().await;
                        true
                    })
                    .await
            }
        });
        inside.notified().await;
        let mut waking = tokio::spawn({
            let companion = companion.clone();
            async move { companion.wake().await }
        });

        assert!(
            timeout(Duration::from_millis(300), &mut waking)
                .await
                .is_err()
        );
        assert!(companion.asleep());
        release.notify_one();
        assert_eq!(polling.await.unwrap().unwrap(), Polled::Reached);
        waking.await.unwrap().unwrap();
        assert!(!companion.asleep());
    }
}
