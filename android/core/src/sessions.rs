use std::fs;
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};

use crate::alerts::{COOKIE_PREFIX, CookieSource, paired_cookies};
use crate::key::{CipherError, KeyCipher, create_dir, create_private, sync_dir};
use crate::pairing::read_laptop;

const DIR: &str = "sessions";
const FILE: &str = "sessions.sealed";
const BAD: &str = "sessions.sealed.bad";
const STAGED: &str = "sessions.sealed.tmp";

pub trait Jar: Send + Sync {
    fn cookies(&self) -> io::Result<Option<String>>;
    fn clear(&self) -> io::Result<()>;
}

#[derive(Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
struct Data {
    laptops: Vec<Laptop>,
}

#[derive(Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
struct Laptop {
    endpoint: String,
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
        Ok(Self {
            root: root.to_owned(),
            jar,
            cipher,
            data: Mutex::new(data),
            quarantined,
            pending_import: AtomicBool::new(!stored),
            unsaved: AtomicBool::new(false),
            unfinished: AtomicBool::new(false),
        })
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

    fn import(&self, data: &mut Data) {
        if !self.pending_import.swap(false, Ordering::SeqCst) {
            return;
        }
        let Ok(cookies) = self.jar.cookies() else {
            return;
        };
        let mut next = Data::default();
        import(
            &mut next,
            read_laptop(&self.root).0,
            &paired_cookies(&cookies.unwrap_or_default()),
        );
        *data = next.clone();
        let _ = self.commit(data, next);
    }

    pub fn header(&self) -> Option<String> {
        let mut data = self.data.lock().unwrap();
        self.import(&mut data);
        let header = data
            .laptops
            .iter()
            .flat_map(|laptop| &laptop.fleets)
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
        let prefix = format!("{COOKIE_PREFIX}{fleet}=");
        self.header()?
            .split("; ")
            .find(|cookie| cookie.starts_with(&prefix))
            .map(str::to_owned)
    }

    pub fn forget(&self) -> io::Result<()> {
        let mut data = self.data.lock().unwrap();
        self.jar.clear()?;
        self.pending_import.store(false, Ordering::SeqCst);
        self.unfinished.store(false, Ordering::SeqCst);
        let saved = self.commit(&mut data, Data::default());
        if saved.is_err() {
            *data = Data::default();
        }
        saved
    }

    pub fn pair(
        &self,
        endpoint: &str,
        fleets: &[Paired],
        replacing: impl FnOnce(),
    ) -> io::Result<()> {
        let mut data = self.data.lock().unwrap();
        self.import(&mut data);
        let mut next = data.clone();
        let replaces = next
            .laptops
            .iter()
            .any(|laptop| laptop.endpoint != endpoint && !laptop.fleets.is_empty());
        next.laptops
            .retain(|laptop| !replaces && laptop.endpoint == endpoint);
        if next.laptops.is_empty() {
            next.laptops.push(Laptop {
                endpoint: endpoint.to_owned(),
                fleets: Vec::new(),
            });
        }
        let laptop = &mut next.laptops[0];
        laptop
            .fleets
            .retain(|entry| !fleets.iter().any(|paired| paired.id == entry.id));
        for paired in fleets {
            let (device, secret) = paired.value.split_once('.').unwrap_or_default();
            laptop.fleets.push(Entry {
                device: device.to_owned(),
                secret: secret.to_owned(),
                id: paired.id.clone(),
                name: paired.name.clone(),
                paired: now(),
            });
        }
        if self.commit(&mut data, next.clone()).is_err() && *data != next {
            return Err(io::Error::other("the pairing store was not written"));
        }
        self.unfinished.store(false, Ordering::SeqCst);
        if replaces {
            replacing();
        }
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

impl<J: Jar + 'static> CookieSource for Arc<Sessions<J>> {
    async fn cookies(&self) -> io::Result<Option<String>> {
        Ok(self.header())
    }
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

fn import(data: &mut Data, laptop: Option<String>, jar: &[(String, String)]) {
    let Some(at) = laptop.map(|endpoint| {
        data.laptops.push(Laptop {
            endpoint,
            fleets: Vec::new(),
        });
        data.laptops.len() - 1
    }) else {
        return;
    };
    for (id, value) in jar {
        let Some((device, secret)) = shaped(value) else {
            continue;
        };
        data.laptops[at].fleets.push(Entry {
            id: id.clone(),
            name: String::new(),
            device: device.to_owned(),
            secret: secret.to_owned(),
            paired: now(),
        });
    }
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

    const LAPTOP: &str = "4d92c10d0b7e3f5a9c1e6b2d8f4a7c3e5b9d1f6a2c8e4b7d3f5a9c1e6b2d6ed6";
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
            fs::write(dir.path().join("laptop"), format!("{LAPTOP}\n")).unwrap();
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
        assert_eq!(sessions.header().as_deref(), Some(BOTH));
        sessions.data.lock().unwrap().laptops[0].fleets[0].name = "hand".to_owned();
        let next = sessions.data.lock().unwrap().clone();
        sessions.commit(&mut Data::default(), next).unwrap();

        let sealed = fs::read(setup.file()).unwrap();
        assert!(!sealed.windows(4).any(|window| window == A.as_bytes()));
        setup.jar.hold(&[]);
        let reopened = setup.open().unwrap();
        assert!(!reopened.quarantined());
        assert_eq!(reopened.header().as_deref(), Some(BOTH));
        let data = reopened.data.lock().unwrap();
        assert_eq!(data.laptops[0].fleets[0].name, "hand");
        assert_eq!(data.laptops[0].endpoint, LAPTOP);
    }

    #[test]
    fn an_unreadable_store_is_quarantined_and_the_pairings_come_back_from_the_jar() {
        let setup = Setup::new(&[COOKIE]);
        setup.open().unwrap().header();
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

            let sessions = setup.open().unwrap();

            assert!(sessions.quarantined());
            assert_eq!(fs::read(setup.bad()).unwrap(), damaged);
            assert_eq!(sessions.header().as_deref(), Some(COOKIE));
            assert!(setup.file().exists());
            assert!(!setup.open().unwrap().quarantined());
        }
    }

