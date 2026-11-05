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
    stderr: thread::JoinHandle<String>,
    stdout_after: thread::JoinHandle<String>,
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
    let stdout_after = thread::spawn(move || {
        let mut reader = BufReader::new(stdout);
        let mut first = String::new();
        reader.read_line(&mut first).unwrap();
        let _ = line_sent.send(first);
        let mut after = String::new();
        reader.read_to_string(&mut after).unwrap();
        after
    });
    let line = line.recv_timeout(WAIT).expect("no line on stdout");
    let fields: Vec<&str> = line.trim_end().split(' ').collect();
    assert_eq!(fields.len(), 3, "{line:?}");
    assert!(!line.contains(SECRET));
    assert_eq!(fields[0], "ready");
    let id = fields[1].strip_prefix("id=").expect("id field").to_owned();
    assert_eq!(fields[2], "relay=none");
    assert_eq!(id.len(), 64);
    assert!(
        id.bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
    );
    Sidecar {
        child,
        id,
        stderr,
        stdout_after,
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
        let after = self.stdout_after.join().unwrap();
        assert_eq!(after, "", "stdout carries more than the ready line");
        (status, self.stderr.join().unwrap())
    }
}

fn locked_iroh() -> &'static str {
    include_str!("../Cargo.lock")
        .split("name = \"iroh\"\nversion = \"")
        .nth(1)
        .and_then(|rest| rest.split('"').next())
        .expect("Cargo.lock locks iroh")
}

#[cfg(target_os = "linux")]
fn udp_sockets(pid: u32) -> Vec<String> {
    let inodes: Vec<String> = std::fs::read_dir(format!("/proc/{pid}/fd"))
        .unwrap()
        .filter_map(|entry| std::fs::read_link(entry.unwrap().path()).ok())
        .filter_map(|link| {
            let link = link.to_str()?;
            let inode = link.strip_prefix("socket:[")?.strip_suffix(']')?;
            Some(inode.to_owned())
        })
        .collect();
    let mut found = Vec::new();
    for table in ["/proc/net/udp", "/proc/net/udp6"] {
        for line in std::fs::read_to_string(table).unwrap().lines().skip(1) {
            let columns: Vec<&str> = line.split_whitespace().collect();
            if inodes.iter().any(|inode| inode == columns[9]) {
                found.push(columns[1].to_owned());
            }
        }
    }
    found
}

#[cfg(target_os = "linux")]
fn loopback(local: &str) -> bool {
    let (ip, _port) = local.split_once(':').unwrap();
    ip.ends_with("7F") && ip.len() == 8 || ip == "00000000000000000000000001000000"
}

#[test]
fn version_names_the_build_and_the_locked_iroh() {
    let out = Command::new(BIN).arg("--version").output().unwrap();
    assert!(out.status.success());
    assert_eq!(
        String::from_utf8(out.stdout).unwrap(),
        format!(
            "hand-iroh {} iroh {}\n",
            env!("CARGO_PKG_VERSION"),
            locked_iroh()
        )
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
    #[cfg(target_os = "linux")]
    {
        let sockets = udp_sockets(sidecar.child.id());
        assert!(!sockets.is_empty());
        assert!(sockets.iter().all(|addr| loopback(addr)), "{sockets:?}");
    }
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
