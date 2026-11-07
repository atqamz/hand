use iroh::EndpointAddr;
use std::collections::HashSet;
use std::fmt;

use crate::dialer::parse_target;

pub const MAX_LEN: usize = 213;
pub const LINK_REJECTED: &str = "This is not a Hand pairing code. Show a new one on your laptop.";
pub const LINK_NEEDS_UPDATE: &str = "This code needs a newer Hand app. Update the app.";
pub const CAMERA_REFUSED: &str = "Allow the camera to scan the code, or paste the link.";

const CODE_ALPHABET: &[u8] = b"0123456789ABCDEFGHJKMNPQRSTVWXYZ";
const UNRESERVED: &[u8] = b"-._~";

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Link {
    pub version: u8,
    pub endpoint_id: String,
    pub fleet: String,
    pub code: String,
    pub relay: Option<String>,
}

impl Link {
    pub fn confirmation(&self) -> String {
        format!(
            "{}…{}",
            &self.endpoint_id[..4],
            &self.endpoint_id[self.endpoint_id.len() - 4..]
        )
    }

    pub fn open_path(&self) -> String {
        format!("/{}/pair#{}", self.fleet, self.code)
    }

    pub fn open_url(&self, port: u16) -> String {
        format!("http://localhost:{port}{}", self.open_path())
    }

    pub fn target(&self) -> Result<EndpointAddr, String> {
        parse_target(&self.endpoint_id, self.relay.as_deref())
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Reject {
    TooLong,
    Characters,
    Scheme,
    NoFragment,
    Query,
    Target,
    VersionMissing,
    VersionUnsupported,
    Fragment,
    DuplicateKey,
    MissingKey,
    EndpointId,
    Fleet,
    Code,
    Relay,
}

impl Reject {
    pub fn reason(self) -> &'static str {
        match self {
            Self::TooLong => "too_long",
            Self::Characters => "characters",
            Self::Scheme => "scheme",
            Self::NoFragment => "no_fragment",
            Self::Query => "query",
            Self::Target => "target",
            Self::VersionMissing => "version_missing",
            Self::VersionUnsupported => "version_unsupported",
            Self::Fragment => "fragment",
            Self::DuplicateKey => "duplicate_key",
            Self::MissingKey => "missing_key",
            Self::EndpointId => "endpoint_id",
            Self::Fleet => "fleet",
            Self::Code => "code",
            Self::Relay => "relay",
        }
    }

    pub fn message(self) -> &'static str {
        match self {
            Self::VersionUnsupported => LINK_NEEDS_UPDATE,
            _ => LINK_REJECTED,
        }
    }
}

impl fmt::Display for Reject {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.reason())
    }
}

impl std::error::Error for Reject {}

pub fn parse_link(text: &str) -> Result<Link, Reject> {
    parse_link_with(text, |_| true)
}

pub fn parse_link_with(text: &str, endpoint_ok: impl Fn(&str) -> bool) -> Result<Link, Reject> {
    if text.len() > MAX_LEN {
        return Err(Reject::TooLong);
    }
    if !text.bytes().all(|byte| (0x21..=0x7e).contains(&byte)) {
        return Err(Reject::Characters);
    }
    if !text.starts_with("handboard://") {
        return Err(Reject::Scheme);
    }
    let Some(hash) = text.find('#') else {
        return Err(Reject::NoFragment);
    };
    let before = &text[..hash];
    if before.contains('?') {
        return Err(Reject::Query);
    }
    if before != "handboard://c" {
        return Err(Reject::Target);
    }
    let fragment = &text[hash + 1..];
    if !fragment.starts_with("v=") {
        return Err(Reject::VersionMissing);
    }
    if fragment[2..].split('&').next() != Some("1") {
        return Err(Reject::VersionUnsupported);
    }

    let mut pairs = Vec::new();
    for pair in fragment.split('&') {
        let Some((key, value)) = pair.split_once('=') else {
            return Err(Reject::Fragment);
        };
        if value.contains('=') || !valid_key(key) || !valid_value(value) {
            return Err(Reject::Fragment);
        }
        pairs.push((key, value));
    }

    let mut seen = HashSet::new();
    for (key, _) in &pairs {
        if !seen.insert(*key) {
            return Err(Reject::DuplicateKey);
        }
    }

    let field = |key: &str| {
        pairs
            .iter()
            .find_map(|(name, value)| (*name == key).then_some(*value))
    };
    let (Some(endpoint_id), Some(fleet), Some(code)) = (field("e"), field("f"), field("c")) else {
        return Err(Reject::MissingKey);
    };

    if !valid_endpoint(endpoint_id) || !endpoint_ok(endpoint_id) {
        return Err(Reject::EndpointId);
    }
    if !valid_fleet(fleet) {
        return Err(Reject::Fleet);
    }
    if !valid_code(code) {
        return Err(Reject::Code);
    }
    let relay = match field("r") {
        Some(value) => Some(decode_relay(value).ok_or(Reject::Relay)?),
        None => None,
    };

    Ok(Link {
        version: 1,
        endpoint_id: endpoint_id.to_owned(),
        fleet: fleet.to_owned(),
        code: code.to_owned(),
        relay,
    })
}