    #[test]
    fn a_keystore_error_that_is_not_corruption_fails_and_leaves_the_store_alone() {
        let setup = Setup::new(&[COOKIE]);
        setup.open().unwrap().header();
        let before = fs::read(setup.file()).unwrap();
        setup.cipher.fail_next_open(io::ErrorKind::PermissionDenied);

        let error = setup.open().err().unwrap();

        assert_eq!(error.kind(), io::ErrorKind::PermissionDenied);
        assert_eq!(fs::read(setup.file()).unwrap(), before);
        assert!(!setup.bad().exists());
        assert_eq!(setup.open().unwrap().header().as_deref(), Some(COOKIE));
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
        assert_eq!(sessions.header(), None);

        assert!(!sessions.quarantined());
        let second = setup.open().unwrap();
        assert!(!second.quarantined());
        assert!(!setup.bad().exists());
    }

    #[test]
    fn the_laptop_file_is_imported_once_and_the_store_keeps_it() {
        let setup = Setup::new(&[]);
        let sessions = setup.open().unwrap();
        assert_eq!(sessions.header(), None);
        assert!(setup.file().exists());

        fs::remove_file(setup.dir.path().join("laptop")).unwrap();

        let reopened = setup.open().unwrap();
        assert_eq!(reopened.data.lock().unwrap().laptops[0].endpoint, LAPTOP);
    }

    #[test]
    fn the_jar_is_read_once_when_the_store_is_missing() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        assert_eq!(sessions.header().as_deref(), Some(COOKIE));

