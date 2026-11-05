#[path = "../src/testutil.rs"]
mod testutil;

use std::io::{BufRead, BufReader, Read};
use std::path::Path;
use std::process::{Child, Command, Stdio};
use std::sync::mpsc;
use std::thread;
use std::time::{Duration, Instant};

use testutil::TempDir;

const BIN: &str = env!("CARGO_BIN_EXE_hand-iroh");
const SECRET: &str = "feedfacefeedfacefeedfacefeedfacefeedfacefeedface";
const WAIT: Duration = Duration::from_secs(20);

struct Sidecar {
    child: Child,
    id: String,
    relay: String,
    stderr: thread::JoinHandle<String>,
}

fn start(dir: &TempDir, keys: &Path) -> Sidecar {
    let secret = dir.file("board.peer", &format!("{SECRET}\n"));
    let mut child = Command::new(BIN)
        .args(["--board", "127.0.0.1:9", "--relay", "none", "--secret-file"])
        .arg(secret)
        .arg("--key-dir")
        .arg(keys)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stderr = child.stderr.take().unwrap();
    let stderr = thread::spawn(move || {
        let mut text = String::new();
        stderr.read_to_string(&mut text).unwrap();
        text
    });
    let stdout = child.stdout.take().unwrap();
    let (line_sent, line) = mpsc::channel();
    thread::spawn(move || {
        let mut text = String::new();
        BufReader::new(stdout).read_line(&mut text).unwrap();
        let _ = line_sent.send(text);
    });
    let line = line.recv_timeout(WAIT).expect("no line on stdout");
    let fields: Vec<&str> = line.trim_end().split(' ').collect();
    assert_eq!(fields.len(), 3, "{line:?}");
    assert_eq!(fields[0], "ready");
    let id = fields[1].strip_prefix("id=").expect("id field").to_owned();
    let relay = fields[2]
        .strip_prefix("relay=")
        .expect("relay field")
        .to_owned();
    assert_eq!(id.len(), 64);
    assert!(
        id.bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
    );
    Sidecar {
        child,
        id,
        relay,
        stderr,
    }
}

impl Sidecar {
    fn finish(mut self) -> (std::process::ExitStatus, String) {
        let deadline = Instant::now() + WAIT;
        let status = loop {
            if let Some(status) = self.child.try_wait().unwrap() {
                break status;
            }
            if Instant::now() > deadline {
                self.child.kill().unwrap();
                panic!("the sidecar did not exit");
            }
            thread::sleep(Duration::from_millis(50));
        };
        (status, self.stderr.join().unwrap())
    }
}

#[test]
fn version_names_the_build_and_the_pinned_iroh() {
    let out = Command::new(BIN).arg("--version").output().unwrap();
    assert!(out.status.success());
    let lock = include_str!("../Cargo.lock");
    assert!(lock.contains("name = \"iroh\"\nversion = \"1.4.1\"\n"));
    assert_eq!(
        String::from_utf8(out.stdout).unwrap(),
        format!("hand-iroh {} iroh 1.4.1\n", env!("CARGO_PKG_VERSION"))
    );
}

#[test]
fn a_secret_on_the_command_line_is_refused_and_not_echoed() {
    let out = Command::new(BIN)
        .args(["--secret", "hunter2hunter2hunter2"])
        .output()
        .unwrap();
    assert_eq!(out.status.code(), Some(2));
    assert!(out.stdout.is_empty());
    assert!(!String::from_utf8_lossy(&out.stderr).contains("hunter2"));
}

#[test]
fn it_announces_one_line_and_exits_when_stdin_closes() {
    let dir = TempDir::new();
    let mut sidecar = start(&dir, &dir.path().join("keys"));
    assert_eq!(sidecar.relay, "none");
    drop(sidecar.child.stdin.take());
    let (status, stderr) = sidecar.finish();
    assert!(status.success());
    assert!(!stderr.contains(SECRET), "{stderr}");
}

#[test]
fn the_endpoint_id_survives_a_restart_and_follows_the_key_folder() {
    let dir = TempDir::new();
    let keys = dir.path().join("keys");
    let mut first = start(&dir, &keys);
    let first_id = first.id.clone();
    drop(first.child.stdin.take());
    assert!(first.finish().0.success());

    let mut again = start(&dir, &keys);
    assert_eq!(again.id, first_id);
    drop(again.child.stdin.take());
    assert!(again.finish().0.success());

    let mut other = start(&dir, &dir.path().join("other"));
    assert_ne!(other.id, first_id);
    drop(other.child.stdin.take());
    assert!(other.finish().0.success());
}

#[cfg(unix)]
#[test]
fn it_exits_cleanly_on_sigterm_and_sigint() {
    for signal in ["TERM", "INT"] {
        let dir = TempDir::new();
        let sidecar = start(&dir, &dir.path().join("keys"));
        let killed = Command::new("kill")
            .arg(format!("-{signal}"))
            .arg(sidecar.child.id().to_string())
            .status()
            .unwrap();
        assert!(killed.success());
        let (status, stderr) = sidecar.finish();
        assert!(status.success(), "{signal}: {status:?} {stderr}");
    }
}
