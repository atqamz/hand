use std::fs;
use std::future::Future;
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::time::Duration;

use serde::Deserialize;
use tokio::time::{Instant, sleep, sleep_until, timeout};

use crate::backoff::Backoff;
use crate::dialer::Dialer;
use crate::open::{parse_open, valid_fleet};
use crate::proxy::unpaired_403;

pub const WAIT: Duration = Duration::from_secs(25);
const ANSWER_SLACK: Duration = Duration::from_secs(10);
const MIN_POLL: Duration = Duration::from_secs(1);
const MAX_HEAD: usize = 16 * 1024;
const MAX_BODY: usize = 64 * 1024;
const MAX_ALERTS: usize = 50;
const MAX_TEXT: usize = 200;
const MAX_NAME: usize = 64;
const COOKIE_PREFIX: &str = "hand_phone_";

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Fleet {
    pub id: String,
    pub name: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Alert {
    pub seq: u64,
    pub kind: String,
    pub reference: String,
    pub text: String,
    pub link: Option<String>,
}

pub trait CookieSource: Sync {
    fn cookies(&self) -> impl Future<Output = io::Result<Option<String>>> + Send;
}

pub trait AlertSink: Sync {
    fn alert(&self, fleet: &Fleet, alert: &Alert) -> impl Future<Output = io::Result<()>> + Send;
    fn unpaired(&self, fleet: &str, name: Option<&str>) -> impl Future<Output = ()> + Send;
}

pub fn paired_fleets(cookie_header: &str) -> Vec<String> {
    let mut fleets: Vec<String> = Vec::new();
    for pair in cookie_header.split(';') {
        let Some((name, value)) = pair.trim().split_once('=') else {
            continue;
        };
        let Some(fleet) = name.strip_prefix(COOKIE_PREFIX) else {
            continue;
        };
        if valid_fleet(fleet) && valid_cookie_value(value) && !fleets.iter().any(|f| f == fleet) {
            fleets.push(fleet.to_owned());
        }
    }
    fleets
}

fn fleet_cookie(cookie_header: &str, fleet: &str) -> Option<String> {
    cookie_header.split(';').find_map(|pair| {
        let (name, value) = pair.trim().split_once('=')?;
        let id = name.strip_prefix(COOKIE_PREFIX)?;
        (id == fleet && valid_cookie_value(value)).then(|| format!("{name}={value}"))
    })
}

fn valid_cookie_value(value: &str) -> bool {
    !value.is_empty()
        && value.bytes().all(|byte| {
            (0x21..=0x7e).contains(&byte) && !matches!(byte, b'"' | b',' | b';' | b'\\')
        })
}

pub struct Feed {
    dialer: Dialer,
    dir: PathBuf,
    wait: Duration,
}

enum Failure {
    Unpaired,
    Retry,
}

impl Feed {
    pub fn new(dialer: Dialer, dir: PathBuf) -> Self {
        Self {
            dialer,
            dir,
            wait: WAIT,
        }
    }

    pub fn with_wait(mut self, wait: Duration) -> Self {
        self.wait = wait;
        self
    }

    pub async fn run(&self, fleet: &str, cookies: &impl CookieSource, sink: &impl AlertSink) {
        if !valid_fleet(fleet) {
            return;
        }
        let path = self.dir.join(format!("{fleet}.cursor"));
        let mut cursor = read_cursor(&path);
        let mut backoff = Backoff::default();
        let mut more = false;
        let mut name: Option<String> = None;
        loop {
            let started = Instant::now();
            let answer = match self.poll(fleet, cookies, cursor, more).await {
                Ok(answer) => answer,
                Err(Failure::Unpaired) => {
                    let _ = fs::remove_file(&path);
                    sink.unpaired(fleet, name.as_deref()).await;
                    return;
                }
                Err(Failure::Retry) => {
                    sleep(backoff.next_delay()).await;
                    continue;
                }
            };
            name = Some(answer.fleet.name.clone());
            let mut delivered = true;
            for alert in &answer.alerts {
                if sink.alert(&answer.fleet, alert).await.is_err() {
                    delivered = false;
                    break;
                }
            }
            if delivered && cursor != Some(answer.cursor) {
                delivered = write_cursor(&path, answer.cursor).is_ok();
                if delivered {
                    cursor = Some(answer.cursor);
                }
            }
            if !delivered {
                sleep(backoff.next_delay()).await;
                continue;
            }
            backoff.reset();
            more = answer.more;
            if !more {
                sleep_until(started + MIN_POLL).await;
            }
        }
    }

    async fn poll(
        &self,
        fleet: &str,
        cookies: &impl CookieSource,
        cursor: Option<u64>,
        more: bool,
    ) -> Result<Answer, Failure> {
        let header = cookies
            .cookies()
            .await
            .map_err(|_| Failure::Retry)?
            .ok_or(Failure::Unpaired)?;
        let cookie = fleet_cookie(&header, fleet).ok_or(Failure::Unpaired)?;
        let request = request(fleet, &cookie, cursor, (!more).then_some(self.wait));
        let (mut send, mut recv) = self
            .dialer
            .open_with(request.as_bytes())
            .await
            .map_err(|_| Failure::Retry)?;
        let _ = send.finish();
        let body = timeout(self.wait + ANSWER_SLACK, async {
            let mut buffer = Vec::new();
            let mut chunk = [0u8; 4096];
            let head_end = loop {
                if let Some(end) = find_head_end(&buffer) {
                    break end;
                }
                if buffer.len() > MAX_HEAD {
                    return Err(Failure::Retry);
                }
                match recv.read(&mut chunk).await {
                    Ok(0) | Err(_) => return Err(Failure::Retry),
                    Ok(n) => buffer.extend_from_slice(&chunk[..n]),
                }
            };
            let head = parse_head(&buffer[..head_end]).ok_or(Failure::Retry)?;
            if head.status != 200 {
                return Err(if unpaired_403(&buffer[..head_end]) {
                    Failure::Unpaired
                } else {
                    Failure::Retry
                });
            }
            if head.chunked {
                return Err(Failure::Retry);
            }
            let length = head.length.filter(|length| *length <= MAX_BODY);
            let length = length.ok_or(Failure::Retry)?;
            let body_start = head_end + 4;
            while buffer.len() < body_start + length {
                match recv.read(&mut chunk).await {
                    Ok(0) | Err(_) => return Err(Failure::Retry),
                    Ok(n) => buffer.extend_from_slice(&chunk[..n]),
                }
            }
            Ok(buffer[body_start..body_start + length].to_vec())
        })
        .await
        .map_err(|_| Failure::Retry)??;
        validate(fleet, cursor, &body).ok_or(Failure::Retry)
    }
}

fn request(fleet: &str, cookie: &str, cursor: Option<u64>, wait: Option<Duration>) -> String {
    let mut query = Vec::new();
    if let Some(cursor) = cursor {
        query.push(format!("after={cursor}"));
    }
    if let Some(wait) = wait {
        query.push(format!("wait={}", wait.as_secs()));
    }
    format!(
        "GET /{fleet}/alerts/feed?{} HTTP/1.1\r\nHost: localhost\r\nCookie: {cookie}\r\nConnection: close\r\n\r\n",
        query.join("&")
    )
}

fn find_head_end(buffer: &[u8]) -> Option<usize> {
    buffer.windows(4).position(|window| window == b"\r\n\r\n")
}

struct Head {
    status: u16,
    length: Option<usize>,
    chunked: bool,
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
    let status = code.parse().ok()?;
    let mut length = None;
    let mut chunked = false;
    for line in lines {
        let (name, value) = line.split_once(':')?;
        if name.eq_ignore_ascii_case("transfer-encoding") {
            chunked = true;
        }
        if name.eq_ignore_ascii_case("content-length") {
            let value = value.trim();
            if length.is_some() || value.is_empty() || !value.bytes().all(|b| b.is_ascii_digit()) {
                return None;
            }
            length = Some(value.parse().ok()?);
        }
    }
    Some(Head {
        status,
        length,
        chunked,
    })
}

#[derive(Deserialize)]
struct WireAnswer {
    version: u64,
    fleet: WireFleet,
    cursor: u64,
    more: bool,
    alerts: Vec<WireAlert>,
}

#[derive(Deserialize)]
struct WireFleet {
    id: String,
    name: String,
}

#[derive(Deserialize)]
struct WireAlert {
    seq: u64,
    kind: String,
    r#ref: String,
    text: String,
    link: String,
}

struct Answer {
    fleet: Fleet,
    alerts: Vec<Alert>,
    cursor: u64,
    more: bool,
}

fn validate(fleet: &str, after: Option<u64>, body: &[u8]) -> Option<Answer> {
    let wire: WireAnswer = serde_json::from_slice(body).ok()?;
    if wire.version != 1 || wire.fleet.id != fleet || wire.alerts.len() > MAX_ALERTS {
        return None;
    }
    match wire.alerts.last() {
        Some(last) => {
            let mut floor = after?;
            for alert in &wire.alerts {
                if alert.seq <= floor {
                    return None;
                }
                floor = alert.seq;
            }
            if wire.cursor != last.seq {
                return None;
            }
        }
        None => {
            if after.is_some_and(|after| wire.cursor > after) {
                return None;
            }
        }
    }
    let more = wire.more && !wire.alerts.is_empty();
    let alerts = wire
        .alerts
        .into_iter()
        .filter_map(|alert| {
            let text = clean(&alert.text, MAX_TEXT);
            (!text.trim().is_empty()).then(|| Alert {
                seq: alert.seq,
                kind: clean(&alert.kind, MAX_NAME),
                reference: clean(&alert.r#ref, MAX_NAME),
                text,
                link: Some(alert.link)
                    .filter(|link| parse_open(link).is_some_and(|open| open.fleet == fleet)),
            })
        })
        .collect::<Vec<_>>();
    Some(Answer {
        more,
        cursor: wire.cursor,
        fleet: Fleet {
            id: fleet.to_owned(),
            name: clean(&wire.fleet.name, MAX_NAME),
        },
        alerts,
    })
}

fn clean(text: &str, max: usize) -> String {
    text.chars()
        .filter_map(|c| match c {
            c if c.is_control() || matches!(c, '\u{2028}' | '\u{2029}') => Some(' '),
            c if is_format(c) => None,
            c => Some(c),
        })
        .take(max)
        .collect()
}

fn is_format(c: char) -> bool {
    matches!(c,
        '\u{AD}'
        | '\u{600}'..='\u{605}'
        | '\u{61C}'
        | '\u{6DD}'
        | '\u{70F}'
        | '\u{890}'..='\u{891}'
        | '\u{8E2}'
        | '\u{180E}'
        | '\u{200B}'..='\u{200F}'
        | '\u{202A}'..='\u{202E}'
        | '\u{2060}'..='\u{2064}'
        | '\u{2066}'..='\u{206F}'
        | '\u{FEFF}'
        | '\u{FFF9}'..='\u{FFFB}'
        | '\u{110BD}'
        | '\u{110CD}'
        | '\u{13430}'..='\u{1343F}'
        | '\u{1BCA0}'..='\u{1BCA3}'
        | '\u{1D173}'..='\u{1D17A}'
        | '\u{E0001}'
        | '\u{E0020}'..='\u{E007F}')
}

fn read_cursor(path: &Path) -> Option<u64> {
    fs::read_to_string(path).ok()?.trim().parse().ok()
}

fn write_cursor(path: &Path, cursor: u64) -> io::Result<()> {
    if let Some(dir) = path.parent() {
        fs::create_dir_all(dir)?;
    }
    let temp = path.with_extension("cursor.tmp");
    let mut file = fs::File::create(&temp)?;
    file.write_all(cursor.to_string().as_bytes())?;
    file.sync_all()?;
    fs::rename(&temp, path)
}

#[cfg(test)]
mod tests {
    use serde_json::{Value, json};

    use super::*;
    use crate::testutil::TempDir;

    const FLEET: &str = "f0123456789ab";

    fn body(cursor: u64, alerts: Vec<Value>) -> Vec<u8> {
        json!({"version": 1, "fleet": {"id": FLEET, "name": "hand"}, "cursor": cursor, "more": false, "alerts": alerts})
            .to_string()
            .into_bytes()
    }

    fn alert(seq: u64) -> Value {
        json!({"seq": seq, "at": "2026-11-10T03:12:00Z", "kind": "attempt.failed", "ref": "a1", "text": "a1 failed", "link": ""})
    }

    #[test]
    fn the_cookie_header_yields_the_fleets_and_only_the_fleets_cookie() {
        let header = "a=b; hand_phone_f0123456789ab=abc; hand_phone_f999999999999=def; hand_phone_f0123456789ab=abc; hand_phone_bad=1; hand_phone_f0123456789ac=; hand_phone_f0123456789ad=a\"b";
        assert_eq!(paired_fleets(header), ["f0123456789ab", "f999999999999"]);
        assert_eq!(
            fleet_cookie(header, FLEET).as_deref(),
            Some("hand_phone_f0123456789ab=abc")
        );
        assert_eq!(fleet_cookie(header, "f0123456789ad"), None);
        assert_eq!(fleet_cookie("a=b", FLEET), None);
    }

    #[test]
    fn the_request_is_a_plain_get_with_the_cookie_and_connection_close() {
        assert_eq!(
            request(FLEET, "hand_phone_f0123456789ab=abc", Some(7), Some(WAIT)),
            "GET /f0123456789ab/alerts/feed?after=7&wait=25 HTTP/1.1\r\nHost: localhost\r\nCookie: hand_phone_f0123456789ab=abc\r\nConnection: close\r\n\r\n"
        );
        assert_eq!(
            request(FLEET, "hand_phone_f0123456789ab=abc", None, None),
            "GET /f0123456789ab/alerts/feed? HTTP/1.1\r\nHost: localhost\r\nCookie: hand_phone_f0123456789ab=abc\r\nConnection: close\r\n\r\n"
        );
    }

    #[test]
    fn a_head_needs_http_one_one_and_one_length_and_flags_chunking() {
        let head = |text: &str| parse_head(text.as_bytes());
        let ok = head("HTTP/1.1 200 OK\r\nContent-Length: 12\r\nContent-Type: x").unwrap();
        assert_eq!((ok.status, ok.length), (200, Some(12)));
        assert_eq!(head("HTTP/1.1 403 Forbidden").unwrap().length, None);
        let refusal = head("HTTP/1.1 403 Forbidden\r\nTransfer-Encoding: chunked").unwrap();
        assert!((refusal.status, refusal.chunked) == (403, true));
        assert!(!ok.chunked);
        for bad in [
            "HTTP/1.0 200 OK",
            "HTTP/1.1 20 OK",
            "HTTP/1.1 abc OK",
            "HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: 1",
            "HTTP/1.1 200 OK\r\nContent-Length: -1",
            "HTTP/1.1 200 OK\r\nContent-Length: ",
            "HTTP/1.1 200 OK\r\nno colon",
            "",
        ] {
            assert!(head(bad).is_none(), "{bad}");
        }
    }

    #[test]
    fn a_valid_answer_is_cleaned_and_its_links_checked_against_the_fleet() {
        let mut first = alert(8);
        first["text"] = json!(format!("a\nb\u{7}{}", "z".repeat(300)));
        first["link"] = json!("handboard://open/f0123456789ab/attempt/a1");
        let mut second = alert(9);
        second["link"] = json!("handboard://open/f999999999999/attempt/a1");
        let mut third = alert(10);
        third["link"] = json!("handboard://c#v1.x");
        let answer = validate(FLEET, Some(7), &body(10, vec![first, second, third])).unwrap();
        assert_eq!(answer.cursor, 10);
        assert_eq!(answer.alerts[0].text.chars().count(), 200);
        assert!(answer.alerts[0].text.starts_with("a b "));
        assert_eq!(
            answer.alerts[0].link.as_deref(),
            Some("handboard://open/f0123456789ab/attempt/a1")
        );
        assert_eq!(answer.alerts[1].link, None);
        assert_eq!(answer.alerts[2].link, None);
    }

    #[test]
    fn an_answer_that_breaks_the_contract_is_refused() {
        let wrong_version = json!({"version": 2, "fleet": {"id": FLEET, "name": "h"}, "cursor": 0, "more": false, "alerts": []});
        let other_fleet = json!({"version": 1, "fleet": {"id": "f999999999999", "name": "h"}, "cursor": 0, "more": false, "alerts": []});
        let missing =
            json!({"version": 1, "fleet": {"id": FLEET, "name": "h"}, "cursor": 0, "alerts": []});
        let many: Vec<Value> = (8..=58).map(alert).collect();
        let cases: Vec<(&str, Option<u64>, Vec<u8>)> = vec![
            ("not json", Some(7), b"<html>".to_vec()),
            ("empty", Some(7), Vec::new()),
            ("version", Some(7), wrong_version.to_string().into_bytes()),
            ("fleet", Some(7), other_fleet.to_string().into_bytes()),
            ("missing field", Some(7), missing.to_string().into_bytes()),
            ("51 alerts", Some(7), body(58, many)),
            ("replay on first poll", None, body(8, vec![alert(8)])),
            ("seq not after", Some(8), body(8, vec![alert(8)])),
            ("seq descending", Some(7), body(9, vec![alert(9), alert(8)])),
            ("empty answer above after", Some(7), body(999, vec![])),
            (
                "cursor is not the last seq",
                Some(7),
                body(12, vec![alert(8)]),
            ),
            (
                "negative seq",
                Some(7),
                body(
                    8,
                    vec![json!({"seq": -1, "kind": "k", "ref": "r", "text": "t", "link": ""})],
                ),
            ),
        ];
        for (name, after, bytes) in cases {
            assert!(validate(FLEET, after, &bytes).is_none(), "{name}");
        }
    }

    #[test]
    fn format_and_separator_characters_are_dropped_and_a_blank_alert_is_skipped() {
        let mut spoof = alert(8);
        spoof["text"] = json!("a\u{202e}b\u{200b}c\u{2028}d\u{2069}e\u{feff}");
        let mut blank = alert(9);
        blank["text"] = json!(" \u{202e}\u{2028}\u{a0}\u{200b}\t");
        let answer = validate(FLEET, Some(7), &body(9, vec![spoof, blank])).unwrap();
        assert_eq!(answer.cursor, 9);
        assert_eq!(answer.alerts.len(), 1);
        assert_eq!(answer.alerts[0].text, "abc de");
        let mut all_blank = alert(8);
        all_blank["text"] = json!("\u{200b}");
        let mut skipped = json!({"version": 1, "fleet": {"id": FLEET, "name": "h"}, "cursor": 8, "more": true, "alerts": [all_blank]});
        skipped["more"] = json!(true);
        let answer = validate(FLEET, Some(7), skipped.to_string().as_bytes()).unwrap();
        assert!(answer.alerts.is_empty() && answer.more && answer.cursor == 8);
    }

    #[test]
    fn a_cursor_that_fell_behind_restarts_and_more_needs_alerts() {
        let reset = validate(FLEET, Some(40), &body(3, vec![])).unwrap();
        assert_eq!((reset.cursor, reset.alerts.len()), (3, 0));
        let mut spin = json!({"version": 1, "fleet": {"id": FLEET, "name": "h"}, "cursor": 3, "more": true, "alerts": []});
        assert!(
            !validate(FLEET, Some(3), spin.to_string().as_bytes())
                .unwrap()
                .more
        );
        spin["alerts"] = json!([alert(4)]);
        spin["cursor"] = json!(4);
        assert!(
            validate(FLEET, Some(3), spin.to_string().as_bytes())
                .unwrap()
                .more
        );
    }

    #[test]
    fn the_cursor_is_written_by_rename_and_a_bad_file_is_no_cursor() {
        let dir = TempDir::new();
        let path = dir.path().join("alerts").join("f0123456789ab.cursor");
        assert_eq!(read_cursor(&path), None);
        write_cursor(&path, 41).unwrap();
        write_cursor(&path, 42).unwrap();
        assert_eq!(read_cursor(&path), Some(42));
        assert_eq!(fs::read_dir(path.parent().unwrap()).unwrap().count(), 1);
        for bad in ["", "abc", "-1", "1 2"] {
            fs::write(&path, bad).unwrap();
            assert_eq!(read_cursor(&path), None, "{bad:?}");
        }
    }

    #[test]
    fn the_contracts_numbers_and_exchanges_hold() {
        let vectors: Value =
            serde_json::from_str(include_str!("../tests/companion-vectors.json")).unwrap();
        let alerts = &vectors["alerts"];
        assert_eq!(alerts["wait_seconds_max"], WAIT.as_secs());
        assert_eq!(alerts["batch_max"], MAX_ALERTS);
        assert_eq!(alerts["text_max_runes"], MAX_TEXT);
        let exchanges = alerts["exchanges"].as_array().unwrap();
        assert!(!exchanges.is_empty());
        for exchange in exchanges {
            let after = exchange["request"]
                .as_str()
                .unwrap()
                .trim_start_matches('?')
                .split('&')
                .find_map(|pair| pair.strip_prefix("after=")?.parse().ok());
            let answer = validate(FLEET, after, exchange["body"].to_string().as_bytes())
                .unwrap_or_else(|| panic!("{}", exchange["name"]));
            let expected = exchange["body"]["alerts"].as_array().unwrap();
            assert_eq!(answer.alerts.len(), expected.len(), "{}", exchange["name"]);
            for (got, want) in answer.alerts.iter().zip(expected) {
                assert_eq!(
                    got.link.as_deref(),
                    want["link"].as_str(),
                    "{}",
                    exchange["name"]
                );
                assert_eq!(got.text, want["text"].as_str().unwrap());
            }
        }
    }
}