        setup.jar.hold(&[NEWER, SECOND]);
        assert_eq!(sessions.header().as_deref(), Some(COOKIE));
        assert_eq!(setup.open().unwrap().header().as_deref(), Some(COOKIE));
    }

    #[test]
    fn a_fresh_install_never_imports_a_cookie_the_jar_gains_later() {
        let setup = Setup::new(&[]);
        let sessions = setup.open().unwrap();
        assert_eq!(sessions.header(), None);

        setup.jar.hold(&[COOKIE]);

        assert_eq!(sessions.header(), None);
        assert_eq!(setup.open().unwrap().header(), None);
    }

    #[test]
    fn an_unreadable_jar_leaves_the_store_unwritten_for_the_next_start() {
        let setup = Setup::new(&[COOKIE]);
        setup.jar.fail_read.store(true, Ordering::SeqCst);
        let sessions = setup.open().unwrap();

        assert_eq!(sessions.header(), None);
        assert!(!setup.file().exists());

        setup.jar.fail_read.store(false, Ordering::SeqCst);
        assert_eq!(sessions.header(), None);
        assert_eq!(setup.open().unwrap().header().as_deref(), Some(COOKIE));
    }

    #[test]
    fn forget_is_not_undone_by_a_restart() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();

        sessions.forget().unwrap();

        assert!(setup.jar.cookies().unwrap().is_none());
        assert_eq!(sessions.header(), None);
        assert_eq!(setup.open().unwrap().header(), None);
    }

    #[test]
    fn a_jar_that_cannot_be_cleared_keeps_the_pairings_and_forget_says_so() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        setup.jar.fail_clear.store(true, Ordering::SeqCst);

        assert!(sessions.forget().is_err());

        assert_eq!(sessions.header().as_deref(), Some(COOKIE));
        assert_eq!(setup.open().unwrap().header().as_deref(), Some(COOKIE));
    }

    #[test]
    fn a_crash_between_the_write_and_the_rename_leaves_the_old_file() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions.header();
        let before = fs::read(setup.file()).unwrap();

        let mut next = sessions.data.lock().unwrap().clone();
        next.laptops[0].fleets.clear();
        sessions.stage(&next).unwrap();
        drop(sessions);

        assert_eq!(fs::read(setup.file()).unwrap(), before);
        setup.jar.hold(&[]);
        assert_eq!(setup.open().unwrap().header().as_deref(), Some(COOKIE));
    }

    #[test]
    fn a_failed_import_write_is_reported_and_the_next_start_writes_it() {
        let setup = Setup::new(&[COOKIE]);
        setup.cipher.fail_next_seal();

        let sessions = setup.open().unwrap();
        assert!(!sessions.unsaved());
        assert_eq!(sessions.header().as_deref(), Some(COOKIE));

        assert!(sessions.unsaved());
        assert!(!setup.file().exists());

        let reopened = setup.open().unwrap();
        assert!(!reopened.unsaved());
        assert_eq!(reopened.header().as_deref(), Some(COOKIE));
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
        next.laptops.push(Laptop {
            endpoint: LAPTOP.to_owned(),
            fleets: Vec::new(),
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
            r#"{{"laptops":[{{"endpoint":"{LAPTOP}","fleets":[{{"id":"{FLEET}","device":"7","secret":"{A}"}}]}}]}}"#
        );
        fs::create_dir_all(setup.file().parent().unwrap()).unwrap();
        fs::write(setup.file(), setup.cipher.seal(plain.as_bytes()).unwrap()).unwrap();

        let sessions = setup.open().unwrap();

        assert!(!sessions.quarantined());
        assert_eq!(sessions.header().as_deref(), Some(COOKIE));
    }

    const NEW_LAPTOP: &str = "d6e458a66697094c1bc7b54cb83fea66a8db57c49f37514944579712cd7e43c5";

    fn paired(id: &str, device: &str, secret: &str) -> Paired {
        Paired {
            id: id.to_owned(),
            name: format!("name of {id}"),
            value: format!("{device}.{secret}"),
        }
    }

    fn header_of(sessions: &Sessions<FakeJar>) -> Vec<String> {
        let mut parts: Vec<String> = sessions
            .header()
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
            .pair(
                LAPTOP,
                &[paired(FLEET, "7", A), paired(OTHER, "9", C)],
                || {},
            )
            .unwrap();

        assert_eq!(sessions.cookie(FLEET).as_deref(), Some(COOKIE));
        assert_eq!(sessions.cookie(OTHER).as_deref(), Some(SECOND));
        assert_eq!(sessions.cookie("f111111111111"), None);
        assert_eq!(sessions.cookie("f012345678"), None);
        sessions.forget().unwrap();
        assert_eq!(sessions.cookie(FLEET), None);
    }

    #[test]
    fn a_pairing_adds_every_fleet_adopts_the_others_and_clears_the_jar() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        let mut replaced = false;

        sessions
            .pair(
                LAPTOP,
                &[paired(OTHER, "9", C), paired("f111111111111", "5", B)],
                || replaced = true,
            )
            .unwrap();

        assert!(!replaced);
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
        assert_eq!(data.laptops[0].fleets[2].name, "name of f111111111111");
        assert!(!sessions.unsaved());
    }

    #[test]
    fn a_pairing_for_a_fleet_already_held_keeps_its_secret_against_the_jar() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();

        sessions
            .pair(LAPTOP, &[paired(FLEET, "8", B)], || {})
            .unwrap();
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
        sessions.header();
        let before = fs::read(setup.file()).unwrap();

        setup.cipher.fail_next_seal();
        let failed = sessions.pair(LAPTOP, &[paired(OTHER, "9", C)], || {});

        assert!(failed.is_err());
        assert!(sessions.unsaved());
        assert_eq!(fs::read(setup.file()).unwrap(), before);
        assert_eq!(header_of(&sessions), [COOKIE.to_owned()]);
        assert!(setup.jar.cookies().unwrap().is_some());
        assert!(!setup.dir.path().join(DIR).join(STAGED).exists());

        sessions
            .pair(LAPTOP, &[paired(OTHER, "9", C)], || {})
            .unwrap();
        assert!(!sessions.unsaved());
        assert_eq!(header_of(&sessions).len(), 2);
    }

    #[test]
    fn a_jar_that_cannot_be_cleared_after_the_write_leaves_the_pairing_made_and_says_so() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();
        setup.jar.fail_clear.store(true, Ordering::SeqCst);
        let mut stopped = 0;

        sessions
            .pair(NEW_LAPTOP, &[paired("f111111111111", "5", B)], || {
                stopped += 1
            })
            .unwrap();

        assert_eq!(stopped, 1);
        assert!(sessions.unsaved());
        fs::write(setup.dir.path().join("laptop"), format!("{NEW_LAPTOP}\n")).unwrap();
        assert_eq!(
            header_of(&sessions)
                .into_iter()
                .filter(|part| part.contains("f111111111111"))
                .count(),
            1
        );
        assert_eq!(
            header_of(&setup.open().unwrap())
                .into_iter()
                .filter(|part| part.contains("f111111111111"))
                .count(),
            1
        );
    }

    #[test]
    fn a_stale_cookie_left_by_a_failed_clear_stays_unfinished_and_never_becomes_a_ghost_fleet() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();
        setup.jar.fail_clear.store(true, Ordering::SeqCst);
        sessions
            .pair(NEW_LAPTOP, &[paired("f111111111111", "5", B)], || {})
            .unwrap();
        fs::write(setup.dir.path().join("laptop"), format!("{NEW_LAPTOP}\n")).unwrap();
        let only = ["hand_phone_f111111111111=5.".to_owned() + B];

        assert!(sessions.unsaved());
        assert_eq!(header_of(&sessions), only);
        assert!(sessions.unsaved());
        assert_eq!(header_of(&sessions), only);
        assert_eq!(header_of(&setup.open().unwrap()), only);

        setup.jar.fail_clear.store(false, Ordering::SeqCst);
        setup.jar.hold(&[]);
        assert_eq!(header_of(&sessions), only);
        assert!(sessions.unsaved());
        sessions
            .pair(NEW_LAPTOP, &[paired("f222222222222", "6", C)], || {})
            .unwrap();
        assert!(!sessions.unsaved());
    }

    #[test]
    fn forget_ends_an_unfinished_pairing() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        setup.jar.fail_clear.store(true, Ordering::SeqCst);
        sessions
            .pair(LAPTOP, &[paired(OTHER, "9", C)], || {})
            .unwrap();
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
        let done = sessions.pair(LAPTOP, &[paired(OTHER, "9", C)], || {});
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o700)).unwrap();

        assert!(done.is_ok());
        assert!(sessions.unsaved());
        assert_eq!(header_of(&sessions).len(), 1);
    }

    #[test]
    fn a_pairing_with_another_laptop_replaces_every_fleet_and_stops_the_feeds_once() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();
        let mut stopped = 0;

        sessions
            .pair(NEW_LAPTOP, &[paired("f111111111111", "5", B)], || {
                stopped += 1
            })
            .unwrap();

        assert_eq!(stopped, 1);
        fs::write(setup.dir.path().join("laptop"), format!("{NEW_LAPTOP}\n")).unwrap();
        assert_eq!(
            header_of(&sessions),
            ["hand_phone_f111111111111=5.".to_owned() + B]
        );
        let data = sessions.data.lock().unwrap();
        assert_eq!(data.laptops.len(), 1);
        assert_eq!(data.laptops[0].endpoint, NEW_LAPTOP);
        assert!(setup.jar.cookies().unwrap().is_none());
    }

    #[test]
    fn a_replacement_that_fails_leaves_the_old_laptop_and_stops_no_feed_when_it_cannot_save() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions.header();
        let mut stopped = 0;

        setup.cipher.fail_next_seal();
        let failed = sessions.pair(NEW_LAPTOP, &[paired("f111111111111", "5", B)], || {
            stopped += 1
        });

        assert!(failed.is_err());
        assert_eq!(stopped, 0);
        assert_eq!(header_of(&sessions), [COOKIE.to_owned()]);
        assert!(setup.jar.cookies().unwrap().is_some());
    }

    #[test]
    fn a_failed_forget_stops_serving_the_pairings_and_says_so() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions
            .pair(LAPTOP, &[paired(OTHER, "9", C)], || {})
            .unwrap();
        assert!(sessions.header().is_some());

        setup.cipher.fail_next_seal();
        assert!(sessions.forget().is_err());

        assert!(sessions.unsaved());
        assert_eq!(sessions.header(), None);
        sessions.forget().unwrap();
        assert!(!sessions.unsaved());
        assert_eq!(setup.open().unwrap().header(), None);
    }
}
