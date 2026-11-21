#[path = "../src/testutil.rs"]
mod testutil;

use std::io::{BufRead, BufReader, Read, Write};
use std::net::{Shutdown, TcpListener, TcpStream, UdpSocket};
use std::path::{Path, PathBuf};
use std::process::{Child, ChildStdin, Command, ExitStatus, Stdio};
use std::sync::mpsc;
use std::thread;
use std::time::{Duration, Instant};

use testutil::TempDir;

const BIN: &str = env!("CARGO_BIN_EXE_hand-iroh");
const SECRET: &str = "feedfacefeedfacefeedfacefeedfacefeedfacefeedface";
const CODE: &str = "ABCDEFGH23";
const WAIT: Duration = Duration::from_secs(20);

struct Proc {
    child: Child,
    stdin: Option<ChildStdin>,
    lines: mpsc::Receiver<String>,
    stdout: thread::JoinHandle<Vec<String>>,
    stderr: thread::JoinHandle<String>,
}

fn spawn(args: &[&str]) -> Proc {
    let mut child = Command::new(BIN)
        .args(args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let stdin = child.stdin.take();
    let mut stderr = child.stderr.take().unwrap();
    let stderr = thread::spawn(move || {
        let mut text = String::new();
        stderr.read_to_string(&mut text).unwrap();
        text
    });
    let stdout = child.stdout.take().unwrap();
    let (sent, lines) = mpsc::channel();
    let stdout = thread::spawn(move || {
        let mut all = Vec::new();
        for line in BufReader::new(stdout).lines() {
            let line = line.unwrap();
            let _ = sent.send(line.clone());
            all.push(line);
        }
        all
    });
    Proc {
        child,
        stdin,
        lines,
        stdout,
        stderr,
    }
}

impl Proc {
    fn line(&self) -> String {
        self.lines.recv_timeout(WAIT).expect("no line on stdout")
    }

    fn line_starting(&self, prefix: &str) -> String {
        let deadline = Instant::now() + WAIT;
        loop {
            let line = self
                .lines
                .recv_timeout(WAIT)
                .expect("no such line on stdout");
            if line.starts_with(prefix) {
                return line;
            }
            assert!(Instant::now() < deadline, "no {prefix} line");
        }
    }

    fn wait(&mut self) -> ExitStatus {
        let deadline = Instant::now() + WAIT;
        loop {
            if let Some(status) = self.child.try_wait().unwrap() {
                return status;
            }
            if Instant::now() > deadline {
                self.child.kill().unwrap();
                panic!("the process did not exit");
            }
            thread::sleep(Duration::from_millis(20));
        }
    }

    fn stop(mut self) -> (ExitStatus, Vec<String>, String) {
        drop(self.stdin.take());
        let status = self.wait();
        (
            status,
            self.stdout.join().unwrap(),
            self.stderr.join().unwrap(),
        )
    }
}

struct Board {
    addr: String,
    prefaces: mpsc::Receiver<String>,
}

fn board() -> Board {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let addr = listener.local_addr().unwrap().to_string();
    let (sent, prefaces) = mpsc::channel();
    thread::spawn(move || {
        for stream in listener.incoming().flatten() {
            let sent = sent.clone();
            thread::spawn(move || {
                let mut writer = stream.try_clone().unwrap();
                let mut reader = BufReader::new(stream);
                let mut line = String::new();
                reader.read_line(&mut line).unwrap();
                let _ = sent.send(line);
                let mut chunk = [0u8; 4096];
                while let Ok(n) = reader.read(&mut chunk) {
                    if n == 0 || writer.write_all(&chunk[..n]).is_err() {
                        break;
                    }
                }
                let _ = writer.shutdown(Shutdown::Write);
            });
        }
    });
    Board { addr, prefaces }
}

fn free_udp_port() -> u16 {
    UdpSocket::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap()
        .port()
}

struct Server {
    proc: Proc,
    id: String,
    udp: String,
}

fn server(dir: &TempDir, board: &Board) -> Server {
    let secret = dir.file("board.peer", &format!("{SECRET}\n"));
    let udp = format!("127.0.0.1:{}", free_udp_port());
    let keys = dir.path().join("server-keys");
    let proc = spawn(&[
        "--board",
        &board.addr,
        "--relay",
        "none",
        "--bind",
        &udp,
        "--secret-file",
        secret.to_str().unwrap(),
        "--key-dir",
        keys.to_str().unwrap(),
    ]);
    let ready = proc.line();
    let id = ready
        .strip_prefix("ready id=")
        .and_then(|rest| rest.strip_suffix(" relay=none"))
        .unwrap_or_else(|| panic!("{ready:?}"))
        .to_owned();
    Server { proc, id, udp }
}

fn client(server: &Server, keys: &Path, extra: &[&str]) -> Proc {
    let mut args = vec![
        "client",
        "--host",
        &server.id,
        "--key-dir",
        keys.to_str().unwrap(),
        "--relay",
        "none",
        "--direct",
        &server.udp,
    ];
    args.extend_from_slice(extra);
    spawn(&args)
}

fn listening(client: &Proc) -> (String, String) {
    let line = client.line();
    let fields: Vec<&str> = line.split(' ').collect();
    assert_eq!(fields.len(), 3, "{line:?}");
    assert_eq!(fields[0], "listening");
    let addr = fields[1].strip_prefix("addr=127.0.0.1:").expect("addr");
    assert!(addr.parse::<u16>().unwrap() > 0);
    let id = fields[2].strip_prefix("id=").expect("id");
    assert_eq!(id.len(), 64);
    assert!(
        id.bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
    );
    (format!("127.0.0.1:{addr}"), id.to_owned())
}

fn ask(addr: &str, request: &[u8]) -> Vec<u8> {
    let mut tcp = TcpStream::connect(addr).unwrap();
    tcp.set_read_timeout(Some(WAIT)).unwrap();
    tcp.write_all(request).unwrap();
    tcp.shutdown(Shutdown::Write).unwrap();
    let mut answer = Vec::new();
    tcp.read_to_end(&mut answer).unwrap();
    answer
}

fn fresh() -> (TempDir, PathBuf) {
    let dir = TempDir::new();
    let keys = dir.path().join("remote").join("host-a");
    (dir, keys)
}

fn last_line(text: &str) -> &str {
    text.trim_end().lines().last().unwrap_or("")
}

#[test]
fn a_client_reaches_the_board_and_says_only_what_the_contract_lists() {
    let (dir, keys) = fresh();
    let board = board();
    let server = server(&dir, &board);
    let client = client(&server, &keys, &["--new-key"]);
    let (addr, id) = listening(&client);
    let request = format!("POST /f1/api/v1/pair HTTP/1.1\r\n\r\n{{\"code\":\"{CODE}\"}}");
    assert_eq!(ask(&addr, request.as_bytes()), request.as_bytes());
    let preface = board.prefaces.recv_timeout(WAIT).unwrap();
    assert_eq!(preface, format!("HAND-PEER 1 {id} {SECRET}\n"));
    assert_ne!(id, server.id);
    let (status, stdout, stderr) = client.stop();
    assert!(status.success(), "{status:?} {stderr}");
    assert_eq!(stderr, "");
    assert!(stdout[0].starts_with("listening addr="), "{stdout:?}");
    assert!(
        stdout[1..]
            .iter()
            .all(|line| matches!(line.as_str(), "state connecting" | "state connected")),
        "{stdout:?}"
    );
    assert!(stdout.contains(&"state connected".to_owned()), "{stdout:?}");
    assert!(!stdout.concat().contains(CODE));
    assert!(!stdout.concat().contains(SECRET));
    let (status, _, stderr) = server.proc.stop();
    assert!(status.success(), "{stderr}");
}

#[test]
fn the_code_and_the_secret_are_in_no_argument_and_no_log() {
    let (dir, keys) = fresh();
    let board = board();
    let server = server(&dir, &board);
    let client = client(&server, &keys, &["--new-key"]);
    let (addr, _) = listening(&client);
    #[cfg(target_os = "linux")]
    {
        let argv = std::fs::read(format!("/proc/{}/cmdline", client.child.id())).unwrap();
        let argv = String::from_utf8_lossy(&argv);
        assert!(!argv.contains(CODE) && !argv.contains(SECRET), "{argv}");
        let environ =
            std::fs::read(format!("/proc/{}/environ", client.child.id())).unwrap_or_default();
        assert!(!String::from_utf8_lossy(&environ).contains(CODE));
    }
    let request =
        format!("GET /pair?code={CODE} HTTP/1.1\r\nCookie: hand_phone_f1=1.{CODE}\r\n\r\n");
    assert_eq!(ask(&addr, request.as_bytes()), request.as_bytes());
    let (_, stdout, stderr) = client.stop();
    let logged = format!("{}{stderr}", stdout.concat());
    assert!(
        !logged.contains(CODE) && !logged.contains("hand_phone") && !logged.contains(SECRET),
        "{logged}"
    );
    server.proc.stop();
}

#[test]
fn a_client_does_not_start_without_a_key_unless_it_may_make_one() {
    let (dir, keys) = fresh();
    let board = board();
    let server = server(&dir, &board);
    let mut missing = client(&server, &keys, &[]);
    assert_eq!(missing.wait().code(), Some(3));
    let (_, stdout, stderr) = missing.stop();
    assert!(stdout.is_empty(), "{stdout:?}");
    assert!(last_line(&stderr).contains("--new-key"), "{stderr}");
    assert!(!keys.exists());
    std::fs::create_dir_all(&keys).unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&keys, std::fs::Permissions::from_mode(0o700)).unwrap();
    }
    let mut empty = client(&server, &keys, &[]);
    assert_eq!(empty.wait().code(), Some(3));
    assert!(!keys.join("hand-iroh.key").exists());
    server.proc.stop();
}

