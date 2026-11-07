use iroh::EndpointId;

use crate::args::Secret;

pub fn line(peer: &EndpointId, secret: &Secret) -> Vec<u8> {
    format!("HAND-PEER 1 {peer} {}\n", secret.as_str()).into_bytes()
}

#[cfg(test)]
mod tests {
    use iroh::SecretKey;

    use super::*;

    fn hex(bytes: &[u8]) -> String {
        bytes.iter().map(|b| format!("{b:02x}")).collect()
    }

    #[test]
    fn line_is_what_the_board_reads() {
        let peer = SecretKey::from_bytes(&[7; 32]).public();
        let secret = Secret::parse(&"ab12".repeat(12)).unwrap();
        let expected = format!(
            "HAND-PEER 1 {} {}\n",
            hex(peer.as_bytes()),
            "ab12".repeat(12)
        );
        assert_eq!(line(&peer, &secret), expected.as_bytes());
    }

    #[test]
    fn line_fits_the_boards_grammar_for_the_longest_secret() {
        let peer = SecretKey::from_bytes(&[200; 32]).public();
        let secret = Secret::parse(&"z".repeat(128)).unwrap();
        let bytes = line(&peer, &secret);
        let text = std::str::from_utf8(&bytes).unwrap();
        assert!(bytes.len() <= 256);
        assert_eq!(text.matches('\n').count(), 1);
        assert!(text.ends_with('\n'));
        let fields: Vec<&str> = text.trim_end_matches('\n').split(' ').collect();
        assert_eq!(fields.len(), 4);
        assert_eq!((fields[0], fields[1]), ("HAND-PEER", "1"));
        assert!((16..=64).contains(&fields[2].len()));
        assert!(
            fields[2]
                .bytes()
                .all(|b| b.is_ascii_digit() || b.is_ascii_lowercase())
        );
        assert_eq!(fields[3], secret.as_str());
    }
}
