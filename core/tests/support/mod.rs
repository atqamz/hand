#![allow(dead_code)]

use std::fs;
use std::io::{BufRead, BufReader};
use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc;
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use hand_android_core::{ALPN, Dialer, Proxy};
use iroh::endpoint::{Endpoint, PortmapperConfig, presets};
use iroh::{EndpointAddr, EndpointId, RelayMode};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use tokio::sync::watch;

const READY_WAIT: Duration = Duration::from_secs(20);
const STARTUP_WAIT: Duration = Duration::from_secs(5);

pub struct TempDir(PathBuf);

impl TempDir {
    pub fn new() -> Self {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let path =
            std::env::temp_dir().join(format!("hand-android-test-{}-{nanos}", std::process::id()));
        fs::create_dir(&path).unwrap();
        Self(path)
    }

    pub fn path(&self) -> &Path {
        &self.0
    }
}

impl Drop for TempDir {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

pub fn sidecar_bin() -> Option<PathBuf> {
    match std::env::var_os("HAND_IROH_BIN") {
        Some(path) => {
            let path = PathBuf::from(path);
            assert!(
                path.is_file(),
                "HAND_IROH_BIN does not name a file: {}",
                path.display()
            );
            Some(path)
        }
        None => {
            eprintln!("skipping: set HAND_IROH_BIN to a hand-iroh binary to run the sidecar tests");
            None
        }
    }
}

#[derive(Clone)]
pub struct SidecarOptions {
    pub board: SocketAddr,
    pub secret: String,
    pub max_connections: usize,
    pub idle: u64,
}

impl SidecarOptions {
    pub fn new(board: SocketAddr) -> Self {
        Self {
            board,
            secret: "0123456789abcdef0123456789abcdef0123456789abcdef".to_owned(),
            max_connections: 16,
            idle: 60,
        }
    }

    pub fn secret(mut self, secret: &str) -> Self {
        self.secret = secret.to_owned();
        self
    }

    pub fn max_connections(mut self, max: usize) -> Self {
        self.max_connections = max;
        self
    }

    pub fn idle(mut self, seconds: u64) -> Self {
        self.idle = seconds;
        self
    }
}

pub struct Sidecar {
    child: Child,
    dir: TempDir,
    id: EndpointId,
    addr: SocketAddr,
}

impl Sidecar {
    pub fn start(bin: &Path, options: SidecarOptions) -> Self {
        let dir = TempDir::new();
        let secret = dir.path().join("board.peer");
        fs::write(&secret, format!("{}\n", options.secret)).unwrap();
        let keys = dir.path().join("iroh");
        let mut child = Command::new(bin)
            .args([
                "--board",
                &options.board.to_string(),
                "--relay",
                "none",
                "--secret-file",
                secret.to_str().unwrap(),
                "--key-dir",
                keys.to_str().unwrap(),
                "--max-connections",
                &options.max_connections.to_string(),
                "--idle",
                &options.idle.to_string(),
            ])
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        let stdout = child.stdout.take().unwrap();
        let (sent, line) = mpsc::channel();
        thread::spawn(move || {
            let mut reader = BufReader::new(stdout);
            let mut first = String::new();
            let read = reader.read_line(&mut first).map(|_| first);
            let _ = sent.send(read);
        });
        let line = line
            .recv_timeout(READY_WAIT)
            .expect("hand-iroh printed no ready line")
            .expect("hand-iroh stdout is not text");
        let id = line
            .trim_end()
            .split(' ')
            .find_map(|field| field.strip_prefix("id="))
            .expect("hand-iroh printed no id");
        let id = id.parse::<EndpointId>().unwrap();
        let port = udp_port(child.id()).expect("hand-iroh bound no IPv4 socket");
        let addr = SocketAddr::from(([127, 0, 0, 1], port));
        Self {
            child,
            dir,
            id,
            addr,
        }
    }

    pub fn id(&self) -> EndpointId {
        self.id
    }

    pub fn addr(&self) -> SocketAddr {
        self.addr
    }