#[test]
fn a_new_key_is_private_and_the_next_start_keeps_the_id() {
    let (dir, keys) = fresh();
    let board = board();
    let server = server(&dir, &board);
    let first = client(&server, &keys, &["--new-key"]);
    let (_, id) = listening(&first);
    assert!(first.stop().0.success());
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mode = |p: &Path| std::fs::metadata(p).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode(&keys.join("hand-iroh.key")), 0o600);
        assert_eq!(mode(&keys), 0o700);
    }
    for extra in [&[][..], &["--new-key"]] {
        let again = client(&server, &keys, extra);
        assert_eq!(listening(&again).1, id);
        assert!(again.stop().0.success());
    }
    server.proc.stop();
}

#[cfg(unix)]
#[test]
fn a_key_others_can_read_or_a_wrong_sized_key_is_exit_3_with_the_reason_last() {
    use std::os::unix::fs::PermissionsExt;
    let (dir, keys) = fresh();
    let board = board();
    let server = server(&dir, &board);
    let first = client(&server, &keys, &["--new-key"]);
    listening(&first);
    assert!(first.stop().0.success());
    let key = keys.join("hand-iroh.key");
    for mode in [0o640, 0o604] {
        std::fs::set_permissions(&key, std::fs::Permissions::from_mode(mode)).unwrap();
        for extra in [&[][..], &["--new-key"]] {
            let mut refused = client(&server, &keys, extra);
            assert_eq!(refused.wait().code(), Some(3), "{mode:o}");
            let (_, stdout, stderr) = refused.stop();
            assert!(stdout.is_empty());
            assert!(last_line(&stderr).contains("chmod 600"), "{stderr}");
        }
    }
    for size in [31, 33] {
        std::fs::write(&key, vec![0u8; size]).unwrap();
        std::fs::set_permissions(&key, std::fs::Permissions::from_mode(0o600)).unwrap();
        let extra: &[&str] = if size == 31 { &[] } else { &["--new-key"] };
        let mut wrong = client(&server, &keys, extra);
        assert_eq!(wrong.wait().code(), Some(3), "{size}");
        let (_, _, stderr) = wrong.stop();
        assert!(last_line(&stderr).contains("not a 32-byte key"), "{stderr}");
    }
    server.proc.stop();
}

