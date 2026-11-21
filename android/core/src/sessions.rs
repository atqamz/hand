use std::fs;
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};

use crate::alerts::{COOKIE_PREFIX, CookieSource, Held, clean, paired_cookies};
use crate::key::{CipherError, KeyCipher, create_dir, create_private, sync_dir};
use crate::legacy;
use crate::link::short_id;

const DIR: &str = "sessions";
const FILE: &str = "sessions.sealed";
const BAD: &str = "sessions.sealed.bad";
const STAGED: &str = "sessions.sealed.tmp";
const NAME_MAX: usize = 64;

pub trait Jar: Send + Sync {
    fn cookies(&self) -> io::Result<Option<String>>;
    fn clear(&self) -> io::Result<()>;
}

#[derive(Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
struct Data {
    #[serde(rename = "laptops")]
    hosts: Vec<Host>,
}

#[derive(Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
struct Host {
    endpoint: String,
    relay: Option<String>,
    name: String,
    fleets: Vec<Entry>,
}

#[derive(Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
struct Entry {
    id: String,
    name: String,
    device: String,
    secret: String,
    paired: u64,
}

pub struct Paired {
    pub id: String,
    pub name: String,
    pub value: String,
}

pub struct Listed {
    pub id: String,
    pub name: String,
    pub paired: u64,
    pub host: String,
}

pub struct HostInfo {
    pub endpoint: String,
    pub short: String,
    pub relay: Option<String>,
    pub name: String,
    pub fleets: Vec<Listed>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Refusal {
    Held,
    NotSaved,
}

pub struct Sessions<J> {
    root: PathBuf,
    jar: J,
    cipher: Arc<dyn KeyCipher>,
    data: Mutex<Data>,
    quarantined: bool,
    pending_import: AtomicBool,
    unsaved: AtomicBool,
    unfinished: AtomicBool,
}

impl<J: Jar> Sessions<J> {
    pub fn open(root: &Path, jar: J, cipher: Arc<dyn KeyCipher>) -> io::Result<Self> {
        let dir = root.join(DIR);
        create_dir(&dir)?;
        let (data, quarantined, stored) = read(&dir, cipher.as_ref())?;
        let sessions = Self {
            root: root.to_owned(),
            jar,
            cipher,
            data: Mutex::new(data),
            quarantined,
            pending_import: AtomicBool::new(!stored),
            unsaved: AtomicBool::new(false),
            unfinished: AtomicBool::new(false),
        };
        if stored {
            sessions.adopt_legacy();
        }
        Ok(sessions)
    }

    pub fn quarantined(&self) -> bool {
        self.quarantined
    }

    pub fn unsaved(&self) -> bool {
        self.unsaved.load(Ordering::SeqCst) || self.unfinished.load(Ordering::SeqCst)
    }

    pub fn mark_unsaved(&self) {
        self.unfinished.store(true, Ordering::SeqCst);
    }

    fn adopt_legacy(&self) {
        let Some((endpoint, relay)) = legacy::read_host(&self.root) else {
            return;
        };
        let mut data = self.data.lock().unwrap();
        let mut next = data.clone();
        if let Some(host) = next.hosts.iter_mut().find(|host| host.endpoint == endpoint)
            && host.relay.is_none()
        {
            host.relay = relay;
        }
        if next == *data || self.commit(&mut data, next).is_ok() {
            let _ = legacy::remove(&self.root);
        }
    }

    fn import(&self, data: &mut Data) -> io::Result<()> {
        if !self.pending_import.swap(false, Ordering::SeqCst) {
            return Ok(());
        }
        let cookies = match self.jar.cookies() {
            Ok(cookies) => cookies,
            Err(error) => {
                self.pending_import.store(true, Ordering::SeqCst);
                return Err(error);
            }
        };
        let mut next = Data::default();
        import(
            &mut next,
            legacy::read_host(&self.root),
            &paired_cookies(&cookies.unwrap_or_default()),
        );
        *data = next.clone();
        if self.commit(data, next).is_ok() {
            let _ = legacy::remove(&self.root);
        }
        Ok(())
    }

    fn read(&self) -> Data {
        let mut data = self.data.lock().unwrap();
        let _ = self.import(&mut data);
        data.clone()
    }

    pub fn hosts(&self) -> Vec<HostInfo> {
        let data = self.read();
        data.hosts
            .iter()
            .map(|host| {
                let shared = data
                    .hosts
                    .iter()
                    .filter(|other| !host.name.is_empty() && other.name == host.name)
                    .count()
                    > 1;
                let short = short_id(&host.endpoint);
                HostInfo {
                    endpoint: host.endpoint.clone(),
                    short: short.clone(),
                    relay: host.relay.clone(),
                    name: match (host.name.is_empty(), shared) {
                        (true, _) => short,
                        (false, true) => format!("{} ({short})", host.name),
                        (false, false) => host.name.clone(),
                    },
                    fleets: host.listed(),
                }
            })
            .collect()
    }

    pub fn name_clashes(&self, endpoint: &str, claim: &str) -> bool {
        self.read()
            .hosts
            .iter()
            .any(|host| host.endpoint != endpoint && host.name == claim)
    }

    pub fn listed(&self) -> Vec<Listed> {
        self.read().hosts.iter().flat_map(Host::listed).collect()
    }

    pub fn held(&self) -> Vec<Held> {
        self.read()
            .hosts
            .iter()
            .flat_map(|host| {
                host.fleets.iter().map(|entry| Held {
                    host: host.endpoint.clone(),
                    fleet: entry.id.clone(),
                    value: format!("{}.{}", entry.device, entry.secret),
                })
            })
            .collect()
    }

    pub fn route_of(&self, fleet: &str, cookie: &str) -> Option<(String, Option<String>)> {
        self.read()
            .hosts
            .into_iter()
            .find(|host| {
                host.fleets.iter().any(|entry| {
                    entry.id == fleet
                        && cookie
                            == format!("{COOKIE_PREFIX}{fleet}={}.{}", entry.device, entry.secret)
                })
            })
            .map(|host| (host.endpoint, host.relay))
    }