fn valid_key(key: &str) -> bool {
    let bytes = key.as_bytes();
    (1..=16).contains(&bytes.len())
        && bytes[0].is_ascii_lowercase()
        && bytes[1..]
            .iter()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || *byte == b'_')
}

fn valid_value(value: &str) -> bool {
    !value.is_empty()
        && value.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'-' | b'_' | b'~' | b'%')
        })
}

fn valid_endpoint(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn valid_fleet(value: &str) -> bool {
    value.len() == 13
        && value.starts_with('f')
        && value[1..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn valid_code(value: &str) -> bool {
    value.len() == 10 && value.bytes().all(|byte| CODE_ALPHABET.contains(&byte))
}

fn decode_relay(value: &str) -> Option<String> {
    let bytes = value.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'%' {
            let high = upper_hex(*bytes.get(index + 1)?)?;
            let low = upper_hex(*bytes.get(index + 2)?)?;
            let byte = high * 16 + low;
            if byte.is_ascii_alphanumeric() || UNRESERVED.contains(&byte) {
                return None;
            }
            out.push(byte);
            index += 3;
        } else {
            out.push(bytes[index]);
            index += 1;
        }
    }
    let text = String::from_utf8(out).ok()?;
    valid_relay(&text).then_some(text)
}

fn upper_hex(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'A'..=b'F' => Some(byte - b'A' + 10),
        _ => None,
    }
}

fn valid_relay(url: &str) -> bool {
    let Some(rest) = url.strip_prefix("https://") else {
        return false;
    };
    if rest.is_empty() || rest.contains('@') || rest.contains('?') || rest.contains('#') {
        return false;
    }
    let authority = match rest.split_once('/') {
        Some((authority, "")) => authority,
        Some(_) => return false,
        None => rest,
    };
    let (host, port) = match authority.rsplit_once(':') {
        Some((host, port)) => (host, Some(port)),
        None => (authority, None),
    };
    if !valid_host(host) {
        return false;
    }
    match port {
        Some(port) => valid_port(port),
        None => true,
    }
}

fn valid_host(host: &str) -> bool {
    let host = host.strip_suffix('.').unwrap_or(host);
    if host.is_empty() || host.len() > 253 {
        return false;
    }
    let labels: Vec<&str> = host.split('.').collect();
    if labels.iter().any(|label| {
        label.is_empty()
            || label.len() > 63
            || label.starts_with('-')
            || label.ends_with('-')
            || !label
                .bytes()
                .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
    }) {
        return false;
    }
    let last = labels[labels.len() - 1];
    if last.bytes().all(|byte| byte.is_ascii_digit()) {
        return false;
    }
    if let Some(hex) = last.strip_prefix("0x")
        && !hex.is_empty()
        && hex
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    {
        return false;
    }
    true
}

fn valid_port(port: &str) -> bool {
    if port.len() > 5 || port.starts_with('0') || port.is_empty() {
        return false;
    }
    let Ok(port) = port.parse::<u16>() else {
        return false;
    };
    port != 0
}

#[cfg(test)]
mod tests {
    use super::*;

    const ID: &str = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6";

    fn link() -> String {
        format!("handboard://c#v=1&e={ID}&f=f0123456789ab&c=K7QM3XRD2P")
    }

    #[test]
    fn the_confirmation_shows_the_ends_of_the_laptop_id_and_never_the_code() {
        let link = parse_link(&link()).unwrap();
        assert_eq!(link.confirmation(), "4d92…6ed6");
        assert!(!link.confirmation().contains(&link.code));
    }

    #[test]
    fn a_canonical_link_opens_the_pair_page_at_localhost() {
        let link = parse_link(&link()).unwrap();
        assert_eq!(
            link.open_url(51_234),
            "http://localhost:51234/f0123456789ab/pair#K7QM3XRD2P"
        );
    }

    #[test]
    fn the_length_cap_counts_bytes() {
        let mut long = link();
        long.push_str(&"a".repeat(MAX_LEN - long.len() + 1));
        assert_eq!(parse_link(&long).unwrap_err(), Reject::TooLong);
    }

    #[test]
    fn only_unknown_keys_may_carry_an_unused_value() {
        assert!(parse_link(&format!("{}&x_note=a%20b", link())).is_ok());
        assert_eq!(
            parse_link(&format!("{}&x_note=", link())).unwrap_err(),
            Reject::Fragment
        );
    }

    #[test]
    fn the_relay_is_percent_decoded_into_one_spelling() {
        let text = format!("{}&r=https%3A%2F%2Frelay.example.test%3A7842%2F", link());
        let parsed = parse_link(&text).unwrap();
        assert_eq!(
            parsed.relay.as_deref(),
            Some("https://relay.example.test:7842/")
        );
        let bad = format!("{}&r=https%3a%2f%2frelay.example.test%2F", link());
        assert_eq!(parse_link(&bad).unwrap_err(), Reject::Relay);
    }
}
