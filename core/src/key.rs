use std::fs::{self, OpenOptions};
use std::io::{self, Write};
use std::path::{Path, PathBuf};

use iroh::SecretKey;

const KEY_DIR: &str = "iroh";
const KEY_FILE: &str = "companion.key";

pub fn key_path(data_dir: &Path) -> PathBuf {
    data_dir.join(KEY_DIR).join(KEY_FILE)
}

pub fn load_or_create(data_dir: &Path) -> io::Result<SecretKey> {
    let dir = data_dir.join(KEY_DIR);
    create_dir(&dir)?;
    let path = dir.join(KEY_FILE);
    match read(&path) {
        Err(error) if error.kind() == io::ErrorKind::NotFound => {}
        found => return found,
    }
    let key = SecretKey::generate();
    let temp = dir.join(format!("{KEY_FILE}.{}.tmp", key.public().fmt_short()));
    let written = create_private(&temp).and_then(|mut file| {
        file.write_all(&key.to_bytes())?;
        file.sync_all()
    });
    let linked = written.and_then(|()| fs::hard_link(&temp, &path));
    let _ = fs::remove_file(&temp);
    match linked {
        Ok(()) => Ok(key),
        Err(error) if error.kind() == io::ErrorKind::AlreadyExists => read(&path),
        Err(error) => Err(error),
    }
}

fn read(path: &Path) -> io::Result<SecretKey> {
    let bytes = fs::read(path)?;
    let bytes: [u8; 32] = bytes.as_slice().try_into().map_err(|_| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            format!("{} is not a 32-byte key", path.display()),
        )
    })?;
    Ok(SecretKey::from_bytes(&bytes))
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
    use std::thread;

    use super::*;

    fn temp() -> crate::testutil::TempDir {
        crate::testutil::TempDir::new()
    }

    #[test]
    fn creates_one_key_and_reuses_it() {
        let dir = temp();
        let first = load_or_create(dir.path()).unwrap();
        let second = load_or_create(dir.path()).unwrap();
        assert_eq!(first.to_bytes(), second.to_bytes());
        assert_eq!(
            fs::read(key_path(dir.path())).unwrap(),
            first.to_bytes().to_vec()
        );
    }

    #[test]
    fn simultaneous_first_runs_agree_on_one_key() {
        let dir = Arc::new(temp());
        let keys: Vec<[u8; 32]> = (0..8)
            .map(|_| {
                let dir = dir.clone();
                thread::spawn(move || load_or_create(dir.path()).unwrap().to_bytes())
            })
            .collect::<Vec<_>>()
            .into_iter()
            .map(|thread| thread.join().unwrap())
            .collect();
        assert!(keys.iter().all(|key| *key == keys[0]));
    }

    #[test]
    fn refuses_a_key_of_the_wrong_size() {
        let dir = temp();
        fs::create_dir_all(dir.path().join(KEY_DIR)).unwrap();
        fs::write(key_path(dir.path()), b"short").unwrap();
        let error = load_or_create(dir.path()).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::InvalidData);
    }

    #[cfg(unix)]
    #[test]
    fn writes_the_key_and_its_folder_private() {
        use std::os::unix::fs::PermissionsExt;
        let dir = temp();
        load_or_create(dir.path()).unwrap();
        let mode = |path: &Path| fs::metadata(path).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode(&key_path(dir.path())), 0o600);
        assert_eq!(mode(&dir.path().join(KEY_DIR)), 0o700);
    }
}