    fn endpoint_of(&self, fleet: &str) -> Option<String> {
        self.read()
            .hosts
            .into_iter()
            .find(|host| host.fleets.iter().any(|entry| entry.id == fleet))
            .map(|host| host.endpoint)
    }

    pub fn held_elsewhere(&self, fleet: &str, endpoint: &str) -> bool {
        self.read()
            .hosts
            .iter()
            .any(|host| host.endpoint != endpoint && host.fleets.iter().any(|e| e.id == fleet))
    }

    pub fn header_for(&self, endpoint: &str) -> Option<String> {
        let header = self
            .read()
            .hosts
            .iter()
            .filter(|host| host.endpoint == endpoint)
            .flat_map(|host| &host.fleets)
            .map(|entry| {
                format!(
                    "{COOKIE_PREFIX}{}={}.{}",
                    entry.id, entry.device, entry.secret
                )
            })
            .collect::<Vec<_>>()
            .join("; ");
        (!header.is_empty()).then_some(header)
    }

    pub fn cookie(&self, fleet: &str) -> Option<String> {
        let endpoint = self.endpoint_of(fleet)?;
        let prefix = format!("{COOKIE_PREFIX}{fleet}=");
        self.header_for(&endpoint)?
            .split("; ")
            .find(|cookie| cookie.starts_with(&prefix))
            .map(str::to_owned)
    }

    pub fn name_host(&self, endpoint: &str, meta: &[u8]) {
        let Some(name) = host_name(meta) else {
            return;
        };
        let mut data = self.data.lock().unwrap();
        let _ = self.import(&mut data);
        let mut next = data.clone();
        if let Some(host) = next.hosts.iter_mut().find(|host| host.endpoint == endpoint)
            && host.name != name
        {
            host.name = name;
            let _ = self.commit(&mut data, next);
        }
    }

    pub fn clear_jar(&self) -> io::Result<()> {
        self.jar.clear()
    }

    pub fn forget(&self) -> io::Result<()> {
        let mut data = self.data.lock().unwrap();
        self.jar.clear()?;
        self.pending_import.store(false, Ordering::SeqCst);
        self.unfinished.store(false, Ordering::SeqCst);
        let _ = legacy::remove(&self.root);
        let saved = self.commit(&mut data, Data::default());
        if saved.is_err() {
            *data = Data::default();
        }
        saved
    }

    pub fn forget_host(&self, endpoint: &str) -> io::Result<()> {
        let mut data = self.data.lock().unwrap();
        self.import(&mut data)?;
        self.jar.clear()?;
        let mut next = data.clone();
        next.hosts.retain(|host| host.endpoint != endpoint);
        let saved = self.commit(&mut data, next.clone());
        if saved.is_err() {
            *data = next;
        }
        saved
    }

    pub fn pair(
        &self,
        endpoint: &str,
        relay: Option<&str>,
        fleets: &[Paired],
    ) -> Result<(), Refusal> {
        let mut data = self.data.lock().unwrap();
        self.import(&mut data).map_err(|_| Refusal::NotSaved)?;
        if data.hosts.iter().any(|host| {
            host.endpoint != endpoint
                && host
                    .fleets
                    .iter()
                    .any(|entry| fleets.iter().any(|paired| paired.id == entry.id))
        }) {
            return Err(Refusal::Held);
        }
        let mut next = data.clone();
        let at = match next.hosts.iter().position(|host| host.endpoint == endpoint) {
            Some(at) => at,
            None => {
                next.hosts.push(Host {
                    endpoint: endpoint.to_owned(),
                    ..Host::default()
                });
                next.hosts.len() - 1
            }
        };
        let host = &mut next.hosts[at];
        host.relay = relay.map(str::to_owned);
        host.fleets
            .retain(|entry| !fleets.iter().any(|paired| paired.id == entry.id));
        for paired in fleets {
            let (device, secret) = paired.value.split_once('.').unwrap_or_default();
            host.fleets.push(Entry {
                device: device.to_owned(),
                secret: secret.to_owned(),
                id: paired.id.clone(),
                name: paired.name.clone(),
                paired: now(),
            });
        }
        if self.commit(&mut data, next.clone()).is_err() && *data != next {
            return Err(Refusal::NotSaved);
        }
        self.unfinished.store(false, Ordering::SeqCst);
        drop(data);
        if self.jar.clear().is_err() {
            self.mark_unsaved();
        }
        Ok(())
    }

    fn commit(&self, data: &mut Data, next: Data) -> io::Result<()> {
        let saved = (|| {
            let staged = self.stage(&next)?;
            let dir = self.root.join(DIR);
            if let Err(error) = fs::rename(&staged, dir.join(FILE)) {
                let _ = fs::remove_file(&staged);
                return Err(error);
            }
            *data = next;
            sync_dir(&dir)
        })();
        self.unsaved.store(saved.is_err(), Ordering::SeqCst);
        saved
    }

    fn stage(&self, data: &Data) -> io::Result<PathBuf> {
        let sealed = self
            .cipher
            .seal(&serde_json::to_vec(data).map_err(io::Error::other)?)
            .map_err(|error| match error {
                CipherError::Io(error) => error,
                other => io::Error::other(other.to_string()),
            })?;
        let staged = self.root.join(DIR).join(STAGED);
        let _ = fs::remove_file(&staged);
        let written = create_private(&staged).and_then(|mut file| {
            file.write_all(&sealed)?;
            file.sync_all()
        });
        match written {
            Ok(()) => Ok(staged),
            Err(error) => {
                let _ = fs::remove_file(&staged);
                Err(error)
            }
        }
    }
}

impl Host {
    fn listed(&self) -> Vec<Listed> {
        self.fleets
            .iter()
            .map(|entry| Listed {
                id: entry.id.clone(),
                name: entry.name.clone(),
                paired: entry.paired,
                host: self.endpoint.clone(),
            })
            .collect()
    }
}

impl<J: Jar + 'static> CookieSource for Arc<Sessions<J>> {
    async fn held(&self) -> io::Result<Vec<Held>> {
        Ok(Sessions::held(self))
    }

