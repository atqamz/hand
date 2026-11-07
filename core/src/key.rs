use std::fmt;
use std::fs::{self, OpenOptions};
use std::io::{self, Write};
use std::path::{Path, PathBuf};

use iroh::SecretKey;

const KEY_DIR: &str = "iroh";
const KEY_FILE: &str = "companion.key";
const KEY_LEN: usize = 32;

const CODE_KEY_REPLACED: &str = "keyReplaced";
const CODE_CORRUPT: &str = "corrupt";
#[cfg(test)]
const CODE_KEYSTORE: &str = "keystore";

pub trait KeyCipher: Send + Sync {
    fn seal(&self, plaintext: &[u8]) -> Result<Vec<u8>, CipherError>;
    fn open(&self, sealed: &[u8]) -> Result<Vec<u8>, CipherError>;
    fn probe(&self) -> Result<(), CipherError>;
}

#[derive(Debug)]
pub enum CipherError {
    KeyReplaced,
    Corrupt(String),
    Io(io::Error),
}

impl fmt::Display for CipherError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::KeyReplaced => write!(f, "the key that sealed the stored key is gone"),
            Self::Corrupt(message) => write!(f, "{message}"),
            Self::Io(error) => write!(f, "{error}"),
        }
    }
}

impl CipherError {
    pub fn from_plugin(code: Option<&str>, message: Option<&str>) -> Self {
        match code {
            Some(CODE_KEY_REPLACED) => Self::KeyReplaced,
            Some(CODE_CORRUPT) => {
                Self::Corrupt(message.unwrap_or("the stored key is corrupt").to_owned())
            }
            _ => Self::Io(io::Error::other(
                message.unwrap_or("the Android Keystore failed").to_owned(),
            )),
        }
    }
}

impl std::error::Error for CipherError {}

impl From<io::Error> for CipherError {
    fn from(error: io::Error) -> Self {
        Self::Io(error)
    }
}

pub fn key_path(data_dir: &Path) -> PathBuf {
    data_dir.join(KEY_DIR).join(KEY_FILE)
}

pub fn load_or_create(data_dir: &Path, cipher: &dyn KeyCipher) -> io::Result<SecretKey> {
    let dir = data_dir.join(KEY_DIR);
    create_dir(&dir)?;
    let path = dir.join(KEY_FILE);
    match stored(&path, cipher)? {
        Stored::Loaded(bytes) => Ok(SecretKey::from_bytes(&bytes)),
        Stored::Missing => create(&dir, &path, cipher),
        Stored::Replaced => replace(&dir, &path, cipher),
    }
}

enum Stored {
    Loaded([u8; KEY_LEN]),
    Missing,
    Replaced,
}

fn stored(path: &Path, cipher: &dyn KeyCipher) -> io::Result<Stored> {
    let sealed = match fs::read(path) {
        Ok(sealed) => sealed,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(Stored::Missing),
        Err(error) => return Err(error),
    };
    match cipher.probe() {
        Ok(()) => {}
        Err(CipherError::KeyReplaced) => return Ok(Stored::Replaced),
        Err(error) => return Err(cipher_io(error)),
    }
    let plaintext = match cipher.open(&sealed) {
        Ok(plaintext) => plaintext,
        Err(CipherError::KeyReplaced) => return Ok(Stored::Replaced),
        Err(CipherError::Corrupt(message)) => return Err(corrupt(path, &message)),
        Err(CipherError::Io(error)) => return Err(error),
    };
    let bytes: [u8; KEY_LEN] = plaintext
        .as_slice()
        .try_into()
        .map_err(|_| corrupt(path, "it does not hold 32 bytes"))?;
    Ok(Stored::Loaded(bytes))
}

fn create(dir: &Path, path: &Path, cipher: &dyn KeyCipher) -> io::Result<SecretKey> {
    let (key, sealed) = sealed_key(cipher)?;
    let temp = write_temp(dir, &key, &sealed)?;
    let linked = fs::hard_link(&temp, path);
    let _ = fs::remove_file(&temp);
    match linked {
        Ok(()) => sync_dir(dir).map(|()| key),
        Err(error) if error.kind() == io::ErrorKind::AlreadyExists => match stored(path, cipher)? {
            Stored::Loaded(bytes) => Ok(SecretKey::from_bytes(&bytes)),
            Stored::Missing | Stored::Replaced => Err(error),
        },
        Err(error) => Err(error),
    }
}

fn replace(dir: &Path, path: &Path, cipher: &dyn KeyCipher) -> io::Result<SecretKey> {
    match fs::remove_file(path) {
        Ok(()) => {}
        Err(error) if error.kind() == io::ErrorKind::NotFound => {}
        Err(error) => return Err(error),
    }
    sync_dir(dir)?;
    create(dir, path, cipher)
}

