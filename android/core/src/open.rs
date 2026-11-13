pub const OPEN_REJECTED: &str = "This is not a link Hand can open.";
pub const NOT_PAIRED: &str = "Pair this phone with your laptop first.";

const HOST_PREFIX: &str = "handboard://open";
const PREFIX: &str = "handboard://open/";

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct OpenLink {
    pub fleet: String,
    pub page: String,
    pub report: Option<String>,
}

impl OpenLink {
    pub fn path(&self) -> String {
        let mut path = format!("/{}/{}", self.fleet, self.page);
        if let Some(report) = &self.report {
            path.push('#');
            path.push_str(report);
        }
        path
    }
}

pub fn open_paired(paired: &[String], text: &str) -> Result<OpenLink, &'static str> {
    if paired.is_empty() {
        return Err(NOT_PAIRED);
    }
    parse_open(text)
        .filter(|link| paired.contains(&link.fleet))
        .ok_or(OPEN_REJECTED)
}

pub fn is_open_link(text: &str) -> bool {
    text.starts_with(HOST_PREFIX)
}

pub fn parse_open(text: &str) -> Option<OpenLink> {
    let rest = text.strip_prefix(PREFIX)?;
    let (rest, report) = match rest.split_once('#') {
        Some((rest, report)) => (rest, Some(report)),
        None => (rest, None),
    };
    let (fleet, page) = rest.split_once('/')?;
    if !valid_fleet(fleet) || !valid_page(page) {
        return None;
    }
    let report = match report {
        Some(report) if page.starts_with("task/") && numbered(report, 'r') => Some(report),
        Some(_) => return None,
        None => None,
    };
    Some(OpenLink {
        fleet: fleet.to_owned(),
        page: page.to_owned(),
        report: report.map(str::to_owned),
    })
}

