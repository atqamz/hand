use std::fs;
use std::io::{self, Write};
use std::path::{Path, PathBuf};
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
    imported: bool,
}

pub struct Sessions<J> {
    root: PathBuf,
    jar: J,
    cipher: Arc<dyn KeyCipher>,
    data: Mutex<Data>,
    quarantined: bool,
}

impl<J: Jar> Sessions<J> {
    pub fn open(root: &Path, jar: J, cipher: Arc<dyn KeyCipher>) -> io::Result<Self> {
        let dir = root.join(DIR);
        create_dir(&dir)?;
        let (data, quarantined) = read(&dir, cipher.as_ref())?;
        Ok(Self {
            root: root.to_owned(),
            jar,
            cipher,
            data: Mutex::new(data),
            quarantined,
        })
    }

    pub fn quarantined(&self) -> bool {
        self.quarantined
    }

    pub fn header(&self) -> Option<String> {
        let mut data = self.data.lock().unwrap();
        let mut next = data.clone();
        if let Ok(jar) = self.jar.cookies() {
            let jar = paired_cookies(&jar.unwrap_or_default());
            import(&mut next, read_laptop(&self.root), &jar);
        }
        if next != *data {
            let _ = self.commit(&mut data, next.clone());
        }
        let header = next
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

    pub fn forget(&self) -> io::Result<()> {
        let mut data = self.data.lock().unwrap();
        self.jar.clear()?;
        self.commit(&mut data, Data::default())
    }

    fn commit(&self, data: &mut Data, next: Data) -> io::Result<()> {
        let staged = self.stage(&next)?;
        let dir = self.root.join(DIR);
        if let Err(error) = fs::rename(&staged, dir.join(FILE)) {
            let _ = fs::remove_file(&staged);
            return Err(error);
        }
        *data = next;
        sync_dir(&dir)
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

fn read(dir: &Path, cipher: &dyn KeyCipher) -> io::Result<(Data, bool)> {
    let path = dir.join(FILE);
    let sealed = match fs::read(&path) {
        Ok(sealed) => sealed,
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            return Ok((Data::default(), false));
        }
        Err(error) => return Err(error),
    };
    let plaintext = match cipher.open(&sealed) {
        Ok(plaintext) => plaintext,
        Err(CipherError::KeyReplaced) => {
            fs::remove_file(&path)?;
            sync_dir(dir)?;
            return Ok((Data::default(), false));
        }
        Err(CipherError::Corrupt(_)) => return quarantine(dir, &path),
        Err(CipherError::Io(error)) => return Err(error),
    };
    match serde_json::from_slice(&plaintext) {
        Ok(data) => Ok((data, false)),
        Err(_) => quarantine(dir, &path),
    }
}

fn quarantine(dir: &Path, path: &Path) -> io::Result<(Data, bool)> {
    fs::rename(path, dir.join(BAD))?;
    sync_dir(dir)?;
    Ok((Data::default(), true))
}

fn import(data: &mut Data, laptop: Option<String>, jar: &[(String, String)]) {
    for known in &mut data.laptops {
        known
            .fleets
            .retain(|entry| !entry.imported || jar.iter().any(|(id, _)| *id == entry.id));
    }
    let at = laptop.map(|endpoint| {
        data.laptops
            .iter()
            .position(|known| known.endpoint == endpoint)
            .unwrap_or_else(|| {
                data.laptops.push(Laptop {
                    endpoint,
                    fleets: Vec::new(),
                });
                data.laptops.len() - 1
            })
    });
    for (id, value) in jar {
        let Some((device, secret)) = shaped(value) else {
            continue;
        };
        let known = data
            .laptops
            .iter_mut()
            .flat_map(|laptop| laptop.fleets.iter_mut())
            .find(|entry| entry.id == *id);
        match (known, at) {
            (Some(entry), _) if !entry.imported => {}
            (Some(entry), _) => {
                if entry.device != device || entry.secret != secret {
                    entry.device = device.to_owned();
                    entry.secret = secret.to_owned();
                    entry.paired = now();
                }
            }
            (None, Some(at)) => data.laptops[at].fleets.push(Entry {
                id: id.clone(),
                name: String::new(),
                device: device.to_owned(),
                secret: secret.to_owned(),
                paired: now(),
                imported: true,
            }),
            (None, None) => {}
        }
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

    fn fleets(sessions: &Sessions<FakeJar>) -> Vec<(String, String, String)> {
        sessions
            .data
            .lock()
            .unwrap()
            .laptops
            .iter()
            .flat_map(|laptop| laptop.fleets.iter())
            .map(|entry| (entry.id.clone(), entry.device.clone(), entry.secret.clone()))
            .collect()
    }

    fn native(sessions: &Sessions<FakeJar>) {
        let mut data = sessions.data.lock().unwrap();
        for entry in data
            .laptops
            .iter_mut()
            .flat_map(|laptop| &mut laptop.fleets)
        {
            entry.imported = false;
        }
    }

    #[test]
    fn a_sealed_store_round_trips_and_holds_no_plaintext() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();
        assert_eq!(sessions.header().as_deref(), Some(BOTH));
        native(&sessions);
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
        let unparseable = setup.cipher.seal(b"{\"laptops\": [\"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc").unwrap();
        for damaged in [tampered, truncated, b"short".to_vec(), unparseable] {
            fs::write(setup.file(), &damaged).unwrap();

            let sessions = setup.open().unwrap();

            assert!(sessions.quarantined());
            assert_eq!(fs::read(setup.bad()).unwrap(), damaged);
            assert!(!setup.file().exists());
            assert_eq!(sessions.header().as_deref(), Some(COOKIE));
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
        assert!(!setup.file().exists());

        assert_eq!(sessions.header(), None);
        assert!(setup.file().exists());
        fs::remove_file(setup.dir.path().join("laptop")).unwrap();

        let reopened = setup.open().unwrap();
        assert_eq!(reopened.data.lock().unwrap().laptops[0].endpoint, LAPTOP);
    }

    #[test]
    fn the_jar_adds_missing_fleets_and_replaces_only_an_imported_secret_of_the_boards_shape() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions.header();
        sessions.data.lock().unwrap().laptops[0].fleets[0].name = "hand".to_owned();

        setup.jar.hold(&[COOKIE, SECOND]);
        sessions.header();
        assert_eq!(
            fleets(&sessions),
            [
                (FLEET.into(), "7".into(), A.into()),
                (OTHER.into(), "9".into(), C.into())
            ]
        );

        for unshaped in [
            "hand_phone_f0123456789ab=x.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
            "hand_phone_f0123456789ab=8.short",
            "hand_phone_f0123456789ab=8",
            "hand_phone_f0123456789ab=.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        ] {
            setup.jar.hold(&[unshaped, SECOND]);
            sessions.header();
            assert_eq!(fleets(&sessions)[0], (FLEET.into(), "7".into(), A.into()));
        }

        setup.jar.hold(&[NEWER, SECOND]);
        sessions.header();
        assert_eq!(fleets(&sessions)[0], (FLEET.into(), "8".into(), B.into()));
        assert_eq!(
            sessions.data.lock().unwrap().laptops[0].fleets[0].name,
            "hand"
        );

        native(&sessions);
        setup.jar.hold(&[COOKIE, SECOND]);
        sessions.header();
        assert_eq!(fleets(&sessions)[0], (FLEET.into(), "8".into(), B.into()));
    }

    #[test]
    fn an_imported_fleet_the_jar_lost_is_dropped_and_a_native_one_stays() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();
        sessions.header();
        sessions.data.lock().unwrap().laptops[0].fleets[1].imported = false;

        setup.jar.hold(&[]);

        assert_eq!(
            sessions.header().as_deref(),
            Some(
                "hand_phone_f999999999999=9.cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
            )
        );
        assert_eq!(setup.open().unwrap().header(), sessions.header());
    }

    #[test]
    fn an_unreadable_jar_changes_nothing() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions.header();
        setup.jar.hold(&[]);
        setup.jar.fail_read.store(true, Ordering::SeqCst);

        assert_eq!(sessions.header().as_deref(), Some(COOKIE));
        assert_eq!(setup.open().unwrap().header().as_deref(), Some(COOKIE));
    }