fn sealed_key(cipher: &dyn KeyCipher) -> io::Result<(SecretKey, Vec<u8>)> {
    let key = SecretKey::generate();
    let sealed = cipher.seal(&key.to_bytes()).map_err(cipher_io)?;
    Ok((key, sealed))
}

fn cipher_io(error: CipherError) -> io::Error {
    match error {
        CipherError::Io(error) => error,
        other => io::Error::other(other.to_string()),
    }
}

fn write_temp(dir: &Path, key: &SecretKey, sealed: &[u8]) -> io::Result<PathBuf> {
    let temp = dir.join(format!("{KEY_FILE}.{}.tmp", key.public().fmt_short()));
    let written = create_private(&temp).and_then(|mut file| {
        file.write_all(sealed)?;
        file.sync_all()
    });
    match written {
        Ok(()) => Ok(temp),
        Err(error) => {
            let _ = fs::remove_file(&temp);
            Err(error)
        }
    }
}

fn corrupt(path: &Path, reason: &str) -> io::Error {
    io::Error::new(
        io::ErrorKind::InvalidData,
        format!("{} is not a readable key: {reason}", path.display()),
    )
}

#[cfg(unix)]
fn sync_dir(dir: &Path) -> io::Result<()> {
    fs::File::open(dir)?.sync_all()
}

#[cfg(not(unix))]
fn sync_dir(_: &Path) -> io::Result<()> {
    Ok(())
}

#[cfg(unix)]
fn create_dir(dir: &Path) -> io::Result<()> {
    use std::os::unix::fs::DirBuilderExt;
    fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(dir)
}

#[cfg(not(unix))]
fn create_dir(dir: &Path) -> io::Result<()> {
    fs::create_dir_all(dir)
}

#[cfg(unix)]
fn create_private(path: &Path) -> io::Result<fs::File> {
    use std::os::unix::fs::OpenOptionsExt;
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)
}