pub(crate) fn valid_fleet(fleet: &str) -> bool {
    fleet.len() == 13
        && fleet.starts_with('f')
        && fleet[1..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn valid_page(page: &str) -> bool {
    match page.split_once('/') {
        None => page.is_empty(),
        Some(("task", id)) => numbered(id, 't'),
        Some(("decision", id)) => numbered(id, 'd'),
        Some(("attempt", id)) => numbered(id, 'a'),
        Some(_) => false,
    }
}

fn numbered(text: &str, letter: char) -> bool {
    let Some(digits) = text.strip_prefix(letter) else {
        return false;
    };
    (1..=9).contains(&digits.len())
        && !digits.starts_with('0')
        && digits.bytes().all(|byte| byte.is_ascii_digit())
}

#[cfg(test)]
mod tests {
    use super::*;

    const FLEET: &str = "f0123456789ab";

    fn open(tail: &str) -> String {
        format!("handboard://open/{FLEET}/{tail}")
    }

    #[test]
    fn a_link_opens_only_a_paired_fleet() {
        let paired = vec![FLEET.to_owned()];
        let task = open("task/t3");
        assert_eq!(
            open_paired(&paired, &task).unwrap().path(),
            "/f0123456789ab/task/t3"
        );
        assert_eq!(open_paired(&[], &task), Err(NOT_PAIRED));
        let other = vec!["f999999999999".to_owned()];
        assert_eq!(open_paired(&other, &task), Err(OPEN_REJECTED));
        for text in [
            "handboard://c#v1.abc",
            "handboard://open/f0123456789ab/report/r1",
            "https://example.test/",
            "",
        ] {
            assert_eq!(open_paired(&paired, text), Err(OPEN_REJECTED), "{text}");
        }
    }

    #[test]
    fn the_five_forms_open_their_board_path() {
        let cases = [
            ("task/t3", "/f0123456789ab/task/t3"),
            ("decision/d3", "/f0123456789ab/decision/d3"),
            ("attempt/a7", "/f0123456789ab/attempt/a7"),
            ("task/t3#r5", "/f0123456789ab/task/t3#r5"),
            ("", "/f0123456789ab/"),
            ("task/t999999999", "/f0123456789ab/task/t999999999"),
        ];
        for (tail, path) in cases {
            let link = parse_open(&open(tail)).unwrap_or_else(|| panic!("{tail}"));
            assert_eq!(link.fleet, FLEET, "{tail}");
            assert_eq!(link.path(), path, "{tail}");
        }
    }

    #[test]
    fn anything_else_is_rejected() {
        let cases = [
            "",
            "handboard://open",
            "handboard://open/",
            "handboard://open/f0123456789ab",
            "handboard://c/f0123456789ab/task/t3",
            "handboard://c#v=1&e=4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6&f=f0123456789ab&c=K7QM3XRD2P",
            "HANDBOARD://open/f0123456789ab/task/t3",
            "handboard://OPEN/f0123456789ab/task/t3",
            "handboard://opener/f0123456789ab/task/t3",
            "http://localhost:1/f0123456789ab/task/t3",
            " handboard://open/f0123456789ab/task/t3",
            "handboard://open/f0123456789ab/task/t3 ",
            "handboard://open/f0123456789ab/task/t3\n",
            "handboard://open/f0123456789abc/task/t3",
            "handboard://open/F0123456789AB/task/t3",
            "handboard://open/f0123456789a/task/t3",
            "handboard://open/g0123456789ab/task/t3",
            "handboard://open/f0123456789ab//task/t3",
            "handboard://open/f0123456789ab/task/t3/",
            "handboard://open/f0123456789ab/task/t3/extra",
            "handboard://open/f0123456789ab/task",
            "handboard://open/f0123456789ab/task/",
            "handboard://open/f0123456789ab/task/t",
            "handboard://open/f0123456789ab/task/t0",
            "handboard://open/f0123456789ab/task/t03",
            "handboard://open/f0123456789ab/task/t1234567890",
            "handboard://open/f0123456789ab/task/T3",
            "handboard://open/f0123456789ab/task/3",
            "handboard://open/f0123456789ab/task/d3",
            "handboard://open/f0123456789ab/decision/t3",
            "handboard://open/f0123456789ab/attempt/d3",
            "handboard://open/f0123456789ab/Task/t3",
            "handboard://open/f0123456789ab/report/r1",
            "handboard://open/f0123456789ab/pair",
            "handboard://open/f0123456789ab/../task/t3",
            "handboard://open/f0123456789ab/task/..",
            "handboard://open/f0123456789ab/%74ask/t3",
            "handboard://open/f0123456789ab/task%2Ft3",
            "handboard://open/f0123456789ab/task%2ft3",
            "handboard://open/f0123456789ab/task/%2e%2e",
            "handboard://open/f0123456789ab/task/t3?x=1",
            "handboard://open/f0123456789ab/?x=1",
            "handboard://open/f0123456789ab/task/t3#",
            "handboard://open/f0123456789ab/task/t3#r",
            "handboard://open/f0123456789ab/task/t3#r0",
            "handboard://open/f0123456789ab/task/t3#r1234567890",
            "handboard://open/f0123456789ab/task/t3#R5",
            "handboard://open/f0123456789ab/task/t3#t5",
            "handboard://open/f0123456789ab/task/t3#r5#r6",
            "handboard://open/f0123456789ab/task/t3#r5?x",
            "handboard://open/f0123456789ab/decision/d3#r1",
            "handboard://open/f0123456789ab/attempt/a7#r1",
            "handboard://open/f0123456789ab/#r1",
            "handboard://user@open/f0123456789ab/task/t3",
            "handboard://open:80/f0123456789ab/task/t3",
            "handboard://open@evil.test/f0123456789ab/task/t3",
            "handboard://open/f0123456789ab@evil.test/task/t3",
            "handboard://open/f0123456789ab/task/t３",
            "handboard://open/f0123456789ab/task/t3\u{0}",
            "handboard://open/f0123456789ab/task\\t3",
            "handboard://open/f0123456789ab/task/t3;x",
        ];
        for text in cases {
            assert_eq!(parse_open(text), None, "{text:?}");
        }
    }

    #[test]
    fn a_fleet_of_the_right_length_and_head_is_rejected_for_its_tail() {
        let fleets = [
            "f0123456789xy",
            "f01234567g9ab",
            "f0123456789aB",
            "f012345678%2e",
            "f012345678\n9a",
            "f012345678\r\na",
            "f0123456789\u{7f}a",
            "f0123456789é",
        ];
        for fleet in fleets {
            assert_eq!(fleet.len(), 13, "{fleet:?}");
            assert!(fleet.starts_with('f'), "{fleet:?}");
            assert_eq!(
                parse_open(&format!("handboard://open/{fleet}/task/t3")),
                None,
                "{fleet:?}"
            );
        }
    }

    #[test]
    fn only_the_open_host_is_routed_to_open_link() {
        for text in [
            "handboard://open",
            "handboard://open/",
            "handboard://open/zzz",
            "handboard://opener/f0123456789ab/task/t3",
            "handboard://open/f0123456789ab/task/t3#r5",
        ] {
            assert!(is_open_link(text), "{text}");
        }
        for text in [
            "",
            "handboard://c#v1.abc",
            "handboard://c/open",
            "HANDBOARD://open/f0123456789ab/task/t3",
            " handboard://open/f0123456789ab/task/t3",
            "http://localhost:1/open",
        ] {
            assert!(!is_open_link(text), "{text}");
        }
    }
}
