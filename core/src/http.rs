use std::time::Duration;

use iroh::endpoint::ReadError;
use tokio::time::{Instant, timeout_at};

use crate::alerts::{fleet_cookie, valid_cookie_value};
use crate::codes;
use crate::dialer::{Dialer, Notice, OpenError};
use crate::open::valid_fleet;
use crate::proxy::unpaired_403;

pub const MAX_BODY: usize = 16 << 20;
pub const REQUEST_TIMEOUT: Duration = Duration::from_secs(30);
const MAX_HEAD: usize = 16 * 1024;
const MAX_LINE: usize = 64;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Method {
    Get,
    Post,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum RequestError {
    Busy,
    NoLaptop,
    Unreachable,
    Unpaired,
    Lost,
    BoardNotRunning,
    TimedOut,
    TooLarge,
    Malformed,
}

impl From<OpenError> for RequestError {
    fn from(error: OpenError) -> Self {
        match error {
            OpenError::Busy => Self::Busy,
            OpenError::Lost => Self::Lost,
            OpenError::NotConfigured => Self::NoLaptop,
            OpenError::Unreachable => Self::Unreachable,
        }
    }
}

pub struct Request {
    bytes: Vec<u8>,
    timeout: Duration,
    max_body: usize,
    by_length: bool,
}

impl Request {
    pub fn new(
        method: Method,
        fleet: &str,
        path: &str,
        cookies: &str,
        csrf: Option<&str>,
        body: &[u8],
    ) -> Option<Self> {
        if !valid_fleet(fleet)
            || !allowed_path(path)
            || csrf.is_some_and(|token| !valid_cookie_value(token))
            || (method == Method::Get && !body.is_empty())
        {
            return None;
        }
        let cookie = fleet_cookie(cookies, fleet)?;
        let verb = match method {
            Method::Get => "GET",
            Method::Post => "POST",
        };
        let mut head =
            format!("{verb} /{fleet}{path} HTTP/1.1\r\nHost: localhost\r\nCookie: {cookie}\r\n");
        if let Some(token) = csrf {
            head.push_str(&format!("X-Hand-CSRF: {token}\r\n"));
        }
        if method == Method::Post {
            head.push_str(&format!(
                "Content-Type: application/json\r\nContent-Length: {}\r\n",
                body.len()
            ));
        }
        head.push_str("Connection: close\r\n\r\n");
        let mut bytes = head.into_bytes();
        bytes.extend_from_slice(body);
        Some(Self {
            bytes,
            timeout: REQUEST_TIMEOUT,
            max_body: MAX_BODY,
            by_length: false,
        })
    }

    pub fn within(mut self, timeout: Duration) -> Self {
        self.timeout = timeout;
        self
    }

    pub fn up_to(mut self, max_body: usize) -> Self {
        self.max_body = max_body;
        self
    }

    pub fn by_length(mut self) -> Self {
        self.by_length = true;
        self
    }
}

fn allowed_path(path: &str) -> bool {
    if !path
        .bytes()
        .all(|byte| (0x21..=0x7e).contains(&byte) && byte != b'#')
    {
        return false;
    }
    let base = path.split('?').next().unwrap_or_default();
    (base == "/alerts/feed" || base.starts_with("/api/v1/"))
        && base[1..].split('/').all(|segment| {
            let segment = segment.to_ascii_lowercase();
            !matches!(segment.replace("%2e", ".").as_str(), "" | "." | "..")
                && !segment.contains("%2f")
                && !segment.contains("%5c")
                && !segment.contains('\\')
        })
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Response {
    pub status: u16,
    pub content_type: Option<String>,
    pub cursor: Option<String>,
    pub error: Option<String>,
    pub body: Vec<u8>,
}

pub async fn exchange(dialer: &Dialer, request: &Request) -> Result<Response, RequestError> {
    let deadline = Instant::now() + request.timeout;
    let (mut send, mut recv) = match timeout_at(deadline, dialer.open_with(&request.bytes)).await {
        Ok(opened) => opened?,
        Err(_) if dialer.notice() == Notice::Busy => return Err(RequestError::Busy),
        Err(_) => return Err(RequestError::TimedOut),
    };
    let _ = send.finish();
    let read = async {
        let mut parser = Parser::new(request.max_body, request.by_length);
        let mut chunk = [0u8; 16 * 1024];
        loop {
            match recv.read(&mut chunk).await {
                Ok(0) => break,
                Ok(n) => {
                    if parser.push(&chunk[..n])? {
                        break;
                    }
                }
                Err(error) => return Err(read_failure(&error, dialer)),
            }
        }
        parser.finish()
    };
    timeout_at(deadline, read)
        .await
        .map_err(|_| RequestError::TimedOut)?
}

fn read_failure(error: &ReadError, dialer: &Dialer) -> RequestError {
    match codes::read_code(error) {
        Some(codes::BOARD_UNREACHABLE) if dialer.notice() == Notice::BoardUnreachable => {
            RequestError::BoardNotRunning
        }
        Some(codes::STREAM_IDLE) => RequestError::TimedOut,
        _ => RequestError::Lost,
    }
}

struct Head {
    status: u16,
    length: Option<usize>,
    chunked: bool,
    content_type: Option<String>,
    cursor: Option<String>,
    error: Option<String>,
}

fn parse_head(head: &[u8]) -> Option<Head> {
    let text = std::str::from_utf8(head).ok()?;
    let mut lines = text.split("\r\n");
    let mut status = lines.next()?.split(' ');
    if status.next()? != "HTTP/1.1" {
        return None;
    }
    let code = status.next()?;
    if code.len() != 3 {
        return None;
    }
    let status: u16 = code.parse().ok()?;
    if !(200..600).contains(&status) {
        return None;
    }
    let mut parsed = Head {
        status,
        length: None,
        chunked: false,
        content_type: None,
        cursor: None,
        error: None,
    };
    for line in lines {
        let (name, value) = line.split_once(':')?;
        let value = value.trim();
        if name.eq_ignore_ascii_case("transfer-encoding") {
            if parsed.chunked || !value.eq_ignore_ascii_case("chunked") {
                return None;
            }
            parsed.chunked = true;
        } else if name.eq_ignore_ascii_case("content-length") {
            if parsed.length.is_some()
                || value.is_empty()
                || !value.bytes().all(|byte| byte.is_ascii_digit())
            {
                return None;
            }
            parsed.length = Some(value.parse().ok()?);
        } else if name.eq_ignore_ascii_case("content-type") {
            parsed.content_type.get_or_insert_with(|| value.to_owned());
        } else if name.eq_ignore_ascii_case("x-hand-cursor") {
            parsed.cursor.get_or_insert_with(|| value.to_owned());
        } else if name.eq_ignore_ascii_case("x-hand-error") {
            parsed.error.get_or_insert_with(|| value.to_owned());
        }
    }
    (!(parsed.chunked && parsed.length.is_some())).then_some(parsed)
}

#[derive(Clone, Copy)]
enum Stage {
    Head,
    Body(usize),
    DataEnd,
    Size,
    Trailers,
    Close,
    Done,
}

struct Parser {
    input: Vec<u8>,
    stage: Stage,
    head: Option<Head>,
    body: Vec<u8>,
    max_body: usize,
    by_length: bool,
    trailers: usize,
}

impl Parser {
    fn new(max_body: usize, by_length: bool) -> Self {
        Self {
            input: Vec::new(),
            stage: Stage::Head,
            head: None,
            body: Vec::new(),
            max_body,
            by_length,
            trailers: 0,
        }
    }

    fn push(&mut self, data: &[u8]) -> Result<bool, RequestError> {
        self.input.extend_from_slice(data);
        loop {
            match self.stage {
                Stage::Done => return Ok(true),
                Stage::Head => {
                    let end = match find(&self.input, b"\r\n\r\n") {
                        Some(end) if end + 4 <= MAX_HEAD => end,
                        Some(_) => return Err(RequestError::Malformed),
                        None if self.input.len() >= MAX_HEAD => {
                            return Err(RequestError::Malformed);
                        }
                        None => return Ok(false),
                    };
                    if unpaired_403(&self.input[..end]) {
                        return Err(RequestError::Unpaired);
                    }
                    let head = parse_head(&self.input[..end]).ok_or(RequestError::Malformed)?;
                    if self.by_length && !matches!(head.status, 204 | 304) && head.length.is_none()
                    {
                        return Err(RequestError::Malformed);
                    }
                    self.input.drain(..end + 4);
                    self.stage = if matches!(head.status, 204 | 304) {
                        Stage::Done
                    } else if head.chunked {
                        Stage::Size
                    } else {
                        match head.length {
                            Some(0) => Stage::Done,
                            Some(length) if length > self.max_body => {
                                return Err(RequestError::TooLarge);
                            }
                            Some(length) => Stage::Body(length),
                            None => Stage::Close,
                        }
                    };
                    self.head = Some(head);
                }
                Stage::Body(left) => {
                    let take = left.min(self.input.len());
                    self.body.extend(self.input.drain(..take));
                    if take < left {
                        self.stage = Stage::Body(left - take);
                        return Ok(false);
                    }
                    let chunked = self.head.as_ref().is_some_and(|head| head.chunked);
                    self.stage = if chunked { Stage::DataEnd } else { Stage::Done };
                }
                Stage::DataEnd => {
                    if self.input.len() < 2 {
                        return Ok(false);
                    }
                    if !self.input.starts_with(b"\r\n") {
                        return Err(RequestError::Malformed);
                    }
                    self.input.drain(..2);
                    self.stage = Stage::Size;
                }
                Stage::Size => {
                    let Some(line) = self.take_line(MAX_LINE)? else {
                        return Ok(false);
                    };
                    let line = std::str::from_utf8(&line).map_err(|_| RequestError::Malformed)?;
                    let digits = line.split(';').next().unwrap_or_default();
                    if digits.is_empty()
                        || digits.len() > 16
                        || !digits.bytes().all(|byte| byte.is_ascii_hexdigit())
                    {
                        return Err(RequestError::Malformed);
                    }
                    let size =
                        usize::from_str_radix(digits, 16).map_err(|_| RequestError::Malformed)?;
                    self.stage = if size == 0 {
                        Stage::Trailers
                    } else if self.body.len().saturating_add(size) > self.max_body {
                        return Err(RequestError::TooLarge);
                    } else {
                        Stage::Body(size)
                    };
                }
                Stage::Trailers => {
                    let Some(line) = self.take_line(MAX_HEAD)? else {
                        return Ok(false);
                    };
                    self.trailers += line.len() + 2;
                    if self.trailers > MAX_HEAD {
                        return Err(RequestError::Malformed);
                    }
                    if line.is_empty() {
                        self.stage = Stage::Done;
                    }
                }
                Stage::Close => {
                    self.body.append(&mut self.input);
                    return if self.body.len() > self.max_body {
                        Err(RequestError::TooLarge)
                    } else {
                        Ok(false)
                    };
                }
            }
        }
    }

    fn take_line(&mut self, limit: usize) -> Result<Option<Vec<u8>>, RequestError> {
        let Some(end) = find(&self.input, b"\r\n") else {
            return if self.input.len() > limit {
                Err(RequestError::Malformed)
            } else {
                Ok(None)
            };
        };
        let line = self.input.drain(..end).collect();
        self.input.drain(..2);
        Ok(Some(line))
    }

    fn finish(self) -> Result<Response, RequestError> {
        match (self.stage, self.head) {
            (Stage::Done | Stage::Close, Some(head)) => Ok(Response {
                status: head.status,
                content_type: head.content_type,
                cursor: head.cursor,
                error: head.error,
                body: self.body,
            }),
            _ => Err(RequestError::Lost),
        }
    }
}

fn find(buffer: &[u8], needle: &[u8]) -> Option<usize> {
    buffer
        .windows(needle.len())
        .position(|window| window == needle)
}

#[cfg(test)]
mod tests {
    use super::*;

    const FLEET: &str = "f0123456789ab";
    const COOKIES: &str = "a=b; hand_phone_f999999999999=other; hand_phone_f0123456789ab=abc.def";

    fn parse(chunks: &[&[u8]], max_body: usize) -> Result<Response, RequestError> {
        parse_with(chunks, max_body, false)
    }

    fn parse_with(
        chunks: &[&[u8]],
        max_body: usize,
        by_length: bool,
    ) -> Result<Response, RequestError> {
        let mut parser = Parser::new(max_body, by_length);
        for chunk in chunks {
            if parser.push(chunk)? {
                break;
            }
        }
        parser.finish()
    }

    fn bytewise(wire: &[u8], max_body: usize) -> Result<Response, RequestError> {
        let chunks: Vec<&[u8]> = wire.chunks(1).collect();
        parse(&chunks, max_body)
    }

    fn text(request: &Request) -> &str {
        std::str::from_utf8(&request.bytes).unwrap()
    }

    #[test]
    fn a_get_names_the_fleets_cookie_alone_and_closes_the_connection() {
        let request = Request::new(Method::Get, FLEET, "/api/v1/status", COOKIES, None, b"");
        assert_eq!(
            text(&request.unwrap()),
            "GET /f0123456789ab/api/v1/status HTTP/1.1\r\nHost: localhost\r\nCookie: hand_phone_f0123456789ab=abc.def\r\nConnection: close\r\n\r\n"
        );
    }

    #[test]
    fn a_post_carries_the_csrf_token_and_its_body_with_a_length() {
        let request = Request::new(
            Method::Post,
            FLEET,
            "/api/v1/message",
            COOKIES,
            Some("tok123"),
            br#"{"text":"hi"}"#,
        );
        assert_eq!(
            text(&request.unwrap()),
            "POST /f0123456789ab/api/v1/message HTTP/1.1\r\nHost: localhost\r\nCookie: hand_phone_f0123456789ab=abc.def\r\nX-Hand-CSRF: tok123\r\nContent-Type: application/json\r\nContent-Length: 13\r\nConnection: close\r\n\r\n{\"text\":\"hi\"}"
        );
    }

    #[test]
    fn a_request_that_could_leave_its_fleet_or_split_a_header_is_refused() {
        let new = |fleet: &str, path: &str, cookies: &str, csrf: Option<&str>| {
            Request::new(Method::Get, fleet, path, cookies, csrf, b"")
        };
        let ok = "/api/v1/ok";
        assert!(new(FLEET, ok, COOKIES, None).is_some());
        for (fleet, path, cookies, csrf) in [
            ("f999999999999", ok, "hand_phone_f0123456789ab=abc", None),
            (FLEET, ok, "a=b", None),
            ("bad", ok, "hand_phone_bad=abc", None),
            (FLEET, "api/v1/ok", COOKIES, None),
            (FLEET, "/api/v1/a b", COOKIES, None),
            (FLEET, "/api/v1/a\r\nX: y", COOKIES, None),
            (FLEET, "/api/v1/a#b", COOKIES, None),
            (FLEET, ok, COOKIES, Some("a\r\nb")),
            (FLEET, ok, COOKIES, Some("")),
        ] {
            assert!(new(fleet, path, cookies, csrf).is_none(), "{path} {csrf:?}");
        }
        assert!(Request::new(Method::Get, FLEET, ok, COOKIES, None, b"x").is_none());
    }

    #[test]
    fn only_the_feed_and_the_api_are_reachable_and_never_by_a_dotted_path() {
        let path =
            |path: &str| Request::new(Method::Get, FLEET, path, COOKIES, None, b"").is_some();
        for allowed in [
            "/alerts/feed",
            "/alerts/feed?",
            "/alerts/feed?after=7&wait=25",
            "/api/v1/status",
            "/api/v1/task/t1?since=a/../b",
            "/api/v1/supervisor/keys",
        ] {
            assert!(path(allowed), "{allowed}");
        }
        for refused in [
            "/",
            "/pair",
            "/alerts",
            "/alerts/feed/x",
            "/alerts/feedx",
            "/api/v1",
            "/api/v1/",
            "/api/v2/status",
            "/api/v1x/status",
            "/api/v1//status",
            "/api/v1/status/",
            "/api/v1/./status",
            "/api/v1/../pair",
            "/api/v1/%2e%2e/pair",
            "/api/v1/%2E/status",
            "/api/v1/.%2e/pair",
            "/api/v1/..%2fpair",
            "/api/v1/..%5cpair",
            "/api/v1/..\\pair",
            "/alerts/../api/v1/status",
        ] {
            assert!(!path(refused), "{refused}");
        }
    }

    #[test]
    fn a_length_answer_is_read_across_any_split_with_its_headers() {
        let wire = b"HTTP/1.1 200 OK\r\nContent-Type: application/json; charset=utf-8\r\nX-Hand-Cursor: c7\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello";
        for response in [bytewise(wire, 100).unwrap(), parse(&[wire], 100).unwrap()] {
            assert_eq!(response.status, 200);
            assert_eq!(
                response.content_type.as_deref(),
                Some("application/json; charset=utf-8")
            );
            assert_eq!(response.cursor.as_deref(), Some("c7"));
            assert_eq!(response.error, None);
            assert_eq!(response.body, b"hello");
        }
    }

    #[test]
    fn a_chunked_answer_is_joined_and_its_extensions_and_trailers_are_dropped() {
        let wire = b"HTTP/1.1 200 OK\r\nTransfer-Encoding: Chunked\r\n\r\n5;ext=1\r\nhello\r\nB\r\n, the board\r\n0\r\nX-Trailer: 1\r\n\r\n";
        for response in [bytewise(wire, 100).unwrap(), parse(&[wire], 100).unwrap()] {
            assert_eq!(response.body, b"hello, the board");
        }
    }

    #[test]
    fn a_304_and_an_empty_body_end_at_the_head() {
        let wire = b"HTTP/1.1 304 Not Modified\r\nX-Hand-Cursor: c7\r\n\r\n";
        let response = bytewise(wire, 100).unwrap();
        assert_eq!((response.status, response.body.len()), (304, 0));
        assert_eq!(response.cursor.as_deref(), Some("c7"));
        let empty = parse(&[b"HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"], 100).unwrap();
        assert!(empty.body.is_empty());
        let sized = b"HTTP/1.1 304 Not Modified\r\nContent-Length: 12\r\n\r\n";
        let response = bytewise(sized, 100).unwrap();
        assert_eq!((response.status, response.body.len()), (304, 0));
    }

    #[test]
    fn an_answer_with_no_length_ends_when_the_stream_does() {
        let response = parse(&[b"HTTP/1.1 200 OK\r\n\r\nab", b"cd"], 100).unwrap();
        assert_eq!(response.body, b"abcd");
    }

    #[test]
    fn a_403_that_says_no_longer_paired_is_unpaired_whatever_follows_its_head() {
        let gone = "X-Hand-Error: This%20phone%20is%20no%20longer%20paired";
        let cases = [
            (
                "complete",
                format!("HTTP/1.1 403 Forbidden\r\n{gone}\r\nContent-Length: 0\r\n\r\n"),
            ),
            (
                "truncated body",
                format!("HTTP/1.1 403 Forbidden\r\n{gone}\r\nContent-Length: 100\r\n\r\nabc"),
            ),
            (
                "oversize body",
                format!("HTTP/1.1 403 Forbidden\r\n{gone}\r\nContent-Length: 99999\r\n\r\n"),
            ),
            (
                "bad chunk",
                format!(
                    "HTTP/1.1 403 Forbidden\r\n{gone}\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n"
                ),
            ),
            (
                "odd transfer encoding",
                format!("HTTP/1.1 403 Forbidden\r\n{gone}\r\nTransfer-Encoding: gzip\r\n\r\n"),
            ),
            (
                "length and chunked",
                format!(
                    "HTTP/1.1 403 Forbidden\r\n{gone}\r\nTransfer-Encoding: chunked\r\nContent-Length: 0\r\n\r\n"
                ),
            ),
            (
                "second error line",
                format!(
                    "HTTP/1.1 403 Forbidden\r\nX-Hand-Error: other\r\n{gone}\r\nContent-Length: 0\r\n\r\n"
                ),
            ),
        ];
        for (name, wire) in cases {
            for by_length in [false, true] {
                assert_eq!(
                    parse_with(&[wire.as_bytes()], 10, by_length),
                    Err(RequestError::Unpaired),
                    "{name}"
                );
            }
            assert_eq!(
                bytewise(wire.as_bytes(), 10),
                Err(RequestError::Unpaired),
                "{name}"
            );
        }
    }

    #[test]
    fn any_other_refusal_is_an_answer_with_its_error_header() {
        let other = parse(
            &[b"HTTP/1.1 403 Forbidden\r\nX-Hand-Error: Pair%20this%20phone%20first\r\nContent-Length: 0\r\n\r\n"],
            100,
        )
        .unwrap();
        assert_eq!(other.status, 403);
        assert_eq!(other.error.as_deref(), Some("Pair%20this%20phone%20first"));
        for wire in [
            &b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"[..],
            b"HTTP/1.1 200 OK\r\nX-Hand-Error: no longer paired\r\nContent-Length: 0\r\n\r\n",
            b"HTTP/1.1 404 Not Found\r\nX-Hand-Error: no longer paired\r\nContent-Length: 0\r\n\r\n",
        ] {
            assert!(parse(&[wire], 100).is_ok());
        }
    }

    #[test]
    fn a_feed_style_request_takes_a_length_and_nothing_else() {
        let by_length = |wire: &[u8]| parse_with(&[wire], 100, true);
        assert_eq!(
            by_length(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nhi")
                .unwrap()
                .body,
            b"hi"
        );
        assert_eq!(
            by_length(b"HTTP/1.1 304 Not Modified\r\n\r\n")
                .unwrap()
                .status,
            304
        );
        for wire in [
            &b"HTTP/1.1 200 OK\r\n\r\nhi"[..],
            b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nhi\r\n0\r\n\r\n",
        ] {
            assert_eq!(by_length(wire), Err(RequestError::Malformed));
            assert!(parse(&[wire], 100).is_ok());
        }
    }

    #[test]
    fn a_body_over_the_cap_is_too_large_however_it_is_framed() {
        let declared = b"HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\n";
        assert_eq!(parse(&[declared], 10), Err(RequestError::TooLarge));
        let exact = b"HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\n0123456789";
        assert_eq!(parse(&[exact], 10).unwrap().body.len(), 10);
        let chunked = b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n6\r\n012345\r\n5\r\n67890\r\n0\r\n\r\n";
        assert_eq!(parse(&[chunked], 10), Err(RequestError::TooLarge));
        assert_eq!(parse(&[chunked], 11).unwrap().body.len(), 11);
        let open = [&b"HTTP/1.1 200 OK\r\n\r\n01234"[..], b"567890"];
        assert_eq!(parse(&open, 10), Err(RequestError::TooLarge));
    }

    #[test]
    fn an_answer_that_breaks_http_is_malformed() {
        let cases: [(&str, &[u8]); 14] = [
            ("http 1.0", b"HTTP/1.0 200 OK\r\nContent-Length: 0\r\n\r\n"),
            ("short code", b"HTTP/1.1 20 OK\r\n\r\n"),
            ("word code", b"HTTP/1.1 abc OK\r\n\r\n"),
            ("informational", b"HTTP/1.1 100 Continue\r\n\r\n"),
            (
                "two lengths",
                b"HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: 1\r\n\r\nx",
            ),
            (
                "signed length",
                b"HTTP/1.1 200 OK\r\nContent-Length: -1\r\n\r\n",
            ),
            (
                "length and chunked",
                b"HTTP/1.1 200 OK\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\nx",
            ),
            (
                "gzip",
                b"HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip, chunked\r\n\r\n",
            ),
            (
                "header without colon",
                b"HTTP/1.1 200 OK\r\nno colon\r\n\r\n",
            ),
            ("not utf-8", b"HTTP/1.1 200 OK\r\nX: \xff\r\n\r\n"),
            (
                "chunk size not hex",
                b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n",
            ),
            (
                "chunk size signed",
                b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n+5\r\nhello\r\n",
            ),
            (
                "chunk size empty",
                b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n\r\n",
            ),
            (
                "chunk without crlf",
                b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nhiXX",
            ),
        ];
        for (name, wire) in cases {
            assert_eq!(parse(&[wire], 100), Err(RequestError::Malformed), "{name}");
        }
        let head_of = |size: usize| {
            let mut head = b"HTTP/1.1 200 OK\r\nX: ".to_vec();
            head.resize(size - 4, b'a');
            head.extend_from_slice(b"\r\n\r\n");
            head
        };
        assert!(parse(&[&head_of(MAX_HEAD)], 100).is_ok());
        assert_eq!(
            parse(&[&head_of(MAX_HEAD + 1)], 100),
            Err(RequestError::Malformed)
        );
        let mut trailers = b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n".to_vec();
        for _ in 0..MAX_HEAD / 100 + 1 {
            trailers.extend_from_slice(&[b"X: ".as_slice(), &[b'a'; 95], b"\r\n"].concat());
        }
        trailers.extend_from_slice(b"\r\n");
        let pieces: Vec<&[u8]> = trailers.chunks(1000).collect();
        assert_eq!(parse(&pieces, 100), Err(RequestError::Malformed));
        let endless_head = vec![b'a'; MAX_HEAD + 1];
        assert_eq!(parse(&[&endless_head], 100), Err(RequestError::Malformed));
        let mut endless_size = b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n".to_vec();
        endless_size.extend(vec![b'1'; MAX_LINE + 1]);
        assert_eq!(parse(&[&endless_size], 100), Err(RequestError::Malformed));
    }

    #[test]
    fn a_stream_that_ends_before_the_answer_is_complete_is_lost() {
        for (name, wire) in [
            ("nothing", &b""[..]),
            ("mid head", b"HTTP/1.1 200 OK\r\nContent-Le"),
            (
                "mid body",
                b"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhel",
            ),
            (
                "mid chunk",
                b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhel",
            ),
            (
                "no last chunk",
                b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nhi\r\n",
            ),
            (
                "mid trailers",
                b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nX: 1\r\n",
            ),
        ] {
            assert_eq!(parse(&[wire], 100), Err(RequestError::Lost), "{name}");
        }
    }

    #[test]
    fn open_failures_keep_their_kind_and_a_missing_laptop_is_its_own() {
        assert_eq!(RequestError::from(OpenError::Busy), RequestError::Busy);
        assert_eq!(RequestError::from(OpenError::Lost), RequestError::Lost);
        assert_eq!(
            RequestError::from(OpenError::Unreachable),
            RequestError::Unreachable
        );
        assert_eq!(
            RequestError::from(OpenError::NotConfigured),
            RequestError::NoLaptop
        );
    }
}