#[test]
fn two_clients_on_one_key_are_not_allowed() {
    let (dir, keys) = fresh();
    let board = board();
    let server = server(&dir, &board);
    let first = client(&server, &keys, &["--new-key"]);
    listening(&first);
    let mut second = client(&server, &keys, &["--new-key"]);
    assert_eq!(second.wait().code(), Some(1));
    let (_, stdout, stderr) = second.stop();
    assert!(stdout.is_empty());
    assert!(
        last_line(&stderr).contains("already uses the key"),
        "{stderr}"
    );
    assert!(first.stop().0.success());
    let third = client(&server, &keys, &[]);
    listening(&third);
    third.stop();
    server.proc.stop();
}

#[test]
fn a_usage_mistake_is_exit_2_and_never_echoes_a_code() {
    let (dir, keys) = fresh();
    let board = board();
    let server = server(&dir, &board);
    for extra in [
        vec!["--code", CODE],
        vec!["--link", CODE],
        vec!["--listen", "0.0.0.0:0"],
        vec!["--listen", "192.168.1.5:0"],
        vec!["--listen", "[::1]:0"],
    ] {
        let mut refused = client(&server, &keys, &extra);
        assert_eq!(refused.wait().code(), Some(2), "{extra:?}");
        let (_, stdout, stderr) = refused.stop();
        assert!(stdout.is_empty());
        assert!(!stderr.contains(CODE), "{stderr}");
    }
    assert!(!keys.exists());
    server.proc.stop();
}

