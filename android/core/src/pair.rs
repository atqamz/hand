use serde::Deserialize;

use crate::alerts::COOKIE_PREFIX;
use crate::dialer::Dialer;
use crate::http::{Request, RequestError, exchange};
use crate::link::Link;
use crate::open::valid_fleet;
use crate::sessions::Paired;

pub const NAME_MAX: usize = 40;
const ANSWER_MAX: usize = 64 << 10;
const REFUSALS: [(&str, &str); 6] = [
    (
        "bad_name",
        "Use a name of at most 40 characters with no control character.",
    ),
    (
        "wrong_code",
        "That code is not right. Check it against the one on your laptop, or show a new code there.",
    ),
    (
        "code_expired",
        "That code has expired. A code works for 5 minutes. Show a new code on your laptop.",
    ),
    (
        "code_used",
        "That code was already used. A code works once. Show a new code on your laptop.",
    ),
    (
        "codes_cancelled",
        "Too many wrong codes. Every code was cancelled. Show a new code on your laptop.",
    ),
    (
        "rate_limited",
        "Too many wrong codes from this phone. Wait a minute, then try again.",
    ),
];
const REFUSED: &str =
    "The laptop did not pair this phone. Show a new code on your laptop and try again.";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PairError {
    NoPending,
    NotSaved,
    Request(RequestError),
    Refused(&'static str),
}

impl PairError {
    pub fn name(self) -> &'static str {
        match self {
            Self::NoPending => "no_pending",
            Self::NotSaved => "not_saved",
            Self::Request(error) => error.name(),
            Self::Refused(code) => code,
        }
    }

    pub fn message(self) -> &'static str {
        match self {
            Self::NoPending => "This pairing was closed. Scan or paste the code again.",
            Self::NotSaved => concat!(
                "The phone could not save the pairing. Check its storage. ",
                "The laptop may hold an unfinished device: remove it on the laptop's Phone page, then show a new code and try again."
            ),
            Self::Request(RequestError::Busy) => "Your laptop is busy. Try again in a moment.",
            Self::Request(RequestError::NoLaptop | RequestError::Unreachable) => {
                "Can't reach your laptop. Check that it is on, that Hand's Board is running, and that this phone has internet."
            }
            Self::Request(RequestError::BoardNotRunning) => {
                "Your laptop answers, but the Board is not running. Start it on the laptop."
            }
            Self::Request(RequestError::Lost | RequestError::TimedOut) => {
                "The connection broke while pairing. If it fails again, show a new code on your laptop."
            }
            Self::Request(_) => concat!(
                "The laptop's answer could not be read. ",
                "The laptop may hold an unfinished device: remove it on the laptop's Phone page, then show a new code and try again."
            ),
            Self::Refused(code) => REFUSALS
                .iter()
                .find(|(known, _)| *known == code)
                .map_or(REFUSED, |(_, sentence)| sentence),
        }
    }
}

pub fn valid_name(name: &str) -> Option<&str> {
    let trimmed = name.trim();
    (!name.chars().any(char::is_control) && trimmed.chars().count() <= NAME_MAX).then_some(trimmed)
}

pub async fn redeem(dialer: &Dialer, link: &Link, name: &str) -> Result<Vec<Paired>, PairError> {
    let name = valid_name(name).ok_or(PairError::Refused("bad_name"))?;
    let body = serde_json::json!({ "code": link.code, "name": name }).to_string();
    let request = Request::pair(&link.fleet, body.as_bytes())
        .map_err(PairError::Request)?
        .up_to(ANSWER_MAX);
    let response = exchange(dialer, &request)
        .await
        .map_err(PairError::Request)?;
    parse_answer(response.status, &response.body)
}

#[derive(Deserialize)]
struct Answer {
    version: u8,
    data: Data,
}

#[derive(Deserialize)]
struct Data {
    fleets: Vec<Entry>,
}

#[derive(Deserialize)]
struct Entry {
    id: String,
    name: String,
    cookie_name: String,
    cookie_value: String,
}

#[derive(Deserialize)]
struct Failure {
    error: Detail,
}