#[cfg(not(unix))]
fn create_private(path: &Path) -> io::Result<fs::File> {
    OpenOptions::new().write(true).create_new(true).open(path)
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;
    use std::sync::Mutex;
    use std::sync::atomic::{AtomicBool, AtomicU8, Ordering};
    use std::thread;

    use super::*;

    const FAKE_IV: usize = 12;
    const FAKE_TAG: usize = 16;
    static NEXT_KEY: AtomicU8 = AtomicU8::new(1);

    struct FakeCipher {
        key: Arc<Mutex<Option<u8>>>,
        fail_seal: Arc<AtomicBool>,
        open_error: Arc<Mutex<Option<io::ErrorKind>>>,
        probe_error: Arc<Mutex<Option<io::ErrorKind>>>,
    }

    impl FakeCipher {
        fn new() -> Self {
            Self {
                key: Arc::new(Mutex::new(None)),
                fail_seal: Arc::new(AtomicBool::new(false)),
                open_error: Arc::new(Mutex::new(None)),
                probe_error: Arc::new(Mutex::new(None)),
            }
        }

        fn forget_key(&self) {
            *self.key.lock().unwrap() = None;
        }

        fn rotate_key(&self) {
            *self.key.lock().unwrap() = Some(NEXT_KEY.fetch_add(1, Ordering::SeqCst));
        }

        fn fail_next_seal(&self) {
            self.fail_seal.store(true, Ordering::SeqCst);
        }

        fn fail_next_open(&self, kind: io::ErrorKind) {
            *self.open_error.lock().unwrap() = Some(kind);
        }

        fn fail_next_probe(&self, kind: io::ErrorKind) {
            *self.probe_error.lock().unwrap() = Some(kind);
        }
    }

    fn stream(id: u8, iv: &[u8], index: usize) -> u8 {
        id.wrapping_mul(31)
            .wrapping_add(iv[index % iv.len()])
            .wrapping_add(index as u8)
    }

    fn fake_tag(id: u8, iv: &[u8], ciphertext: &[u8]) -> [u8; FAKE_TAG] {
        let mut tag = [0u8; FAKE_TAG];
        for (index, byte) in ciphertext.iter().enumerate() {
            let slot = index % FAKE_TAG;
            tag[slot] = tag[slot].wrapping_add(byte ^ stream(id, iv, index));
        }
        tag
    }

    fn fake_seal(id: u8, plaintext: &[u8]) -> Vec<u8> {
        let iv: [u8; FAKE_IV] = std::array::from_fn(|index| id.wrapping_add(index as u8));
        let ciphertext: Vec<u8> = plaintext
            .iter()
            .enumerate()
            .map(|(index, byte)| byte ^ stream(id, &iv, index))
            .collect();
        let mut sealed = iv.to_vec();
        sealed.extend_from_slice(&ciphertext);
        sealed.extend_from_slice(&fake_tag(id, &iv, &ciphertext));
        sealed
    }

    impl KeyCipher for FakeCipher {
        fn probe(&self) -> Result<(), CipherError> {
            if let Some(kind) = self.probe_error.lock().unwrap().take() {
                return Err(CipherError::Io(io::Error::from(kind)));
            }
            if self.key.lock().unwrap().is_some() {
                Ok(())
            } else {
                Err(CipherError::KeyReplaced)
            }
        }

        fn seal(&self, plaintext: &[u8]) -> Result<Vec<u8>, CipherError> {
            let mut key = self.key.lock().unwrap();
            let id = *key.get_or_insert_with(|| NEXT_KEY.fetch_add(1, Ordering::SeqCst));
            if self.fail_seal.swap(false, Ordering::SeqCst) {
                return Err(CipherError::Io(io::Error::other("the Keystore died")));
            }
            Ok(fake_seal(id, plaintext))
        }

        fn open(&self, sealed: &[u8]) -> Result<Vec<u8>, CipherError> {
            if let Some(kind) = self.open_error.lock().unwrap().take() {
                return Err(CipherError::Io(io::Error::from(kind)));
            }
            if sealed.len() < FAKE_IV + FAKE_TAG + 1 {
                return Err(CipherError::Corrupt("the ciphertext is too short".into()));
            }
            let Some(id) = *self.key.lock().unwrap() else {
                return Err(CipherError::KeyReplaced);
            };
            let (iv, rest) = sealed.split_at(FAKE_IV);
            let (ciphertext, tag) = rest.split_at(rest.len() - FAKE_TAG);
            if tag != fake_tag(id, iv, ciphertext) {
                return Err(CipherError::Corrupt(
                    "the ciphertext failed authentication".into(),
                ));
            }
            Ok(ciphertext
                .iter()
                .enumerate()
                .map(|(index, byte)| byte ^ stream(id, iv, index))
                .collect())
        }
    }

    fn temp() -> crate::testutil::TempDir {
        crate::testutil::TempDir::new()
    }

    #[test]
    fn creates_one_sealed_key_and_reuses_it() {
        let dir = temp();
        let cipher = FakeCipher::new();
        let first = load_or_create(dir.path(), &cipher).unwrap();
        let second = load_or_create(dir.path(), &cipher).unwrap();
        assert_eq!(first.to_bytes(), second.to_bytes());
        let sealed = fs::read(key_path(dir.path())).unwrap();
        assert_eq!(sealed.len(), FAKE_IV + KEY_LEN + FAKE_TAG);
        assert_ne!(sealed, first.to_bytes().to_vec());
    }

    #[test]
    fn simultaneous_first_runs_agree_on_one_key() {
        let dir = Arc::new(temp());
        let cipher = Arc::new(FakeCipher::new());
        let keys: Vec<[u8; 32]> = (0..8)
            .map(|_| {
                let dir = dir.clone();
                let cipher = cipher.clone();
                thread::spawn(move || {
                    load_or_create(dir.path(), cipher.as_ref())
                        .unwrap()
                        .to_bytes()
                })
            })
            .collect::<Vec<_>>()
            .into_iter()
            .map(|thread| thread.join().unwrap())
            .collect();
        assert!(keys.iter().all(|key| *key == keys[0]));
    }

    #[test]
    fn refuses_corrupt_ciphertext_and_leaves_it_alone() {
        let dir = temp();
        let cipher = FakeCipher::new();
        load_or_create(dir.path(), &cipher).unwrap();
        fs::write(key_path(dir.path()), b"short").unwrap();
        let error = load_or_create(dir.path(), &cipher).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::InvalidData);
        assert!(error.to_string().contains("not a readable key"));
        assert_eq!(fs::read(key_path(dir.path())).unwrap(), b"short");
    }

    #[test]
    fn a_missing_keystore_key_makes_a_new_identity() {
        let dir = temp();
        let cipher = FakeCipher::new();
        let first = load_or_create(dir.path(), &cipher).unwrap();
        cipher.forget_key();
        let second = load_or_create(dir.path(), &cipher).unwrap();
        assert_ne!(first.to_bytes(), second.to_bytes());
        let third = load_or_create(dir.path(), &cipher).unwrap();
        assert_eq!(second.to_bytes(), third.to_bytes());
    }

    #[test]
    fn a_changed_keystore_key_is_corrupt_not_replaced() {
        let dir = temp();
        let cipher = FakeCipher::new();
        load_or_create(dir.path(), &cipher).unwrap();
        cipher.rotate_key();
        let sealed = fs::read(key_path(dir.path())).unwrap();
        let error = load_or_create(dir.path(), &cipher).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::InvalidData);
        assert!(error.to_string().contains("failed authentication"));
        assert_eq!(fs::read(key_path(dir.path())).unwrap(), sealed);
    }

    #[test]
    fn keystore_errors_are_not_missing_or_replaced() {
        let dir = temp();
        let cipher = FakeCipher::new();
        load_or_create(dir.path(), &cipher).unwrap();
        let sealed = fs::read(key_path(dir.path())).unwrap();
        cipher.fail_next_open(io::ErrorKind::PermissionDenied);
        let error = load_or_create(dir.path(), &cipher).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::PermissionDenied);
        assert_eq!(fs::read(key_path(dir.path())).unwrap(), sealed);
    }

    #[test]
    fn a_keystore_probe_error_is_not_a_missing_key() {
        let dir = temp();
        let cipher = FakeCipher::new();
        let first = load_or_create(dir.path(), &cipher).unwrap();
        let sealed = fs::read(key_path(dir.path())).unwrap();
        cipher.fail_next_probe(io::ErrorKind::PermissionDenied);
        let error = load_or_create(dir.path(), &cipher).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::PermissionDenied);
        assert_eq!(fs::read(key_path(dir.path())).unwrap(), sealed);
        assert_eq!(
            load_or_create(dir.path(), &cipher).unwrap().to_bytes(),
            first.to_bytes()
        );
    }

    #[test]
    fn the_plugin_codes_match_the_android_plugin() {
        assert!(matches!(
            CipherError::from_plugin(Some(CODE_KEY_REPLACED), None),
            CipherError::KeyReplaced
        ));
        assert!(matches!(
            CipherError::from_plugin(Some(CODE_CORRUPT), Some("failed authentication")),
            CipherError::Corrupt(message) if message == "failed authentication"
        ));
        for (code, message) in [(Some(CODE_KEYSTORE), Some("boom")), (None, None)] {
            match CipherError::from_plugin(code, message) {
                CipherError::Io(error) => assert!(!error.to_string().is_empty()),
                other => panic!("{code:?} mapped to {other:?}"),
            }
        }
        const KOTLIN: &str = include_str!(
            "../../src-tauri/gen/android/app/src/main/java/io/github/atqamz/handcompanion/KeystorePlugin.kt"
        );
        for (name, code) in [
            ("CODE_KEY_REPLACED", CODE_KEY_REPLACED),
            ("CODE_CORRUPT", CODE_CORRUPT),
            ("CODE_KEYSTORE", CODE_KEYSTORE),
        ] {
            let declaration = format!("const val {name} = \"{code}\"");
            assert!(
                KOTLIN.contains(&declaration),
                "KeystorePlugin.kt does not declare `{declaration}`"
            );
        }
    }

    #[test]
    fn a_plaintext_that_is_not_32_bytes_is_corrupt() {
        let dir = temp();
        fs::create_dir_all(dir.path().join(KEY_DIR)).unwrap();
        let cipher = FakeCipher::new();
        fs::write(
            key_path(dir.path()),
            cipher.seal(&[1; KEY_LEN - 1]).unwrap(),
        )
        .unwrap();
        let error = load_or_create(dir.path(), &cipher).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::InvalidData);
        assert!(error.to_string().contains("32 bytes"));
    }

    #[test]
    fn an_interrupted_rotation_leaves_no_unopenable_blob() {
        let dir = temp();
        let cipher = FakeCipher::new();
        let first = load_or_create(dir.path(), &cipher).unwrap();
        cipher.forget_key();
        cipher.fail_next_seal();
        assert!(load_or_create(dir.path(), &cipher).is_err());
        assert!(!key_path(dir.path()).exists());
        let second = load_or_create(dir.path(), &cipher).unwrap();
        assert_ne!(first.to_bytes(), second.to_bytes());
        assert_eq!(
            load_or_create(dir.path(), &cipher).unwrap().to_bytes(),
            second.to_bytes()
        );
    }

    #[cfg(unix)]
    #[test]
    fn writes_the_ciphertext_and_its_folder_private() {
        use std::os::unix::fs::PermissionsExt;
        let dir = temp();
        load_or_create(dir.path(), &FakeCipher::new()).unwrap();
        let mode = |path: &Path| fs::metadata(path).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode(&key_path(dir.path())), 0o600);
        assert_eq!(mode(&dir.path().join(KEY_DIR)), 0o700);
    }
}