    #[test]
    fn forget_is_not_undone_by_the_next_import() {
        let setup = Setup::new(&[COOKIE, SECOND]);
        let sessions = setup.open().unwrap();
        sessions.header();

        sessions.forget().unwrap();

        assert!(setup.jar.cookies().unwrap().is_none());
        assert_eq!(sessions.header(), None);
        assert_eq!(setup.open().unwrap().header(), None);
    }

    #[test]
    fn a_jar_that_cannot_be_cleared_keeps_the_pairings_and_forget_says_so() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions.header();
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
        setup.jar.hold(&[COOKIE]);
        let reopened = setup.open().unwrap();
        assert_eq!(reopened.header().as_deref(), Some(COOKIE));
        setup.jar.hold(&[]);
        reopened.header();
        assert_eq!(setup.open().unwrap().header(), None);
    }

    #[test]
    fn a_failed_write_still_serves_the_header_and_is_retried_by_the_next_call() {
        let setup = Setup::new(&[COOKIE]);
        let sessions = setup.open().unwrap();
        sessions.header();
        let before = fs::read(setup.file()).unwrap();

        setup.jar.hold(&[COOKIE, SECOND]);
        setup.cipher.fail_next_seal();
        assert_eq!(sessions.header().as_deref(), Some(BOTH));

        assert_eq!(fs::read(setup.file()).unwrap(), before);
        assert_eq!(fleets(&sessions).len(), 1);
        assert_eq!(sessions.header().as_deref(), Some(BOTH));
        assert_eq!(fleets(&sessions).len(), 2);
        assert_ne!(fs::read(setup.file()).unwrap(), before);
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
}
