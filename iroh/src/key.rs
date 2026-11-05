use std::fs::{self, File, OpenOptions};
use std::io::{self, Write};
use std::path::Path;
use std::time::Duration;

use iroh::SecretKey;

const FILE: &str = "hand-iroh.key";
const STALE_TEMP: Duration = Duration::from_secs(60);

pub fn load_or_create(dir: &Path) -> io::Result<SecretKey> {
    create_dir(dir)?;
    private(dir, 0o022, "can be changed by other users", "chmod go-w")?;
    sweep(dir);
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
    let linked = written.and_then(|()| link(&temp, &path));
    let _ = fs::remove_file(&temp);
    match linked {
        Ok(()) => Ok(key),
        Err(e) if e.kind() == io::ErrorKind::AlreadyExists => read(&path).map_err(|e| {
            if e.kind() == io::ErrorKind::NotFound {
                io::Error::new(
                    io::ErrorKind::NotFound,
                    format!(
                        "{} is a link to a file that does not exist; delete the link to make a new key",
                        path.display()
                    ),
                )
            } else {
                e
            }
        }),
        Err(e) => Err(e),
    }
}

fn link(temp: &Path, path: &Path) -> io::Result<()> {
    fs::hard_link(temp, path).map_err(|e| match e.kind() {
        io::ErrorKind::AlreadyExists => e,
        kind => io::Error::new(
            kind,
            format!("{e}; the key folder must be on a filesystem that supports hard links"),
        ),
    })
}

fn sweep(dir: &Path) {
    let Ok(entries) = fs::read_dir(dir) else {
        return;
    };
    for entry in entries.flatten() {
        let name = entry.file_name();
        let Some(name) = name.to_str() else { continue };
        let ours = name.starts_with(&format!("{FILE}.")) && name.ends_with(".tmp");
        let age = entry
            .metadata()
            .and_then(|m| m.modified())
            .ok()
            .and_then(|modified| modified.elapsed().ok());
        if ours && age.is_some_and(|age| age > STALE_TEMP) {
            let _ = fs::remove_file(entry.path());
        }
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
    private(path, 0o077, "can be read by other users", "chmod 600")?;
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
fn private(path: &Path, others: u32, problem: &str, fix: &str) -> io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    if fs::metadata(path)?.permissions().mode() & others != 0 {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            format!("{} {problem}; run {fix} on it", path.display()),
        ));
    }
    Ok(())
}

#[cfg(not(unix))]
fn private(_: &Path, _: u32, _: &str, _: &str) -> io::Result<()> {
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;
    use std::thread;
    use std::time::SystemTime;

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
            assert!(
                error.to_string().contains("run chmod go-w on it"),
                "{error}"
            );
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
        assert!(error.to_string().contains("run chmod 600 on it"), "{error}");
    }

    #[test]
    fn temporary_leftovers_older_than_a_minute_are_removed_and_nothing_else() {
        let dir = TempDir::new();
        let stale = dir.file(&format!("{FILE}.aaaa.tmp"), "old");
        let fresh = dir.file(&format!("{FILE}.bbbb.tmp"), "new");
        let unrelated = dir.file("notes.tmp", "mine");
        let old = SystemTime::now() - Duration::from_secs(120);
        File::options()
            .write(true)
            .open(&stale)
            .unwrap()
            .set_modified(old)
            .unwrap();
        load_or_create(dir.path()).unwrap();
        assert!(!stale.exists());
        assert!(fresh.exists());
        assert!(unrelated.exists());
    }

    #[cfg(unix)]
    #[test]
    fn a_link_to_nothing_is_named_in_the_error() {
        let dir = TempDir::new();
        std::os::unix::fs::symlink(dir.path().join("missing"), dir.path().join(FILE)).unwrap();
        let error = load_or_create(dir.path()).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::NotFound);
        assert!(
            error
                .to_string()
                .contains("link to a file that does not exist"),
            "{error}"
        );
        assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
    }

    #[test]
    fn a_link_that_cannot_be_made_says_what_the_folder_needs() {
        let dir = TempDir::new();
        let error = link(&dir.path().join("absent"), &dir.path().join("target")).unwrap_err();
        assert!(error.to_string().contains("supports hard links"), "{error}");
        let existing = dir.file("existing", "");
        let error = link(&existing, &existing).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::AlreadyExists);
    }
}
