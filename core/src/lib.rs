mod backoff;
mod codes;
mod companion;
mod dialer;
mod key;
mod link;
mod navigation;
mod pairing;
mod scan;
#[cfg(test)]
mod testutil;

pub mod proxy;

pub use codes::{BOARD_ERROR, BOARD_UNREACHABLE, FULL, IDLE, STREAM_IDLE};
pub use companion::{Companion, CompanionConfig, ConfirmError, StartError, proxy_origin};
pub use dialer::{
    ALPN, BACKOFF_RESET, Dialer, HANDSHAKE_DEADLINE, Notice, OpenError, STREAMS_PER_CONNECTION,
    StreamRecv, StreamSend, parse_target,
};
pub use key::{CipherError, KeyCipher, key_path, load_or_create as load_or_create_key};
pub use link::{
    CAMERA_REFUSED, LINK_NEEDS_UPDATE, LINK_REJECTED, Link, MAX_LEN, Reject, parse_link,
    parse_link_with,
};
pub use navigation::may_load;
pub use pairing::{Offer, OfferError, PAIRING_PENDING, Pairing};
pub use proxy::{Proxy, Signal};
pub use scan::{CAMERA_FAILED, SCAN_OPEN, ScanError, ScanStatus, scan_reply};
