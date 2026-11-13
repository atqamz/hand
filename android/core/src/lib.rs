mod alerts;
mod backoff;
mod codes;
mod companion;
mod dialer;
mod http;
mod key;
mod link;
mod live;
mod open;
mod pair;
mod pairing;
mod sessions;
#[cfg(test)]
mod testutil;

pub mod proxy;

pub use alerts::{
    Alert, AlertSink, CookieSource, Feed, Fleet, RESCAN, Stop, Supervisor, WAIT, paired_fleets,
};
pub use codes::{BOARD_ERROR, BOARD_UNREACHABLE, FULL, IDLE, STREAM_IDLE};
pub use companion::{Companion, CompanionConfig, ConfirmError, OpenLinkError, StartError};
pub use dialer::{
    ALPN, BACKOFF_RESET, Dialer, HANDSHAKE_DEADLINE, Notice, OpenError, STREAMS_PER_CONNECTION,
    StreamRecv, StreamSend, parse_target,
};
pub use http::{MAX_BODY, Method, REQUEST_TIMEOUT, Request, RequestError, Response, exchange};
pub use key::{CipherError, KeyCipher, key_path, load_or_create as load_or_create_key};
pub use link::{
    LINK_NEEDS_UPDATE, LINK_REJECTED, Link, MAX_LEN, Reject, parse_link, parse_link_with,
};
pub use live::{Cancel, Live, LiveEnd, LiveSink, WATCHDOG as LIVE_WATCHDOG};
pub use open::{NOT_PAIRED, OPEN_REJECTED, OpenLink, is_open_link, open_paired, parse_open};
pub use pair::{NAME_MAX, PairError, parse_answer, redeem, valid_name};
pub use pairing::{Offer, OfferError, PAIRING_PENDING, Pairing};
pub use proxy::Proxy;
pub use sessions::{Jar, Paired, Sessions};
