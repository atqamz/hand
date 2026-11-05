use std::fmt;
use std::fs;
use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::time::Duration;

use iroh::RelayUrl;

pub const VERSION: &str = env!("CARGO_PKG_VERSION");
pub const IROH_VERSION: &str = "1.4.1";
pub const USAGE: &str = "usage: hand-iroh --board 127.0.0.1:PORT --secret-file PATH --key-dir DIR [--alpn NAME] [--relay default|none|URL] [--max-connections N] [--idle SECONDS] | --version";
const SECRET_MIN: usize = 16;
const SECRET_MAX: usize = 128;
const ALPN_DEFAULT: &str = "hand-board";
const MAX_CONNECTIONS_DEFAULT: usize = 16;
const IDLE_DEFAULT: Duration = Duration::from_secs(60);

#[derive(Clone, PartialEq, Eq)]
pub struct Secret(String);

impl Secret {
    pub fn parse(text: &str) -> Result<Self, String> {
        let text = text.trim();
        let printable = text.bytes().all(|b| b.is_ascii_graphic());
        if !printable || !(SECRET_MIN..=SECRET_MAX).contains(&text.len()) {
            return Err(format!(
                "the secret must be {SECRET_MIN} to {SECRET_MAX} printable characters without spaces"
            ));
        }
        Ok(Self(text.to_owned()))
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Debug for Secret {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("Secret(..)")
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Relay {
    Default,
    None,
    Custom(RelayUrl),
}

#[derive(Debug, Clone)]
pub struct Config {
    pub board: SocketAddr,
    pub secret: Secret,
    pub key_dir: PathBuf,
    pub alpn: String,
    pub relay: Relay,
    pub max_connections: usize,
    pub idle: Duration,
}

#[derive(Debug)]
pub enum Command {
    Run(Config),
    Version,
}

pub fn version_line() -> String {
    format!("hand-iroh {VERSION} iroh {IROH_VERSION}")
}

pub fn parse(args: impl IntoIterator<Item = String>) -> Result<Command, String> {
    let mut args = args.into_iter();
    let (mut board, mut secret_file, mut key_dir) = (None, None, None);
    let mut alpn = ALPN_DEFAULT.to_owned();
    let mut relay = Relay::Default;
    let mut max_connections = MAX_CONNECTIONS_DEFAULT;
    let mut idle = IDLE_DEFAULT;
    while let Some(flag) = args.next() {
        if flag == "--version" {
            return Ok(Command::Version);
        }
        if !matches!(
            flag.as_str(),
            "--board"
                | "--secret-file"
                | "--key-dir"
                | "--alpn"
                | "--relay"
                | "--max-connections"
                | "--idle"
        ) {
            return Err(match flag.strip_prefix("--") {
                Some(rest) => format!("unknown flag --{}", rest.split('=').next().unwrap_or("")),
                None => "unexpected argument".to_owned(),
            });
        }
        let value = args.next().ok_or_else(|| format!("{flag} needs a value"))?;
        match flag.as_str() {
            "--board" => board = Some(loopback(&value)?),
            "--secret-file" => secret_file = Some(PathBuf::from(value)),
            "--key-dir" => key_dir = Some(PathBuf::from(value)),
            "--alpn" => alpn = name(&value)?,
            "--relay" => relay = relay_mode(&value)?,
            "--max-connections" => max_connections = positive(&flag, &value)?,
            _ => idle = Duration::from_secs(positive(&flag, &value)? as u64),
        }
    }
    let secret_file = secret_file.ok_or("--secret-file is required")?;
    Ok(Command::Run(Config {
        board: board.ok_or("--board is required")?,
        secret: read_secret(&secret_file)?,
        key_dir: key_dir.ok_or("--key-dir is required")?,
        alpn,
        relay,
        max_connections,
        idle,
    }))
}

fn loopback(value: &str) -> Result<SocketAddr, String> {
    match value.parse::<SocketAddr>() {
        Ok(addr) if addr.ip().is_loopback() && addr.port() != 0 => Ok(addr),
        _ => Err(
            "--board must be a loopback IP address and a port, such as 127.0.0.1:7778".to_owned(),
        ),
    }
}

fn name(value: &str) -> Result<String, String> {
    if value.is_empty() || value.len() > 255 {
        return Err("--alpn must be 1 to 255 bytes".to_owned());
    }
    Ok(value.to_owned())
}

fn relay_mode(value: &str) -> Result<Relay, String> {
    match value {
        "default" => Ok(Relay::Default),
        "none" => Ok(Relay::None),
        _ => value
            .parse()
            .map(Relay::Custom)
            .map_err(|_| "--relay must be default, none or a relay URL".to_owned()),
    }
}

fn positive(flag: &str, value: &str) -> Result<usize, String> {
    match value.parse::<usize>() {
        Ok(n) if n > 0 => Ok(n),
        _ => Err(format!("{flag} must be a whole number above 0")),
    }
}

fn read_secret(path: &Path) -> Result<Secret, String> {
    let text = fs::read_to_string(path).map_err(|e| {
        format!(
            "cannot read the secret file {}: {}",
            path.display(),
            e.kind()
        )
    })?;
    Secret::parse(&text).map_err(|e| format!("{}: {e}", path.display()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::testutil::TempDir;

    const SECRET: &str = "0123456789abcdef0123456789abcdef0123456789abcdef";

    fn parse_all(args: &[&str]) -> Result<Command, String> {
        parse(args.iter().map(|a| a.to_string()))
    }

    fn config(args: &[&str]) -> Result<Config, String> {
        match parse_all(args)? {
            Command::Run(config) => Ok(config),
            Command::Version => panic!("expected a run"),
        }
    }

    fn run_with(dir: &TempDir, extra: &[&str]) -> Result<Config, String> {
        let secret = dir.file("board.peer", &format!("{SECRET}\n"));
        let mut args = vec![
            "--board",
            "127.0.0.1:7778",
            "--secret-file",
            secret.to_str().unwrap(),
            "--key-dir",
            dir.path().to_str().unwrap(),
        ];
        args.extend_from_slice(extra);
        config(&args)
    }

    #[test]
    fn version_needs_nothing_else() {
        assert!(matches!(parse_all(&["--version"]), Ok(Command::Version)));
        assert_eq!(
            version_line(),
            format!("hand-iroh {} iroh 1.4.1", env!("CARGO_PKG_VERSION"))
        );
    }

    #[test]
    fn defaults_are_the_documented_ones() {
        let dir = TempDir::new();
        let config = run_with(&dir, &[]).unwrap();
        assert_eq!(config.board, "127.0.0.1:7778".parse().unwrap());
        assert_eq!(config.secret.as_str(), SECRET);
        assert_eq!(config.alpn, "hand-board");
        assert_eq!(config.relay, Relay::Default);
        assert_eq!(config.max_connections, 16);
        assert_eq!(config.idle, Duration::from_secs(60));
    }

    #[test]
    fn flags_override_the_defaults() {
        let dir = TempDir::new();
        let config = run_with(
            &dir,
            &[
                "--alpn",
                "other",
                "--relay",
                "https://relay.example.test",
                "--max-connections",
                "3",
                "--idle",
                "9",
            ],
        )
        .unwrap();
        assert_eq!(config.alpn, "other");
        assert!(
            matches!(config.relay, Relay::Custom(url) if url.to_string() == "https://relay.example.test/")
        );
        assert_eq!(config.max_connections, 3);
        assert_eq!(config.idle, Duration::from_secs(9));
        assert_eq!(
            run_with(&dir, &["--relay", "none"]).unwrap().relay,
            Relay::None
        );
        assert_eq!(
            run_with(&dir, &["--relay", "default"]).unwrap().relay,
            Relay::Default
        );
    }

    #[test]
    fn the_secret_never_comes_from_the_command_line() {
        for args in [
            vec!["--secret", "hunter2hunter2hunter2"],
            vec!["--secret=hunter2hunter2hunter2"],
            vec!["hunter2hunter2hunter2"],
        ] {
            let error = parse_all(&args).unwrap_err();
            assert!(!error.contains("hunter2"), "{error}");
        }
        assert_eq!(
            parse_all(&["--secret", "x"]).unwrap_err(),
            "unknown flag --secret"
        );
    }

    #[test]
    fn the_secret_file_is_required_and_read() {
        let dir = TempDir::new();
        let error = parse_all(&["--board", "127.0.0.1:7778", "--key-dir", "k"]).unwrap_err();
        assert_eq!(error, "--secret-file is required");
        let missing = dir.path().join("absent");
        let error = parse_all(&[
            "--board",
            "127.0.0.1:7778",
            "--secret-file",
            missing.to_str().unwrap(),
            "--key-dir",
            "k",
        ])
        .unwrap_err();
        assert!(error.contains("absent"), "{error}");
    }

    #[test]
    fn the_secret_file_must_hold_a_usable_secret() {
        let dir = TempDir::new();
        let too_long = "a".repeat(129);
        for bad in [
            "",
            "   \n",
            "short",
            "has a space in the middle of it",
            "tab\tseparated-secret-x",
            too_long.as_str(),
            "naïve-secret-with-accent-x",
        ] {
            let path = dir.file("bad", bad);
            let error = parse_all(&[
                "--board",
                "127.0.0.1:7778",
                "--secret-file",
                path.to_str().unwrap(),
                "--key-dir",
                "k",
            ])
            .unwrap_err();
            assert!(error.contains("printable characters"), "{error}");
            assert!(
                bad.trim().is_empty() || !error.contains(bad.trim()),
                "{error}"
            );
        }
    }

    #[test]
    fn the_secret_hides_from_debug_output() {
        let dir = TempDir::new();
        let config = run_with(&dir, &[]).unwrap();
        assert!(!format!("{config:?}").contains(SECRET));
    }

    #[test]
    fn the_board_must_be_a_loopback_address_and_port() {
        let dir = TempDir::new();
        for bad in [
            "0.0.0.0:7778",
            "192.168.1.5:7778",
            "[::]:7778",
            "[fe80::1]:7778",
            "localhost:7778",
            "127.0.0.1",
            "127.0.0.1:0",
            "",
        ] {
            let secret = dir.file("board.peer", SECRET);
            let error = parse_all(&[
                "--board",
                bad,
                "--secret-file",
                secret.to_str().unwrap(),
                "--key-dir",
                "k",
            ])
            .unwrap_err();
            assert!(
                error.starts_with("--board must be a loopback"),
                "{bad}: {error}"
            );
        }
        for good in ["127.0.0.1:7778", "[::1]:7778", "127.0.0.2:80"] {
            let secret = dir.file("board.peer", SECRET);
            assert!(
                parse_all(&[
                    "--board",
                    good,
                    "--secret-file",
                    secret.to_str().unwrap(),
                    "--key-dir",
                    "k"
                ])
                .is_ok(),
                "{good}"
            );
        }
    }

    #[test]
    fn bad_values_are_refused_by_name() {
        let dir = TempDir::new();
        for (flag, value) in [
            ("--relay", "nonsense"),
            ("--max-connections", "0"),
            ("--max-connections", "many"),
            ("--idle", "0"),
            ("--idle", "-1"),
            ("--alpn", ""),
        ] {
            let error = run_with(&dir, &[flag, value]).unwrap_err();
            assert!(error.starts_with(flag), "{flag} {value}: {error}");
        }
        assert_eq!(
            run_with(&dir, &["--idle"]).unwrap_err(),
            "--idle needs a value"
        );
    }

    #[test]
    fn required_flags_are_named() {
        let dir = TempDir::new();
        let secret = dir.file("board.peer", SECRET);
        let secret = secret.to_str().unwrap();
        assert_eq!(
            parse_all(&["--secret-file", secret, "--key-dir", "k"]).unwrap_err(),
            "--board is required"
        );
        assert_eq!(
            parse_all(&["--board", "127.0.0.1:7778", "--secret-file", secret]).unwrap_err(),
            "--key-dir is required"
        );
    }
}