    async fn cookie(&self, fleet: &str) -> io::Result<Option<String>> {
        Ok(Sessions::cookie(self, fleet))
    }
}

fn host_name(meta: &[u8]) -> Option<String> {
    let value: serde_json::Value = serde_json::from_slice(meta).ok()?;
    let name = clean(value.get("host")?.as_str()?, NAME_MAX);
    let name = name.trim();
    (!name.is_empty()).then(|| name.to_owned())
}

fn read(dir: &Path, cipher: &dyn KeyCipher) -> io::Result<(Data, bool, bool)> {
    let path = dir.join(FILE);
    let sealed = match fs::read(&path) {
        Ok(sealed) => sealed,
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            return Ok((Data::default(), false, false));
        }
        Err(error) => return Err(error),
    };
    let plaintext = match cipher.open(&sealed) {
        Ok(plaintext) => plaintext,
        Err(CipherError::KeyReplaced) => {
            fs::remove_file(&path)?;
            sync_dir(dir)?;
            return Ok((Data::default(), false, false));
        }
        Err(CipherError::Corrupt(_)) => return quarantine(dir, &path),
        Err(CipherError::Io(error)) => return Err(error),
    };
    match serde_json::from_slice(&plaintext) {
        Ok(data) => Ok((data, false, true)),
        Err(_) => quarantine(dir, &path),
    }
}

fn quarantine(dir: &Path, path: &Path) -> io::Result<(Data, bool, bool)> {
    fs::rename(path, dir.join(BAD))?;
    sync_dir(dir)?;
    Ok((Data::default(), true, false))
}

fn import(data: &mut Data, host: Option<(String, Option<String>)>, jar: &[(String, String)]) {
    let Some((endpoint, relay)) = host else {
        return;
    };
    let mut fleets = Vec::new();
    for (id, value) in jar {
        let Some((device, secret)) = shaped(value) else {
            continue;
        };
        fleets.push(Entry {
            id: id.clone(),
            name: String::new(),
            device: device.to_owned(),
            secret: secret.to_owned(),
            paired: 0,
        });
    }
    data.hosts.push(Host {
        endpoint,
        relay,
        name: String::new(),
        fleets,
    });
}

fn shaped(value: &str) -> Option<(&str, &str)> {
    let (device, secret) = value.split_once('.')?;
    (!device.is_empty()
        && device.bytes().all(|byte| byte.is_ascii_digit())
        && secret.len() == 64
        && secret.bytes().all(|byte| byte.is_ascii_hexdigit()))
    .then_some((device, secret))
}

