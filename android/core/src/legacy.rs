use std::fs;
use std::io;
use std::path::Path;

use crate::dialer::parse_target;

const HOST_FILE: &str = "laptop";
const RELAY_FILE: &str = "laptop-relay";
const RELAY_MARK: &str = "\t";

pub(crate) fn read_host(dir: &Path) -> Option<(String, Option<String>)> {
    let text = fs::read_to_string(dir.join(HOST_FILE)).ok()?;
    let mut lines = text.lines();
    let line = lines.next()?;
    let endpoint = line.trim();
    if !valid_endpoint(endpoint) {
        return None;
    }
    let legacy = lines
        .next()
        .map(str::trim)
        .filter(|relay| parse_target(endpoint, Some(relay)).is_ok())
        .map(str::to_owned);
    let relay = if line.ends_with(RELAY_MARK) {
        read_relay(dir, endpoint).or(legacy)
    } else {
        legacy
    };
    Some((endpoint.to_owned(), relay))
}

pub(crate) fn remove(dir: &Path) -> io::Result<()> {
    let host = remove_if_present(&dir.join(HOST_FILE));
    let relay = remove_if_present(&dir.join(RELAY_FILE));
    host.and(relay)
}

fn read_relay(dir: &Path, endpoint: &str) -> Option<String> {
    let text = fs::read_to_string(dir.join(RELAY_FILE)).ok()?;
    let mut lines = text.lines();
    if lines.next().map(str::trim) != Some(endpoint) {
        return None;
    }
    let relay = lines.next().map(str::trim)?;
    parse_target(endpoint, Some(relay)).ok()?;
    Some(relay.to_owned())
}

fn remove_if_present(path: &Path) -> io::Result<()> {
    match fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error),
    }
}

fn valid_endpoint(endpoint: &str) -> bool {
    endpoint.len() == 64
        && endpoint
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        && endpoint.parse::<iroh::EndpointId>().is_ok()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::testutil::TempDir;

    const FIRST: &str = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6";
    const SECOND: &str = "d6e458a66697094c1bc7b54cb83fea66a8db57c49f37514944579712cd7e43c5";
    const RELAY: &str = "https://relay.example.test:7842/";

    fn write(dir: &TempDir, host: &str, relay: Option<&str>) {
        fs::write(dir.path().join(HOST_FILE), host).unwrap();
        if let Some(relay) = relay {
            fs::write(dir.path().join(RELAY_FILE), relay).unwrap();
        }
    }

    #[test]
    fn a_marked_host_reads_the_relay_written_beside_it() {
        let dir = TempDir::new();
        write(
            &dir,
            &format!("{FIRST}\t\n"),
            Some(&format!("{FIRST}\n{RELAY}\n")),
        );
        assert_eq!(
            read_host(dir.path()),
            Some((FIRST.to_owned(), Some(RELAY.to_owned())))
        );
    }

    #[test]
    fn a_two_line_host_file_from_a_previous_build_keeps_its_relay() {
        let dir = TempDir::new();
        write(&dir, &format!("{FIRST}\n{RELAY}\n"), None);
        assert_eq!(
            read_host(dir.path()),
            Some((FIRST.to_owned(), Some(RELAY.to_owned())))
        );
    }

    #[test]
    fn a_relay_file_for_another_host_is_not_read() {
        let dir = TempDir::new();
        write(
            &dir,
            &format!("{FIRST}\t\n"),
            Some(&format!("{SECOND}\n{RELAY}\n")),
        );
        assert_eq!(read_host(dir.path()), Some((FIRST.to_owned(), None)));
    }

    #[test]
    fn a_crlf_line_ending_keeps_the_relay_mark() {
        let dir = TempDir::new();
        write(
            &dir,
            &format!("{FIRST}\t\r\n"),
            Some(&format!("{FIRST}\r\n{RELAY}\r\n")),
        );
        assert_eq!(
            read_host(dir.path()),
            Some((FIRST.to_owned(), Some(RELAY.to_owned())))
        );
    }

    #[test]
    fn a_space_after_or_before_the_newline_drops_the_relay() {
        for host in [format!("{FIRST}\t \n"), format!("{FIRST} \n")] {
            let dir = TempDir::new();
            write(&dir, &host, Some(&format!("{FIRST}\n{RELAY}\n")));
            assert_eq!(read_host(dir.path()), Some((FIRST.to_owned(), None)));
        }
    }

    #[test]
    fn a_host_that_is_not_an_endpoint_id_is_not_read() {
        let dir = TempDir::new();
        write(&dir, "not-an-endpoint\n", None);
        assert_eq!(read_host(dir.path()), None);
    }

    #[test]
    fn removing_deletes_both_files_and_a_missing_one_is_fine() {
        let dir = TempDir::new();
        write(
            &dir,
            &format!("{FIRST}\t\n"),
            Some(&format!("{FIRST}\n{RELAY}\n")),
        );
        remove(dir.path()).unwrap();
        remove(dir.path()).unwrap();
        assert!(!dir.path().join(HOST_FILE).exists());
        assert!(!dir.path().join(RELAY_FILE).exists());
    }
}
