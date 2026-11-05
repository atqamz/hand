use std::fs::{self, File, OpenOptions};
use std::io::{self, Write};
use std::path::Path;

use iroh::SecretKey;

const FILE: &str = "hand-iroh.key";

pub fn load_or_create(dir: &Path) -> io::Result<SecretKey> {
    create_dir(dir)?;
    private(dir, 0o022, "700")?;
    let path = dir.join(FILE);
    match read(&path) {
        Err(e) if e.kind() == io::ErrorKind::NotFound => {}
        found => return found,
    }
    let key = SecretKey::generate();
    let temp = dir.join(format!("{FILE}.{}.tmp", key.public().fmt_short()));
    let written = create_private(&temp).and_then(|mut f| {
        f.write_all(&key.to_bytes())?;
        f.sync_all()
    });
    let linked = written.and_then(|()| fs::hard_link(&temp, &path));
    let _ = fs::remove_file(&temp);
    match linked {
        Ok(()) => Ok(key),
        Err(e) if e.kind() == io::ErrorKind::AlreadyExists => read(&path),
        Err(e) => Err(e),
    }
}

fn read(path: &Path) -> io::Result<SecretKey> {
    let bytes = fs::read(path)?;
    let bytes: [u8; 32] = bytes.as_slice().try_into().map_err(|_| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            format!(
                "{} is not a 32-byte key; delete it to make a new one",
                path.display()
            ),
        )
    })?;
    private(path, 0o077, "600")?;
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
fn create_private(path: &Path) -> io::Result<File> {
    use std::os::unix::fs::OpenOptionsExt;
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)
}

#[cfg(not(unix))]
fn create_private(path: &Path) -> io::Result<File> {
    OpenOptions::new().write(true).create_new(true).open(path)
}

#[cfg(unix)]
fn private(path: &Path, others: u32, chmod: &str) -> io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    if fs::metadata(path)?.permissions().mode() & others != 0 {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            format!(
                "{} is open to other users; run chmod {chmod} on it",
                path.display()
            ),
        ));
    }
    Ok(())
}

#[cfg(not(unix))]
fn private(_: &Path, _: u32, _: &str) -> io::Result<()> {
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;
    use std::thread;

    use super::*;
    use crate::testutil::TempDir;

    #[test]
    fn creates_the_key_once_and_reuses_it() {
        let dir = TempDir::new();
        let nested = dir.path().join("a").join("b");
        let first = load_or_create(&nested).unwrap();
        let second = load_or_create(&nested).unwrap();
        assert_eq!(first.to_bytes(), second.to_bytes());
        assert_eq!(fs::read(nested.join(FILE)).unwrap(), first.to_bytes());
        assert_eq!(fs::read_dir(&nested).unwrap().count(), 1);
    }

    #[test]
    fn simultaneous_first_runs_agree_on_one_key() {
        let dir = Arc::new(TempDir::new());
        let keys: Vec<[u8; 32]> = (0..8)
            .map(|_| {
                let dir = dir.clone();
                thread::spawn(move || load_or_create(dir.path()).unwrap().to_bytes())
            })
            .collect::<Vec<_>>()
            .into_iter()
            .map(|t| t.join().unwrap())
            .collect();
        assert!(keys.iter().all(|k| *k == keys[0]));
        assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
    }

    #[test]
    fn refuses_a_file_of_the_wrong_size() {
        let dir = TempDir::new();
        dir.file(FILE, "short");
        let error = load_or_create(dir.path()).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::InvalidData);
        assert!(!error.to_string().contains("short\n"));
    }

    #[cfg(unix)]
    #[test]
    fn writes_the_key_and_its_folder_private() {
        use std::os::unix::fs::PermissionsExt;
        let dir = TempDir::new();
        let nested = dir.path().join("keys");
        load_or_create(&nested).unwrap();
        let mode = |p: &Path| fs::metadata(p).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode(&nested.join(FILE)), 0o600);
        assert_eq!(mode(&nested), 0o700);
    }

    #[cfg(unix)]
    #[test]
    fn refuses_a_folder_other_users_can_write() {
        use std::os::unix::fs::PermissionsExt;
        for mode in [0o770, 0o707, 0o777] {
            let dir = TempDir::new();
            fs::set_permissions(dir.path(), fs::Permissions::from_mode(mode)).unwrap();
            let error = load_or_create(dir.path()).unwrap_err();
            assert_eq!(error.kind(), io::ErrorKind::PermissionDenied, "{mode:o}");
            assert!(!dir.path().join(FILE).exists());
        }
        let dir = TempDir::new();
        fs::set_permissions(dir.path(), fs::Permissions::from_mode(0o755)).unwrap();
        assert!(load_or_create(dir.path()).is_ok());
    }

    #[cfg(unix)]
    #[test]
    fn refuses_a_key_other_users_can_read() {
        use std::os::unix::fs::PermissionsExt;
        let dir = TempDir::new();
        load_or_create(dir.path()).unwrap();
        let path = dir.path().join(FILE);
        fs::set_permissions(&path, fs::Permissions::from_mode(0o640)).unwrap();
        let error = load_or_create(dir.path()).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::PermissionDenied);
    }
}