#[derive(Deserialize)]
struct Detail {
    code: String,
}

pub fn parse_answer(status: u16, body: &[u8]) -> Result<Vec<Paired>, PairError> {
    if status != 200 {
        let code = serde_json::from_slice::<Failure>(body)
            .map(|failure| failure.error.code)
            .unwrap_or_default();
        return Err(PairError::Refused(
            REFUSALS
                .iter()
                .find(|(known, _)| *known == code)
                .map_or("refused", |(known, _)| known),
        ));
    }
    let malformed = PairError::Request(RequestError::Malformed);
    let answer: Answer = serde_json::from_slice(body).map_err(|_| malformed)?;
    if answer.version != 1 {
        return Err(malformed);
    }
    let mut paired: Vec<Paired> = Vec::new();
    for entry in answer.data.fleets {
        if !valid_fleet(&entry.id)
            || entry.cookie_name != format!("{COOKIE_PREFIX}{}", entry.id)
            || !valid_value(&entry.cookie_value)
            || paired.iter().any(|known| known.id == entry.id)
        {
            return Err(malformed);
        }
        paired.push(Paired {
            id: entry.id,
            name: clean(&entry.name),
            value: entry.cookie_value,
        });
    }
    if paired.is_empty() {
        return Err(malformed);
    }
    Ok(paired)
}

fn clean(name: &str) -> String {
    let name: String = name.chars().filter(|c| !c.is_control()).collect();
    name.trim().chars().take(NAME_MAX).collect()
}