#[test]
fn a_busy_listen_port_is_a_runtime_failure() {
    let (dir, keys) = fresh();
    let board = board();
    let server = server(&dir, &board);
    let taken = TcpListener::bind("127.0.0.1:0").unwrap();
    let addr = taken.local_addr().unwrap().to_string();
    let mut client = client(&server, &keys, &["--new-key", "--listen", &addr]);
    assert_eq!(client.wait().code(), Some(1));
    let (_, stdout, stderr) = client.stop();
    assert!(stdout.is_empty());
    assert!(last_line(&stderr).contains("cannot listen"), "{stderr}");
    server.proc.stop();
}

#[cfg(unix)]
#[test]
fn a_client_stops_cleanly_on_sigterm_and_sigint() {
    for signal in ["TERM", "INT"] {
        let (dir, keys) = fresh();
        let board = board();
        let server = server(&dir, &board);
        let mut client = client(&server, &keys, &["--new-key"]);
        listening(&client);
        let sent = Command::new("kill")
            .arg(format!("-{signal}"))
            .arg(client.child.id().to_string())
            .status()
            .unwrap();
        assert!(sent.success());
        let status = client.wait();
        assert!(status.success(), "{signal}: {status:?}");
        server.proc.stop();
    }
}

#[test]
fn a_dead_board_shows_as_board_down_on_the_second_try() {
    let (dir, keys) = fresh();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let dead = Board {
        addr: listener.local_addr().unwrap().to_string(),
        prefaces: mpsc::channel().1,
    };
    drop(listener);
    let server = server(&dir, &dead);
    let client = client(&server, &keys, &["--new-key"]);
    let (addr, _) = listening(&client);
    ask(&addr, b"GET / HTTP/1.1\r\n\r\n");
    client.line_starting("state connected");
    ask(&addr, b"GET / HTTP/1.1\r\n\r\n");
    assert_eq!(client.line_starting("state board_down"), "state board_down");
    client.stop();
    server.proc.stop();
}