    pub fn target(&self) -> EndpointAddr {
        EndpointAddr::new(self.id).with_ip_addr(self.addr)
    }
}

impl Drop for Sidecar {
    fn drop(&mut self) {
        drop(self.child.stdin.take());
        let _ = self.child.wait();
    }
}

fn udp_port(pid: u32) -> Option<u16> {
    let deadline = Instant::now() + STARTUP_WAIT;
    loop {
        if let Some(port) = udp_port_once(pid) {
            return Some(port);
        }
        if Instant::now() > deadline {
            return None;
        }
        thread::sleep(Duration::from_millis(20));
    }
}

#[cfg(target_os = "linux")]
fn udp_port_once(pid: u32) -> Option<u16> {
    let inodes: Vec<String> = fs::read_dir(format!("/proc/{pid}/fd"))
        .ok()?
        .filter_map(|entry| fs::read_link(entry.ok()?.path()).ok())
        .filter_map(|link| {
            let link = link.to_str()?;
            Some(link.strip_prefix("socket:[")?.strip_suffix(']')?.to_owned())
        })
        .collect();
    let table = fs::read_to_string("/proc/net/udp").ok()?;
    for line in table.lines().skip(1) {
        let columns: Vec<&str> = line.split_whitespace().collect();
        if columns.len() > 9 && inodes.iter().any(|inode| inode == columns[9]) {
            let local = columns[1];
            let port = u16::from_str_radix(local.rsplit_once(':')?.1, 16).ok()?;
            return Some(port);
        }
    }
    None
}

#[cfg(not(target_os = "linux"))]
fn udp_port_once(_: u32) -> Option<u16> {
    None
}

pub async fn endpoint() -> Endpoint {
    Endpoint::builder(presets::Minimal)
        .relay_mode(RelayMode::Disabled)
        .portmapper_config(PortmapperConfig::Disabled)
        .clear_ip_transports()
        .bind_addr("127.0.0.1:0")
        .unwrap()
        .alpns(vec![ALPN.to_vec()])
        .bind()
        .await
        .unwrap()
}

pub async fn dialer(target: EndpointAddr) -> Dialer {
    let endpoint = endpoint().await;
    Dialer::new(endpoint, Some(target))
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Behavior {
    Http,
    HalfClose,
    Hold,
    Reset,
    Close,
    Drain,
    AnswerThenClose,
}

pub struct Board {
    addr: SocketAddr,
    accepted: Arc<AtomicUsize>,
    release: watch::Sender<bool>,
}

impl Board {
    pub async fn start(secret: &str, behavior: Behavior) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let accepted = Arc::new(AtomicUsize::new(0));
        let (release, released) = watch::channel(false);
        let secret = secret.to_owned();
        let count = accepted.clone();
        tokio::spawn(async move {
            while let Ok((stream, _)) = listener.accept().await {
                count.fetch_add(1, Ordering::SeqCst);
                let secret = secret.clone();
                let released = released.clone();
                tokio::spawn(async move {
                    serve(stream, &secret, behavior, released).await;
                });
            }
        });
        Self {
            addr,
            accepted,
            release,
        }
    }

    pub fn addr(&self) -> SocketAddr {
        self.addr
    }

    pub fn accepted(&self) -> usize {
        self.accepted.load(Ordering::SeqCst)
    }

    pub fn release_all(&self) {
        let _ = self.release.send(true);
    }
}

async fn serve(
    mut stream: tokio::net::TcpStream,
    secret: &str,
    behavior: Behavior,
    mut released: watch::Receiver<bool>,
) {
    let mut line = Vec::new();
    loop {
        let mut byte = [0u8; 1];
        match tokio::time::timeout(Duration::from_secs(5), stream.read(&mut byte)).await {
            Ok(Ok(1)) => {
                line.push(byte[0]);
                if byte[0] == b'\n' || line.len() > 256 {
                    break;
                }
            }
            _ => return,
        }
    }
    if !valid_preface(&line, secret) {
        return;
    }
    match behavior {
        Behavior::Reset => {
            if let Ok(stream) = stream.into_std() {
                let _ = socket2::SockRef::from(&stream).set_linger(Some(Duration::ZERO));
            }
            return;
        }
        Behavior::Close | Behavior::Hold | Behavior::Drain => {}
        Behavior::AnswerThenClose => {
            let body = b"accepted before the body\n";
            let response = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );
            let mut out = response.into_bytes();
            out.extend_from_slice(body);
            let _ = stream.write_all(&out).await;
            let _ = stream.shutdown().await;
            return;
        }
        Behavior::Http | Behavior::HalfClose => {
            let mut request = Vec::new();
            if behavior == Behavior::HalfClose {
                let _ = stream.read_to_end(&mut request).await;
            } else {
                let mut chunk = [0u8; 1024];
                while !request.windows(4).any(|window| window == b"\r\n\r\n") {
                    match stream.read(&mut chunk).await {
                        Ok(0) | Err(_) => break,
                        Ok(n) => request.extend_from_slice(&chunk[..n]),
                    }
                }
            }
            let body = b"hello from the board\n";
            let response = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );
            let mut out = response.into_bytes();
            out.extend_from_slice(body);
            let _ = stream.write_all(&out).await;
            let _ = stream.shutdown().await;
            return;
        }
    }
    match behavior {
        Behavior::Hold => {
            let mut chunk = [0u8; 1024];
            let _ = stream.read(&mut chunk).await;
            let _ = stream
                .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nx")
                .await;
            while !*released.borrow() {
                if released.changed().await.is_err() {
                    break;
                }
            }
        }
        Behavior::Drain => {
            let mut chunk = [0u8; 1024];
            while matches!(stream.read(&mut chunk).await, Ok(n) if n > 0) {}
        }
        _ => {}
    }
}

fn valid_preface(line: &[u8], secret: &str) -> bool {
    let Ok(text) = std::str::from_utf8(line) else {
        return false;
    };
    let Some(text) = text.strip_suffix('\n') else {
        return false;
    };
    let fields: Vec<&str> = text.split(' ').collect();
    fields.len() == 4
        && fields[0] == "HAND-PEER"
        && fields[1] == "1"
        && (16..=64).contains(&fields[2].len())
        && fields[2]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'z').contains(&byte))
        && fields[3] == secret
}

pub async fn proxy(dialer: Dialer) -> (TempDir, u16) {
    let dir = TempDir::new();
    let (proxy, listeners) = Proxy::bind(dir.path()).await.unwrap();
    for listener in listeners {
        tokio::spawn(hand_android_core::proxy::serve(listener, dialer.clone()));
    }
    (dir, proxy.port())
}

pub async fn request(port: u16, path: &str) -> std::io::Result<(Vec<u8>, Duration)> {
    let mut stream = tokio::net::TcpStream::connect(("127.0.0.1", port)).await?;
    let started = Instant::now();
    stream
        .write_all(format!("GET {path} HTTP/1.1\r\nHost: localhost\r\n\r\n").as_bytes())
        .await?;
    let mut first = [0u8; 1];
    let read = stream.read(&mut first).await?;
    let ttfb = started.elapsed();
    let mut body = Vec::new();
    if read > 0 {
        body.push(first[0]);
        stream.read_to_end(&mut body).await?;
    }
    Ok((body, ttfb))
}

pub fn body(response: &[u8]) -> &[u8] {
    let split = response
        .windows(4)
        .position(|window| window == b"\r\n\r\n")
        .expect("no header terminator");
    &response[split + 4..]
}