fn valid_value(value: &str) -> bool {
    value.split_once('.').is_some_and(|(device, secret)| {
        !device.starts_with('0')
            && !device.is_empty()
            && device.bytes().all(|byte| byte.is_ascii_digit())
            && secret.len() == 64
            && secret
                .bytes()
                .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    const SECRET: &str = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";

    fn answer(fleets: &str) -> Vec<u8> {
        format!(r#"{{"version":1,"fleet":{{"id":"f0123456789ab","name":"hand"}},"data":{{"fleets":[{fleets}],"missed":0}}}}"#)
            .into_bytes()
    }

    fn entry(id: &str, cookie_name: &str, value: &str) -> String {
        format!(
            r#"{{"id":"{id}","name":"hand","cookie_name":"{cookie_name}","cookie_value":"{value}"}}"#
        )
    }

    #[test]
    fn a_name_has_no_control_character_and_at_most_forty_characters_once_trimmed() {
        assert_eq!(valid_name("  Pixel 8  "), Some("Pixel 8"));
        assert_eq!(valid_name(""), Some(""));
        assert!(valid_name(&"é".repeat(40)).is_some());
        assert!(valid_name(&"é".repeat(41)).is_none());
        assert!(valid_name(&format!("{}   ", "x".repeat(40))).is_some());
        assert!(valid_name("Pixel\n").is_none());
        assert!(valid_name("Pi\u{7}xel").is_none());
    }

    #[test]
    fn each_refusal_has_its_sentence() {
        let sentence = |code: &str| {
            let body = format!(r#"{{"error":{{"code":"{code}","message":"x"}}}}"#);
            parse_answer(403, body.as_bytes()).err().unwrap().message()
        };
        assert_eq!(
            sentence("wrong_code"),
            "That code is not right. Check it against the one on your laptop, or show a new code there."
        );
        assert_eq!(
            sentence("code_expired"),
            "That code has expired. A code works for 5 minutes. Show a new code on your laptop."
        );
        assert_eq!(
            sentence("code_used"),
            "That code was already used. A code works once. Show a new code on your laptop."
        );
        assert_eq!(
            sentence("codes_cancelled"),
            "Too many wrong codes. Every code was cancelled. Show a new code on your laptop."
        );
        assert_eq!(
            sentence("rate_limited"),
            "Too many wrong codes from this phone. Wait a minute, then try again."
        );
        assert_eq!(
            sentence("bad_name"),
            "Use a name of at most 40 characters with no control character."
        );
        assert_eq!(
            sentence("forbidden"),
            "The laptop did not pair this phone. Show a new code on your laptop and try again."
        );
        assert_eq!(
            parse_answer(500, b"not json").err(),
            Some(PairError::Refused("refused"))
        );
    }

    #[test]
    fn a_failure_of_the_laptop_has_its_sentence() {
        let message = |error| PairError::Request(error).message();
        assert_eq!(
            message(RequestError::Busy),
            "Your laptop is busy. Try again in a moment."
        );
        assert!(message(RequestError::Unreachable).starts_with("Can't reach your laptop."));
        assert!(message(RequestError::BoardNotRunning).contains("the Board is not running"));
        assert!(message(RequestError::Lost).contains("show a new code"));
        assert!(message(RequestError::Malformed).contains("could not be read"));
        for unfinished in [
            message(RequestError::Malformed),
            message(RequestError::TooLarge),
            PairError::NotSaved.message(),
        ] {
            assert!(unfinished.contains("remove it on the laptop's Phone page"));
        }
    }

    #[test]
    fn a_fleet_name_from_the_answer_loses_control_characters_and_is_cut_to_forty() {
        let value = format!("7.{SECRET}");
        let long = format!("  ho\u{7}me\n{}  ", "x".repeat(60));
        let name = serde_json::to_string(&long).unwrap();
        let body = answer(
            &entry("f0123456789ab", "hand_phone_f0123456789ab", &value)
                .replace(r#""name":"hand""#, &format!(r#""name":{name}"#)),
        );
        let paired = parse_answer(200, &body).unwrap();
        assert_eq!(paired[0].name.chars().count(), 40);
        assert!(paired[0].name.starts_with("home"));
        assert!(!paired[0].name.chars().any(char::is_control));
    }

    #[test]
    fn an_answer_gives_every_fleet_with_its_device_value() {
        let value = format!("7.{SECRET}");
        let body = answer(&format!(
            "{},{}",
            entry("f0123456789ab", "hand_phone_f0123456789ab", &value),
            entry("f999999999999", "hand_phone_f999999999999", &value)
        ));
        let paired = parse_answer(200, &body).unwrap();
        assert_eq!(paired.len(), 2);
        assert_eq!(paired[1].id, "f999999999999");
        assert_eq!(paired[0].value, value);
    }

    #[test]
    fn an_answer_that_breaks_the_contract_is_not_stored() {
        let good = format!("7.{SECRET}");
        let upper = format!("7.{}", SECRET.to_uppercase());
        let short = format!("7.{}", &SECRET[1..]);
        let cases = [
            answer(""),
            answer(&entry("f0123456789ab", "hand_phone_f999999999999", &good)),
            answer(&entry("F0123456789AB", "hand_phone_F0123456789AB", &good)),
            answer(&entry("f0123456789ab", "hand_phone_f0123456789ab", &upper)),
            answer(&entry("f0123456789ab", "hand_phone_f0123456789ab", &short)),
            answer(&entry(
                "f0123456789ab",
                "hand_phone_f0123456789ab",
                &format!("07.{SECRET}"),
            )),
            answer(&entry(
                "f0123456789ab",
                "hand_phone_f0123456789ab",
                &format!(".{SECRET}"),
            )),
            answer(&entry("f0123456789ab", "hand_phone_f0123456789ab", SECRET)),
            format!(
                "{},{}",
                String::from_utf8(answer(&entry(
                    "f0123456789ab",
                    "hand_phone_f0123456789ab",
                    &good
                )))
                .unwrap(),
                "x"
            )
            .into_bytes(),
            String::from_utf8(answer(&entry(
                "f0123456789ab",
                "hand_phone_f0123456789ab",
                &good,
            )))
            .unwrap()
            .replace(r#""version":1"#, r#""version":2"#)
            .into_bytes(),
            answer(&format!(
                "{0},{0}",
                entry("f0123456789ab", "hand_phone_f0123456789ab", &good)
            )),
        ];
        for body in cases {
            assert_eq!(
                parse_answer(200, &body).err(),
                Some(PairError::Request(RequestError::Malformed)),
                "{}",
                String::from_utf8_lossy(&body).replace(SECRET, "…")
            );
        }
    }
}
