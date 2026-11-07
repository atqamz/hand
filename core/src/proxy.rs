use std::io;
use std::net::{Ipv4Addr, Ipv6Addr};
use std::path::Path;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::tcp::{OwnedReadHalf, OwnedWriteHalf};
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::mpsc::UnboundedSender;

use crate::dialer::Dialer;

const PORT_FILE: &str = "proxy.port";
const PORT_MIN: u16 = 49_152;
const PORT_MAX: u16 = 65_535;

pub struct Proxy {
    port: u16,
}

impl Proxy {
    pub fn port(&self) -> u16 {
        self.port
    }

    pub async fn bind(data_dir: &Path) -> io::Result<(Self, Vec<TcpListener>)> {
        let stored = read_port(data_dir);
        let mut candidate = stored;
        for _ in 0..20 {
            let port = candidate.unwrap_or_else(|| rand::random_range(PORT_MIN..=PORT_MAX));
            let v4 = match TcpListener::bind((Ipv4Addr::LOCALHOST, port)).await {
                Ok(listener) => listener,
                Err(error) if error.kind() == io::ErrorKind::AddrInUse => {
                    candidate = None;
                    continue;
                }
                Err(error) => return Err(error),
            };
            let port = v4.local_addr()?.port();
            let v6 = match TcpListener::bind((Ipv6Addr::LOCALHOST, port)).await {
                Ok(listener) => Some(listener),
                Err(error) if error.kind() == io::ErrorKind::AddrInUse => {
                    candidate = None;
                    continue;
                }
                Err(_) => None,
            };
            if stored != Some(port) {
                write_port(data_dir, port)?;
            }
            let mut listeners = vec![v4];
            listeners.extend(v6);
            return Ok((Self { port }, listeners));
        }
        Err(io::Error::new(
            io::ErrorKind::AddrInUse,
            "no free loopback port after 20 tries",
        ))
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Signal {
    Unpaired,
}

pub async fn serve(
    listener: TcpListener,
    dialer: Dialer,
    signals: Option<UnboundedSender<Signal>>,
) {
    while let Ok((tcp, _)) = listener.accept().await {
        tokio::spawn(handle(tcp, dialer.clone(), signals.clone()));
    }
}

async fn handle(mut tcp: TcpStream, dialer: Dialer, signals: Option<UnboundedSender<Signal>>) {
    let mut first = vec![0u8; 16 << 10];
    let n = match tcp.read(&mut first).await {
        Ok(0) | Err(_) => return,
        Ok(n) => n,
    };
    let (mut send, mut recv) = match dialer.open_with(&first[..n]).await {
        Ok(streams) => streams,
        Err(_) => {
            let _ = tcp.shutdown().await;
            return;
        }
    };
    let (mut tcp_read, mut tcp_write) = tcp.into_split();
    let (up, down) = {
        let mut up = std::pin::pin!(upload(&mut tcp_read, &mut send));
        let mut down = std::pin::pin!(download(&mut recv, &mut tcp_write, &signals));
        let (mut up_result, mut down_result) = (None, None);
        loop {
            if up_result.is_some() && down_result.is_some() {
                break;
            }
            tokio::select! {
                result = &mut up, if up_result.is_none() => {
                    up_result = Some(result);
                    if matches!(result, Side::Client) {
                        break;
                    }
                    if matches!(result, Side::Stream(code) if code != crate::BOARD_ERROR) {
                        break;
                    }
                }
                result = &mut down, if down_result.is_none() => {
                    down_result = Some(result);
                    if matches!(result, Side::Client | Side::Stream(_)) {
                        break;
                    }
                }
            }
        }
        (up_result, down_result)
    };
    if up == Some(Side::Client) || down == Some(Side::Client) {
        send.reset();
        recv.stop();
    }
    let _ = tcp_write.shutdown().await;
    drop(tcp_read);
    drop(tcp_write);
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Side {
    Done,
    Stream(u64),
    Client,
}

async fn upload(tcp: &mut OwnedReadHalf, send: &mut crate::dialer::StreamSend) -> Side {
    let mut buffer = vec![0u8; 16 << 10];
    loop {
        match tcp.read(&mut buffer).await {
            Ok(0) => {
                let _ = send.finish();
                return Side::Done;
            }
            Ok(n) => {
                if let Err(error) = send.write_all(&buffer[..n]).await {
                    return Side::Stream(crate::codes::write_code(&error).unwrap_or(0));
                }
            }
            Err(_) => return Side::Client,
        }
    }
}

async fn download(
    recv: &mut crate::dialer::StreamRecv,
    tcp: &mut OwnedWriteHalf,
    signals: &Option<UnboundedSender<Signal>>,
) -> Side {
    let mut buffer = vec![0u8; 16 << 10];
    let mut head = Vec::new();
    let mut watching = signals.is_some();
    loop {
        match recv.read(&mut buffer).await {
            Ok(0) => {
                let _ = tcp.shutdown().await;
                return Side::Done;
            }
            Ok(n) => {
                if watching {
                    head.extend_from_slice(&buffer[..n]);
                    if let Some(end) = header_end(&head) {
                        if unpaired_403(&head[..end])
                            && let Some(signals) = signals
                        {
                            let _ = signals.send(Signal::Unpaired);
                        }
                        watching = false;
                        head = Vec::new();
                    } else if head.len() >= 16 << 10 {
                        watching = false;
                        head = Vec::new();
                    }
                }
                if tcp.write_all(&buffer[..n]).await.is_err() {
                    return Side::Client;
                }
            }
            Err(error) => {
                return Side::Stream(crate::codes::read_code(&error).unwrap_or(0));
            }
        }
    }
}

fn header_end(head: &[u8]) -> Option<usize> {
    head.windows(4)
        .position(|window| window == b"\r\n\r\n")
        .map(|end| end + 4)
}

fn unpaired_403(head: &[u8]) -> bool {
    let Ok(text) = std::str::from_utf8(head) else {
        return false;
    };
    let mut lines = text.split("\r\n");
    let Some(status) = lines.next() else {
        return false;
    };
    if status.split_whitespace().nth(1) != Some("403") {
        return false;
    }
    lines.any(|line| {
        let Some((name, value)) = line.split_once(':') else {
            return false;
        };
        name.eq_ignore_ascii_case("x-hand-error") && {
            let value = value.trim().to_ascii_lowercase();
            value.contains("no longer paired") || value.contains("no%20longer%20paired")
        }
    })
}

fn read_port(data_dir: &Path) -> Option<u16> {
    let text = std::fs::read_to_string(data_dir.join(PORT_FILE)).ok()?;
    let port = text.trim().parse::<u16>().ok()?;
    (PORT_MIN..=PORT_MAX).contains(&port).then_some(port)
}

fn write_port(data_dir: &Path, port: u16) -> io::Result<()> {
    std::fs::write(data_dir.join(PORT_FILE), format!("{port}\n"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::testutil::TempDir;

    #[test]
    fn a_no_longer_paired_refusal_is_recognised_by_its_header() {
        let head = b"HTTP/1.1 403 Forbidden\r\nContent-Type: text/html\r\nX-Hand-Error: This%20phone%20is%20no%20longer%20paired%20with%20this%20fleet\r\n\r\n";
        assert!(unpaired_403(head));
        let other = b"HTTP/1.1 403 Forbidden\r\nX-Hand-Error: Pair%20this%20phone%20first\r\n\r\n";
        assert!(!unpaired_403(other));
        let ok = b"HTTP/1.1 200 OK\r\nX-Hand-Error: no longer paired\r\n\r\n";
        assert!(!unpaired_403(ok));
    }

    #[tokio::test]
    async fn keeps_the_port_across_a_restart() {
        let dir = TempDir::new();
        let (first, listeners) = Proxy::bind(dir.path()).await.unwrap();
        let port = first.port();
        assert!((PORT_MIN..=PORT_MAX).contains(&port));
        drop(listeners);
        let (second, listeners) = Proxy::bind(dir.path()).await.unwrap();
        assert_eq!(second.port(), port);
        drop(listeners);
    }
}
