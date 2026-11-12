use std::fs;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicU8, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use crate::key::{CipherError, KeyCipher};

static NEXT: AtomicUsize = AtomicUsize::new(0);

pub struct TempDir(PathBuf);

impl TempDir {
    pub fn new() -> Self {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let count = NEXT.fetch_add(1, Ordering::Relaxed);
        let path = std::env::temp_dir().join(format!(
            "hand-android-{}-{nanos}-{count}",
            std::process::id()
        ));
        fs::create_dir(&path).unwrap();
        private(&path);
        Self(path)
    }

    pub fn path(&self) -> &Path {
        &self.0
    }
}

#[cfg(unix)]
fn private(path: &Path) {
    use std::os::unix::fs::PermissionsExt;
    fs::set_permissions(path, fs::Permissions::from_mode(0o700)).unwrap();
}

#[cfg(not(unix))]
fn private(_: &Path) {}

impl Drop for TempDir {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

pub const FAKE_IV: usize = 12;
pub const FAKE_TAG: usize = 16;
static NEXT_KEY: AtomicU8 = AtomicU8::new(1);

pub struct FakeCipher {
    key: Arc<Mutex<Option<u8>>>,
    fail_seal: Arc<AtomicBool>,
    open_error: Arc<Mutex<Option<io::ErrorKind>>>,
    probe_error: Arc<Mutex<Option<io::ErrorKind>>>,
}

impl FakeCipher {
    pub fn new() -> Self {
        Self {
            key: Arc::new(Mutex::new(None)),
            fail_seal: Arc::new(AtomicBool::new(false)),
            open_error: Arc::new(Mutex::new(None)),
            probe_error: Arc::new(Mutex::new(None)),
        }
    }

    pub fn forget_key(&self) {
        *self.key.lock().unwrap() = None;
    }

    pub fn rotate_key(&self) {
        *self.key.lock().unwrap() = Some(NEXT_KEY.fetch_add(1, Ordering::SeqCst));
    }

    pub fn fail_next_seal(&self) {
        self.fail_seal.store(true, Ordering::SeqCst);
    }

    pub fn fail_next_open(&self, kind: io::ErrorKind) {
        *self.open_error.lock().unwrap() = Some(kind);
    }

    pub fn fail_next_probe(&self, kind: io::ErrorKind) {
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