fn now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |elapsed| elapsed.as_secs())
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::{AtomicBool, Ordering};

    use super::*;
    use crate::testutil::{FakeCipher, TempDir};

    const RELAY: &str = "https://relay.example.test:7842/";
    const HOST: &str = "4d92c10d0b7e3f5a9c1e6b2d8f4a7c3e5b9d1f6a2c8e4b7d3f5a9c1e6b2d6ed6";
    const FLEET: &str = "f0123456789ab";
    const OTHER: &str = "f999999999999";
    const A: &str = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    const B: &str = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
    const C: &str = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";
    const COOKIE: &str = "hand_phone_f0123456789ab=7.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    const NEWER: &str = "hand_phone_f0123456789ab=8.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
    const SECOND: &str = "hand_phone_f999999999999=9.cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";
    const BOTH: &str = "hand_phone_f0123456789ab=7.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; hand_phone_f999999999999=9.cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";

    #[derive(Clone, Default)]
    struct FakeJar {
        cookies: Arc<Mutex<Vec<String>>>,
        fail_read: Arc<AtomicBool>,
        fail_clear: Arc<AtomicBool>,
    }

    impl FakeJar {
        fn hold(&self, cookies: &[&str]) {
            *self.cookies.lock().unwrap() = cookies.iter().map(|c| (*c).to_owned()).collect();
        }
    }

    impl Jar for FakeJar {
        fn cookies(&self) -> io::Result<Option<String>> {
            if self.fail_read.load(Ordering::SeqCst) {
                return Err(io::Error::other("the jar is unreadable"));
            }
            let cookies = self.cookies.lock().unwrap().clone();
            Ok((!cookies.is_empty()).then(|| cookies.join("; ")))
        }

        fn clear(&self) -> io::Result<()> {
            if self.fail_clear.load(Ordering::SeqCst) {
                return Err(io::Error::other("the jar refused"));
            }
            self.cookies.lock().unwrap().clear();
            Ok(())
        }
    }

    struct Setup {
        dir: TempDir,
        cipher: Arc<FakeCipher>,
        jar: FakeJar,
    }

    impl Setup {
        fn new(jar: &[&str]) -> Self {
            let dir = TempDir::new();
            fs::write(dir.path().join("laptop"), format!("{HOST}\n")).unwrap();
            let setup = Self {
                dir,
                cipher: Arc::new(FakeCipher::new()),
                jar: FakeJar::default(),
            };
            setup.jar.hold(jar);
            setup
        }

        fn open(&self) -> io::Result<Sessions<FakeJar>> {
            Sessions::open(self.dir.path(), self.jar.clone(), self.cipher.clone())
        }

        fn file(&self) -> PathBuf {
            self.dir.path().join(DIR).join(FILE)
        }

        fn bad(&self) -> PathBuf {
            self.dir.path().join(DIR).join(BAD)
        }
    }

    #[test]
    fn a_sealed_store_round_trips_and_holds_no_plaintext() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();
        assert_eq!(all(&sessions).as_deref(), Some(BOTH));
        sessions.data.lock().unwrap().hosts[0].fleets[0].name = "hand".to_owned();
        let next = sessions.data.lock().unwrap().clone();
        sessions.commit(&mut Data::default(), next).unwrap();

        let sealed = fs::read(setup.file()).unwrap();
        assert!(!sealed.windows(4).any(|window| window == A.as_bytes()));
        setup.jar.hold(&[]);
        let reopened = setup.open().unwrap();
        assert!(!reopened.quarantined());
        assert_eq!(all(&reopened).as_deref(), Some(BOTH));
        let data = reopened.data.lock().unwrap();
        assert_eq!(data.hosts[0].fleets[0].name, "hand");
        assert_eq!(data.hosts[0].endpoint, HOST);
    }

    #[test]
    fn an_unreadable_store_is_quarantined_and_the_pairings_come_back_from_the_jar() {
        let setup = Setup::new(&[COOKIE]);
        all(&setup.open().unwrap());
        let sealed = fs::read(setup.file()).unwrap();

        let mut tampered = sealed.clone();
        tampered[sealed.len() / 2] ^= 1;
        let truncated = sealed[..sealed.len() - 3].to_vec();
        let unparseable = setup
            .cipher
            .seal(b"{\"laptops\": [\"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
            .unwrap();
        for damaged in [tampered, truncated, b"short".to_vec(), unparseable] {
            fs::write(setup.file(), &damaged).unwrap();
            fs::write(setup.dir.path().join("laptop"), format!("{HOST}\n")).unwrap();

            let sessions = setup.open().unwrap();

            assert!(sessions.quarantined());
            assert_eq!(fs::read(setup.bad()).unwrap(), damaged);
            assert_eq!(all(&sessions).as_deref(), Some(COOKIE));
            assert!(setup.file().exists());
            assert!(!setup.open().unwrap().quarantined());
        }
    }

    #[test]
    fn a_keystore_error_that_is_not_corruption_fails_and_leaves_the_store_alone() {
        let setup = Setup::new(&[COOKIE]);
        all(&setup.open().unwrap());
        let before = fs::read(setup.file()).unwrap();
        setup.cipher.fail_next_open(io::ErrorKind::PermissionDenied);

        let error = setup.open().err().unwrap();

        assert_eq!(error.kind(), io::ErrorKind::PermissionDenied);
        assert_eq!(fs::read(setup.file()).unwrap(), before);
        assert!(!setup.bad().exists());
        assert_eq!(all(&setup.open().unwrap()).as_deref(), Some(COOKIE));
    }

    #[test]
    fn a_lost_keystore_key_drops_the_old_blob_so_the_next_open_is_clean() {
        let setup = Setup::new(&[]);
        fs::remove_file(setup.dir.path().join("laptop")).unwrap();
        let sealed = setup.cipher.seal(b"{}").unwrap();
        fs::create_dir_all(setup.file().parent().unwrap()).unwrap();
        fs::write(setup.file(), sealed).unwrap();
        setup.cipher.forget_key();

        let sessions = setup.open().unwrap();
        setup.cipher.seal(b"x").unwrap();
        assert_eq!(all(&sessions), None);

        assert!(!sessions.quarantined());
        let second = setup.open().unwrap();
        assert!(!second.quarantined());
        assert!(!setup.bad().exists());
    }

    #[test]
    fn the_host_file_is_imported_once_and_the_store_keeps_it() {
        let setup = Setup::new(&[]);
        let sessions = setup.open().unwrap();
        assert_eq!(all(&sessions), None);
        assert!(setup.file().exists());
        assert!(!setup.dir.path().join("laptop").exists());

        let reopened = setup.open().unwrap();
        assert_eq!(reopened.data.lock().unwrap().hosts[0].endpoint, HOST);
    }

    fn old_store(setup: &Setup) {
        let store = format!(
            r#"{{"laptops":[{{"endpoint":"{HOST}","fleets":[{{"id":"{FLEET}","name":"hand","device":"7","secret":"{A}","paired":5}}]}}]}}"#
        );
        fs::create_dir_all(setup.file().parent().unwrap()).unwrap();
        fs::write(setup.file(), setup.cipher.seal(store.as_bytes()).unwrap()).unwrap();
    }

    fn relay_files(setup: &Setup, host: &str) {
        fs::write(setup.dir.path().join("laptop"), format!("{host}\t\n")).unwrap();
        fs::write(
            setup.dir.path().join("laptop-relay"),
            format!("{host}\n{RELAY}\n"),
        )
        .unwrap();
    }

    fn files_gone(setup: &Setup) -> bool {
        !setup.dir.path().join("laptop").exists() && !setup.dir.path().join("laptop-relay").exists()
    }

    #[test]
    fn a_single_host_upgrade_moves_the_laptop_relay_into_the_store_and_deletes_the_files() {
        let setup = Setup::new(&[]);
        old_store(&setup);
        relay_files(&setup, HOST);

        let sessions = setup.open().unwrap();

        let hosts = sessions.hosts();
        assert_eq!(hosts.len(), 1);
        assert_eq!(hosts[0].endpoint, HOST);
        assert_eq!(hosts[0].relay.as_deref(), Some(RELAY));
        assert_eq!(sessions.cookie(FLEET).as_deref(), Some(COOKIE));
        assert!(files_gone(&setup));
        assert_eq!(
            setup.open().unwrap().hosts()[0].relay.as_deref(),
            Some(RELAY)
        );
    }

    #[test]
    fn the_laptop_files_never_replace_a_relay_the_store_holds_and_go_anyway() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions
            .pair(
                HOST,
                Some("https://other.example.test:7842/"),
                &[paired(FLEET, "7", A)],
            )
            .unwrap();
        relay_files(&setup, HOST);

        let reopened = setup.open().unwrap();

        assert_eq!(
            reopened.hosts()[0].relay.as_deref(),
            Some("https://other.example.test:7842/")
        );
        assert!(files_gone(&setup));
    }

    #[test]
    fn laptop_files_for_a_host_the_store_lacks_are_deleted_and_add_no_host() {
        let setup = Setup::new(&[]);
        old_store(&setup);
        relay_files(&setup, NEW_HOST);

        let sessions = setup.open().unwrap();

        assert_eq!(sessions.hosts().len(), 1);
        assert_eq!(sessions.hosts()[0].endpoint, HOST);
        assert!(files_gone(&setup));
    }

    #[test]
    fn laptop_files_stay_when_the_store_cannot_be_written_and_are_adopted_next_start() {
        let setup = Setup::new(&[]);
        old_store(&setup);
        relay_files(&setup, HOST);

        setup.cipher.fail_next_seal();
        let sessions = setup.open().unwrap();
        assert!(!files_gone(&setup));
        assert_eq!(sessions.hosts()[0].relay, None);

        let again = setup.open().unwrap();
        assert!(files_gone(&setup));
        assert_eq!(again.hosts()[0].relay.as_deref(), Some(RELAY));
    }

    #[test]
    fn a_first_import_keeps_the_laptop_relay_and_deletes_the_files() {
        let setup = Setup::new(&[COOKIE]);
        relay_files(&setup, HOST);

        let sessions = setup.open().unwrap();

        assert_eq!(sessions.hosts()[0].relay.as_deref(), Some(RELAY));
        assert_eq!(sessions.cookie(FLEET).as_deref(), Some(COOKIE));
        assert!(files_gone(&setup));
    }

    #[test]
    fn forget_removes_the_laptop_files_a_failed_adoption_left() {
        let setup = Setup::new(&[]);
        old_store(&setup);
        relay_files(&setup, HOST);
        setup.cipher.fail_next_seal();
        let sessions = setup.open().unwrap();
        assert!(!files_gone(&setup));

        sessions.forget().unwrap();

        assert!(files_gone(&setup));
    }

    #[test]
    fn the_jar_is_read_once_when_the_store_is_missing() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        assert_eq!(all(&sessions).as_deref(), Some(COOKIE));

        setup.jar.hold(&[NEWER, SECOND]);
        assert_eq!(all(&sessions).as_deref(), Some(COOKIE));
        assert_eq!(all(&setup.open().unwrap()).as_deref(), Some(COOKIE));

        for unshaped in [
            format!("hand_phone_{FLEET}=x.{A}"),
            format!("hand_phone_{FLEET}=8.short"),
            format!("hand_phone_{FLEET}=8"),
            format!("hand_phone_{FLEET}=.{A}"),
        ] {
            let setup = Setup::new(&[unshaped.as_str()]);
            let sessions = setup.open().unwrap();
            assert_eq!(all(&sessions), None, "{unshaped}");
            assert_eq!(all(&setup.open().unwrap()), None, "{unshaped}");
        }
    }

    #[test]
    fn a_fresh_install_never_imports_a_cookie_the_jar_gains_later() {
        let setup = Setup::new(&[]);
        let sessions = setup.open().unwrap();
        assert_eq!(all(&sessions), None);

        setup.jar.hold(&[COOKIE]);

        assert_eq!(all(&sessions), None);
        assert_eq!(all(&setup.open().unwrap()), None);
    }

    #[test]
    fn an_unreadable_jar_leaves_the_store_unwritten_until_the_next_read() {
        let setup = Setup::new(&[COOKIE]);
        setup.jar.fail_read.store(true, Ordering::SeqCst);
        let sessions = setup.open().unwrap();

        assert_eq!(all(&sessions), None);
        assert!(!setup.file().exists());

        setup.jar.fail_read.store(false, Ordering::SeqCst);
        assert_eq!(all(&sessions).as_deref(), Some(COOKIE));
        assert!(setup.file().exists());
        assert_eq!(all(&setup.open().unwrap()).as_deref(), Some(COOKIE));
    }

    #[test]
    fn a_failed_jar_read_is_retried_and_a_pairing_keeps_the_legacy_fleets() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        setup.jar.fail_read.store(true, Ordering::SeqCst);

        assert_eq!(all(&sessions), None);
        assert!(!setup.file().exists());

        setup.jar.fail_read.store(false, Ordering::SeqCst);
        sessions.pair(HOST, None, &[paired(OTHER, "9", C)]).unwrap();

        let expected = vec![
            format!("hand_phone_{FLEET}=7.{A}"),
            format!("hand_phone_{OTHER}=9.{C}"),
        ];
        assert_eq!(header_of(&sessions), expected);
        assert_eq!(header_of(&setup.open().unwrap()), expected);
        assert!(setup.jar.cookies().unwrap().is_none());
    }

    #[test]
    fn a_pairing_that_cannot_read_the_jar_fails_before_writing_or_clearing() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        setup.jar.fail_read.store(true, Ordering::SeqCst);

        let result = sessions.pair(HOST, None, &[paired(OTHER, "9", C)]);

        assert!(result.is_err());
        assert!(!setup.file().exists());
        assert!(!setup.dir.path().join(DIR).join(STAGED).exists());
        setup.jar.fail_read.store(false, Ordering::SeqCst);
        assert_eq!(setup.jar.cookies().unwrap().as_deref(), Some(COOKIE));
    }

    #[test]
    fn forget_is_not_undone_by_a_restart() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();

        sessions.forget().unwrap();

        assert!(setup.jar.cookies().unwrap().is_none());
        assert_eq!(all(&sessions), None);
        assert_eq!(all(&setup.open().unwrap()), None);
    }

    #[test]
    fn a_jar_that_cannot_be_cleared_keeps_the_pairings_and_forget_says_so() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        setup.jar.fail_clear.store(true, Ordering::SeqCst);

        assert!(sessions.forget().is_err());

        assert_eq!(all(&sessions).as_deref(), Some(COOKIE));
        assert_eq!(all(&setup.open().unwrap()).as_deref(), Some(COOKIE));
    }

    #[test]
    fn a_crash_between_the_write_and_the_rename_leaves_the_old_file() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        all(&sessions);
        let before = fs::read(setup.file()).unwrap();

        let mut next = sessions.data.lock().unwrap().clone();
        next.hosts[0].fleets.clear();
        sessions.stage(&next).unwrap();
        drop(sessions);

        assert_eq!(fs::read(setup.file()).unwrap(), before);
        setup.jar.hold(&[]);
        assert_eq!(all(&setup.open().unwrap()).as_deref(), Some(COOKIE));
    }

    #[test]
    fn a_failed_import_write_is_reported_and_the_next_start_writes_it() {
        let setup = Setup::new(&[COOKIE]);
        setup.cipher.fail_next_seal();

        let sessions = setup.open().unwrap();
        assert!(!sessions.unsaved());
        assert_eq!(all(&sessions).as_deref(), Some(COOKIE));

        assert!(sessions.unsaved());
        assert!(!setup.file().exists());

        let reopened = setup.open().unwrap();
        assert!(!reopened.unsaved());
        assert_eq!(all(&reopened).as_deref(), Some(COOKIE));
        assert!(setup.file().exists());
    }

    #[cfg(unix)]
    #[test]
    fn the_memory_follows_the_rename_even_when_the_folder_sync_fails() {
        use std::os::unix::fs::PermissionsExt;
        let setup = Setup::new(&[]);
        let sessions = setup.open().unwrap();
        let dir = setup.dir.path().join(DIR);
        let mut next = Data::default();
        next.hosts.push(Host {
            endpoint: HOST.to_owned(),
            ..Host::default()
        });
        let mut data = Data::default();

        fs::set_permissions(&dir, fs::Permissions::from_mode(0o300)).unwrap();
        let committed = sessions.commit(&mut data, next.clone());
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o700)).unwrap();

        assert!(committed.is_err());
        assert!(data == next);
        assert!(setup.file().exists());
    }

    #[test]
    fn a_store_missing_fields_still_opens() {
        let setup = Setup::new(&[]);
        let plain = format!(
            r#"{{"laptops":[{{"endpoint":"{HOST}","fleets":[{{"id":"{FLEET}","device":"7","secret":"{A}"}}]}}]}}"#
        );
        fs::create_dir_all(setup.file().parent().unwrap()).unwrap();
        fs::write(setup.file(), setup.cipher.seal(plain.as_bytes()).unwrap()).unwrap();

        let sessions = setup.open().unwrap();

        assert!(!sessions.quarantined());
        assert_eq!(all(&sessions).as_deref(), Some(COOKIE));
    }

    #[test]
    fn a_store_sealed_under_the_laptops_key_keeps_its_pairing_and_is_written_under_it() {
        let setup = Setup::new(&[]);
        let old = format!(
            r#"{{"laptops":[{{"endpoint":"{HOST}","fleets":[{{"id":"{FLEET}","name":"hand","device":"7","secret":"{A}"}}]}}]}}"#
        );
        fs::create_dir_all(setup.file().parent().unwrap()).unwrap();
        fs::write(setup.file(), setup.cipher.seal(old.as_bytes()).unwrap()).unwrap();

        let sessions = setup.open().unwrap();
        assert_eq!(all(&sessions).as_deref(), Some(COOKIE));
        sessions.pair(HOST, None, &[paired(OTHER, "9", C)]).unwrap();

        let plain = setup.cipher.open(&fs::read(setup.file()).unwrap()).unwrap();
        let text = String::from_utf8(plain).unwrap();
        assert!(text.starts_with(r#"{"laptops":["#), "{text}");
        assert!(!text.contains("\"hosts\""), "{text}");
        assert_eq!(all(&setup.open().unwrap()).as_deref(), Some(BOTH));
    }

    const NEW_HOST: &str = "d6e458a66697094c1bc7b54cb83fea66a8db57c49f37514944579712cd7e43c5";

    fn paired(id: &str, device: &str, secret: &str) -> Paired {
        Paired {
            id: id.to_owned(),
            name: format!("name of {id}"),
            value: format!("{device}.{secret}"),
        }
    }

    fn all(sessions: &Sessions<FakeJar>) -> Option<String> {
        let header = sessions
            .hosts()
            .iter()
            .filter_map(|host| sessions.header_for(&host.endpoint))
            .collect::<Vec<_>>()
            .join("; ");
        (!header.is_empty()).then_some(header)
    }

    fn header_of(sessions: &Sessions<FakeJar>) -> Vec<String> {
        let mut parts: Vec<String> = all(sessions)
            .unwrap_or_default()
            .split("; ")
            .map(str::to_owned)
            .collect();
        parts.sort();
        parts
    }

    #[test]
    fn one_fleets_cookie_comes_from_the_store_and_no_other() {
        let setup = Setup::new(&[]);
        let sessions = setup.open().unwrap();
        sessions
            .pair(HOST, None, &[paired(FLEET, "7", A), paired(OTHER, "9", C)])
            .unwrap();

        assert_eq!(sessions.cookie(FLEET).as_deref(), Some(COOKIE));
        assert_eq!(sessions.cookie(OTHER).as_deref(), Some(SECOND));
        assert_eq!(sessions.cookie("f111111111111"), None);
        assert_eq!(sessions.cookie("f012345678"), None);
        sessions.forget().unwrap();
        assert_eq!(sessions.cookie(FLEET), None);
    }

    #[test]
    fn the_listing_gives_a_paired_fleet_its_time_and_an_imported_one_none() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();

        let imported = sessions.listed();

        assert_eq!(imported.len(), 1);
        assert_eq!(imported[0].id, FLEET);
        assert_eq!(imported[0].paired, 0);
        sessions.pair(HOST, None, &[paired(OTHER, "9", C)]).unwrap();
        let named = setup.open().unwrap().listed();
        assert_eq!(
            named
                .iter()
                .map(|fleet| fleet.id.as_str())
                .collect::<Vec<_>>(),
            [FLEET, OTHER]
        );
        assert_eq!(named[1].name, format!("name of {OTHER}"));
        assert!(named[1].paired > 0);
        assert_eq!(named[0].paired, imported[0].paired);
        sessions.forget().unwrap();
        assert!(sessions.listed().is_empty());
    }

    #[test]
    fn a_pairing_adds_every_fleet_adopts_the_others_and_clears_the_jar() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();

        sessions
            .pair(
                HOST,
                None,
                &[paired(OTHER, "9", C), paired("f111111111111", "5", B)],
            )
            .unwrap();

        assert!(setup.jar.cookies().unwrap().is_none());
        let expected = [
            format!("hand_phone_{FLEET}=7.{A}"),
            format!("hand_phone_{OTHER}=9.{C}"),
            "hand_phone_f111111111111=5.".to_owned() + B,
        ];
        let mut expected = expected.to_vec();
        expected.sort();
        assert_eq!(header_of(&sessions), expected);
        assert_eq!(header_of(&setup.open().unwrap()), expected);
        let data = sessions.data.lock().unwrap();
        assert_eq!(data.hosts[0].fleets[2].name, "name of f111111111111");
        assert!(!sessions.unsaved());
    }

    #[test]
    fn a_pairing_for_a_fleet_already_held_keeps_its_secret_against_the_jar() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();

        sessions.pair(HOST, None, &[paired(FLEET, "8", B)]).unwrap();
        setup.jar.hold(&[COOKIE, SECOND]);

        let expected = vec![
            format!("hand_phone_{FLEET}=8.{B}"),
            format!("hand_phone_{OTHER}=9.{C}"),
        ];
        assert_eq!(header_of(&sessions), expected);
        assert_eq!(header_of(&setup.open().unwrap()), expected);
    }

    #[test]
    fn a_pairing_that_cannot_be_saved_changes_nothing_and_can_be_tried_again() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        all(&sessions);
        let before = fs::read(setup.file()).unwrap();

        setup.cipher.fail_next_seal();
        let failed = sessions.pair(HOST, None, &[paired(OTHER, "9", C)]);

        assert!(failed.is_err());
        assert!(sessions.unsaved());
        assert_eq!(fs::read(setup.file()).unwrap(), before);
        assert_eq!(header_of(&sessions), [COOKIE.to_owned()]);
        assert!(setup.jar.cookies().unwrap().is_some());
        assert!(!setup.dir.path().join(DIR).join(STAGED).exists());

        sessions.pair(HOST, None, &[paired(OTHER, "9", C)]).unwrap();
        assert!(!sessions.unsaved());
        assert_eq!(header_of(&sessions).len(), 2);
    }

    #[test]
    fn a_jar_that_cannot_be_cleared_after_the_write_leaves_the_pairing_made_and_says_so() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();
        setup.jar.fail_clear.store(true, Ordering::SeqCst);

        sessions
            .pair(NEW_HOST, None, &[paired("f111111111111", "5", B)])
            .unwrap();

        assert!(sessions.unsaved());
        let made = |sessions: &Sessions<FakeJar>| {
            header_of(sessions)
                .into_iter()
                .filter(|part| part.contains("f111111111111"))
                .count()
        };
        assert_eq!(made(&sessions), 1);
        assert_eq!(made(&setup.open().unwrap()), 1);
    }

    #[test]
    fn a_stale_cookie_left_by_a_failed_clear_stays_unfinished_and_never_becomes_a_ghost_fleet() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();
        setup.jar.fail_clear.store(true, Ordering::SeqCst);
        sessions
            .pair(NEW_HOST, None, &[paired("f111111111111", "5", B)])
            .unwrap();
        let mut held = vec![
            COOKIE.to_owned(),
            SECOND.to_owned(),
            "hand_phone_f111111111111=5.".to_owned() + B,
        ];
        held.sort();

        assert!(sessions.unsaved());
        assert_eq!(header_of(&sessions), held);
        assert!(sessions.unsaved());
        assert_eq!(header_of(&setup.open().unwrap()), held);

        setup.jar.fail_clear.store(false, Ordering::SeqCst);
        setup.jar.hold(&[]);
        assert_eq!(header_of(&sessions), held);
        assert!(sessions.unsaved());
        sessions
            .pair(NEW_HOST, None, &[paired("f222222222222", "6", C)])
            .unwrap();
        assert!(!sessions.unsaved());
    }

    #[test]
    fn forget_ends_an_unfinished_pairing() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        setup.jar.fail_clear.store(true, Ordering::SeqCst);
        sessions.pair(HOST, None, &[paired(OTHER, "9", C)]).unwrap();
        assert!(sessions.unsaved());
        setup.jar.fail_clear.store(false, Ordering::SeqCst);

        sessions.forget().unwrap();

        assert!(!sessions.unsaved());
    }

    #[cfg(unix)]
    #[test]
    fn a_folder_that_cannot_be_synced_after_the_rename_still_makes_the_pairing_and_says_so() {
        use std::os::unix::fs::PermissionsExt;
        let setup = Setup::new(&[]);
        let sessions = setup.open().unwrap();
        let dir = setup.dir.path().join(DIR);

        fs::set_permissions(&dir, fs::Permissions::from_mode(0o300)).unwrap();
        let done = sessions.pair(HOST, None, &[paired(OTHER, "9", C)]);
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o700)).unwrap();

        assert!(done.is_ok());
        assert!(sessions.unsaved());
        assert_eq!(header_of(&sessions).len(), 1);
    }

    #[test]
    fn a_pairing_with_another_host_adds_it_and_keeps_the_first() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();

        sessions
            .pair(NEW_HOST, Some(RELAY), &[paired("f111111111111", "5", B)])
            .unwrap();

        let hosts = sessions.hosts();
        assert_eq!(hosts.len(), 2);
        assert_eq!(hosts[0].endpoint, HOST);
        assert_eq!(hosts[1].endpoint, NEW_HOST);
        assert_eq!(hosts[1].relay.as_deref(), Some(RELAY));
        let mut first = sessions
            .header_for(HOST)
            .unwrap()
            .split("; ")
            .map(str::to_owned)
            .collect::<Vec<_>>();
        first.sort();
        assert_eq!(first, [COOKIE, SECOND]);
        assert_eq!(
            sessions.header_for(NEW_HOST),
            Some("hand_phone_f111111111111=5.".to_owned() + B)
        );
        assert!(setup.jar.cookies().unwrap().is_none());
    }

    #[test]
    fn a_fleet_held_through_another_host_is_refused_and_changes_nothing() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        let before = all(&sessions);

        let refused = sessions.pair(NEW_HOST, None, &[paired(FLEET, "5", B)]);

        assert_eq!(refused, Err(Refusal::Held));
        assert!(sessions.held_elsewhere(FLEET, NEW_HOST));
        assert!(!sessions.held_elsewhere(FLEET, HOST));
        assert_eq!(all(&sessions), before);
        assert_eq!(sessions.hosts().len(), 1);
        assert_eq!(all(&setup.open().unwrap()), before);
    }

    #[test]
    fn a_pairing_that_cannot_be_saved_leaves_the_stored_hosts_alone() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        all(&sessions);

        setup.cipher.fail_next_seal();
        let failed = sessions.pair(NEW_HOST, None, &[paired("f111111111111", "5", B)]);

        assert_eq!(failed, Err(Refusal::NotSaved));
        assert_eq!(header_of(&sessions), [COOKIE.to_owned()]);
        assert!(setup.jar.cookies().unwrap().is_some());
    }

    #[test]
    fn a_cookie_routes_only_to_the_host_that_issued_it() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        let route = sessions.route_of(FLEET, COOKIE).unwrap();
        assert_eq!(route.0, HOST);
        assert!(sessions.route_of(FLEET, NEWER).is_none());
        assert!(sessions.route_of(OTHER, COOKIE).is_none());

        sessions.forget_host(HOST).unwrap();
        sessions
            .pair(NEW_HOST, None, &[paired(FLEET, "8", B)])
            .unwrap();

        assert!(sessions.route_of(FLEET, COOKIE).is_none());
        assert_eq!(sessions.route_of(FLEET, NEWER).unwrap().0, NEW_HOST);
    }

    #[test]
    fn forgetting_one_host_keeps_the_other_and_clears_the_jar() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions
            .pair(NEW_HOST, None, &[paired("f111111111111", "5", B)])
            .unwrap();
        setup.jar.hold(&[SECOND]);

        sessions.forget_host(HOST).unwrap();

        assert!(setup.jar.cookies().unwrap().is_none());
        assert_eq!(sessions.header_for(HOST), None);
        assert_eq!(sessions.cookie(FLEET), None);
        assert_eq!(
            sessions.cookie("f111111111111"),
            Some("hand_phone_f111111111111=5.".to_owned() + B)
        );
        let reopened = setup.open().unwrap();
        assert_eq!(reopened.hosts().len(), 1);
        assert_eq!(reopened.hosts()[0].endpoint, NEW_HOST);
    }

    #[test]
    fn forgetting_a_host_while_the_jar_cannot_be_cleared_keeps_it() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        all(&sessions);
        setup.jar.fail_clear.store(true, Ordering::SeqCst);

        assert!(sessions.forget_host(HOST).is_err());

        assert_eq!(header_of(&sessions), [COOKIE.to_owned()]);
    }

    #[test]
    fn a_failed_host_forget_stops_serving_that_host_and_says_so() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        all(&sessions);

        setup.cipher.fail_next_seal();
        assert!(sessions.forget_host(HOST).is_err());

        assert!(sessions.unsaved());
        assert_eq!(sessions.header_for(HOST), None);
    }

    #[test]
    fn two_hosts_with_one_name_carry_their_short_ids_and_an_unnamed_one_is_its_short_id() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions
            .pair(NEW_HOST, None, &[paired("f111111111111", "5", B)])
            .unwrap();
        let names = |sessions: &Sessions<FakeJar>| {
            sessions
                .hosts()
                .into_iter()
                .map(|host| host.name)
                .collect::<Vec<_>>()
        };
        assert_eq!(names(&sessions), ["4d92…6ed6", "d6e4…43c5"]);

        sessions.name_host(HOST, br#"{"host":"atqa-laptop"}"#);
        assert_eq!(names(&sessions), ["atqa-laptop", "d6e4…43c5"]);

        sessions.name_host(NEW_HOST, br#"{"host":"atqa-laptop"}"#);
        assert_eq!(
            names(&sessions),
            ["atqa-laptop (4d92…6ed6)", "atqa-laptop (d6e4…43c5)"]
        );
        assert_eq!(names(&setup.open().unwrap()), names(&sessions));
    }

    #[test]
    fn a_renamed_machine_updates_and_an_unusable_name_changes_nothing() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        let name = |sessions: &Sessions<FakeJar>| sessions.hosts()[0].name.clone();

        sessions.name_host(HOST, br#"{"host":"before"}"#);
        sessions.name_host(HOST, br#"{"host":"after"}"#);
        assert_eq!(name(&sessions), "after");

        for meta in [
            &br#"{"version":"1"}"#[..],
            br#"{"host":"  "}"#,
            br#"{"host":7}"#,
            b"not json",
        ] {
            sessions.name_host(HOST, meta);
            assert_eq!(name(&sessions), "after");
        }
        sessions.name_host(HOST, "{\"host\":\"a\\u0007b\\u202ec\"}".as_bytes());
        assert_eq!(name(&sessions), "a bc");
        sessions.name_host(NEW_HOST, br#"{"host":"stranger"}"#);
        assert_eq!(sessions.hosts().len(), 1);
    }

    #[test]
    fn a_failed_forget_stops_serving_the_pairings_and_says_so() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions.pair(HOST, None, &[paired(OTHER, "9", C)]).unwrap();
        assert!(all(&sessions).is_some());

        setup.cipher.fail_next_seal();
        assert!(sessions.forget().is_err());

        assert!(sessions.unsaved());
        assert_eq!(all(&sessions), None);
        sessions.forget().unwrap();
        assert!(!sessions.unsaved());
        assert_eq!(all(&setup.open().unwrap()), None);
    }
}
