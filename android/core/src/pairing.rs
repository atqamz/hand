use std::fs;
use std::io;
use std::mem;
use std::path::{Path, PathBuf};

use crate::dialer::parse_target;
use crate::link::{Link, Reject, parse_link_with};

const LAPTOP_FILE: &str = "laptop";
const RELAY_FILE: &str = "laptop-relay";

pub const PAIRING_PENDING: &str = "Finish or dismiss the current pairing first";

pub struct Pairing {
    dir: PathBuf,
    pending: Option<Link>,
    in_flight: bool,
    active: Option<String>,
    relay: Option<String>,
    generation: u64,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Offer {
    pub laptop: String,
    pub replaces: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum OfferError {
    Rejected(Reject),
    Pending,
}

impl OfferError {
    pub fn message(&self) -> &'static str {
        match self {
            Self::Rejected(reject) => reject.message(),
            Self::Pending => PAIRING_PENDING,
        }
    }
}

impl Pairing {
    pub fn new(dir: &Path) -> Self {
        let (active, relay) = read_laptop(dir);
        Self {
            dir: dir.to_owned(),
            pending: None,
            in_flight: false,
            active,
            relay,
            generation: 0,
        }
    }

    pub fn offer(&mut self, text: &str) -> Result<Offer, OfferError> {
        if self.pending.is_some() {
            return Err(OfferError::Pending);
        }
        let link = parse_link_with(text, |endpoint| {
            endpoint.parse::<iroh::EndpointId>().is_ok()
        })
        .map_err(OfferError::Rejected)?;
        let replaces = self
            .active
            .as_deref()
            .is_some_and(|active| active != link.endpoint_id);
        let laptop = link.confirmation();
        self.pending = Some(link);
        Ok(Offer { laptop, replaces })
    }

    pub fn pending(&self) -> Option<&Link> {
        self.pending.as_ref()
    }

    pub fn pending_offer(&self) -> Option<Offer> {
        self.pending.as_ref().map(|link| Offer {
            laptop: link.confirmation(),
            replaces: self.replaces(),
        })
    }

    pub fn clear_pending(&mut self) {
        self.pending = None;
        self.in_flight = false;
    }

    pub fn active(&self) -> Option<&str> {
        self.active.as_deref()
    }

    pub fn active_relay(&self) -> Option<&str> {
        self.relay.as_deref()
    }

    pub fn generation(&self) -> u64 {
        self.generation
    }

    pub fn replaces(&self) -> bool {
        let Some(pending) = &self.pending else {
            return false;
        };
        self.active
            .as_deref()
            .is_some_and(|active| active != pending.endpoint_id)
    }

    pub fn clear_before_forgetting(
        &mut self,
        clear: impl FnOnce() -> io::Result<()>,
    ) -> io::Result<bool> {
        if !self.replaces() {
            return Ok(false);
        }
        clear()?;
        self.forget()?;
        Ok(true)
    }

    pub fn forget(&mut self) -> io::Result<()> {
        self.active = None;
        self.relay = None;
        self.in_flight = false;
        self.generation = self.generation.wrapping_add(1);
        let laptop = remove_if_present(&self.dir.join(LAPTOP_FILE));
        let relay = remove_if_present(&self.dir.join(RELAY_FILE));
        laptop.and(relay)
    }

    pub fn take(&mut self) -> Option<Link> {
        let link = self.pending.take()?;
        self.in_flight = true;
        Some(link)
    }

    pub fn restore(&mut self, link: Link) {
        if mem::take(&mut self.in_flight) {
            self.pending.get_or_insert(link);
        }
    }

    pub fn activate(&mut self, link: &Link) -> io::Result<()> {
        self.active = Some(link.endpoint_id.clone());
        self.relay = link.relay.clone();
        fs::write(
            self.dir.join(LAPTOP_FILE),
            format!("{}\n", link.endpoint_id),
        )?;
        match &link.relay {
            Some(relay) => fs::write(
                self.dir.join(RELAY_FILE),
                format!("{}\n{}\n", link.endpoint_id, relay),
            ),
            None => remove_if_present(&self.dir.join(RELAY_FILE)),
        }
    }
}

pub(crate) fn read_laptop(dir: &Path) -> (Option<String>, Option<String>) {
    let Ok(text) = fs::read_to_string(dir.join(LAPTOP_FILE)) else {
        return (None, None);
    };
    let mut lines = text.lines();
    let Some(endpoint) = lines.next().map(str::trim).filter(|e| valid_endpoint(e)) else {
        return (None, None);
    };
    let legacy = lines
        .next()
        .map(str::trim)
        .filter(|relay| parse_target(endpoint, Some(relay)).is_ok())
        .map(str::to_owned);
    let relay = read_relay(dir, endpoint).or(legacy);
    (Some(endpoint.to_owned()), relay)
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
    use std::cell::RefCell;

    use super::*;
    use crate::testutil::TempDir;

    const FIRST: &str = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6";
    const SECOND: &str = "d6e458a66697094c1bc7b54cb83fea66a8db57c49f37514944579712cd7e43c5";
    const UNKNOWN: &str = "b6fcff633c251a1f5a8598247c85986e1acc1d0a8d6a3e055f7c4f737843783b";

    fn activate(pairing: &mut Pairing) {
        let link = pairing.pending().cloned().unwrap();
        pairing.activate(&link).unwrap();
    }

    fn link(endpoint: &str) -> String {
        format!("handboard://c#v=1&e={endpoint}&f=f0123456789ab&c=K7QM3XRD2P")
    }

    fn relay_link(endpoint: &str) -> String {
        format!(
            "handboard://c#v=1&e={endpoint}&f=f0123456789ab&c=K7QM3XRD2P&r=https%3A%2F%2Frelay.example.test%3A7842%2F"
        )
    }

    #[test]
    fn a_laptops_relay_is_kept_with_it_and_read_again() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        pairing.offer(&relay_link(FIRST)).unwrap();
        let first = pairing.pending().cloned().unwrap();
        pairing.activate(&first).unwrap();

        let again = Pairing::new(dir.path());
        assert_eq!(again.active(), Some(FIRST));
        assert_eq!(
            again.active_relay(),
            Some("https://relay.example.test:7842/")
        );
        assert_eq!(
            parse_target(again.active().unwrap(), again.active_relay()).unwrap(),
            first.target().unwrap()
        );
    }

    #[test]
    fn a_relay_is_written_beside_a_one_line_laptop() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        pairing.offer(&relay_link(FIRST)).unwrap();
        activate(&mut pairing);

        assert_eq!(
            std::fs::read_to_string(dir.path().join("laptop")).unwrap(),
            format!("{FIRST}\n")
        );
        assert_eq!(
            std::fs::read_to_string(dir.path().join("laptop-relay")).unwrap(),
            format!("{FIRST}\nhttps://relay.example.test:7842/\n")
        );
    }

    #[test]
    fn a_two_line_laptop_file_from_a_previous_build_is_read_and_migrated() {
        let dir = TempDir::new();
        std::fs::write(
            dir.path().join("laptop"),
            format!("{FIRST}\nhttps://relay.example.test:7842/\n"),
        )
        .unwrap();

        let mut pairing = Pairing::new(dir.path());
        assert_eq!(pairing.active(), Some(FIRST));
        assert_eq!(
            pairing.active_relay(),
            Some("https://relay.example.test:7842/")
        );

        pairing.offer(&relay_link(FIRST)).unwrap();
        activate(&mut pairing);

        assert_eq!(
            std::fs::read_to_string(dir.path().join("laptop")).unwrap(),
            format!("{FIRST}\n")
        );
        assert_eq!(
            std::fs::read_to_string(dir.path().join("laptop-relay")).unwrap(),
            format!("{FIRST}\nhttps://relay.example.test:7842/\n")
        );
    }

    #[test]
    fn a_relay_file_for_another_laptop_is_not_read() {
        let dir = TempDir::new();
        std::fs::write(dir.path().join("laptop"), format!("{FIRST}\n")).unwrap();
        std::fs::write(
            dir.path().join("laptop-relay"),
            format!("{SECOND}\nhttps://relay.example.test:7842/\n"),
        )
        .unwrap();

        let pairing = Pairing::new(dir.path());
        assert_eq!(pairing.active(), Some(FIRST));
        assert_eq!(pairing.active_relay(), None);
    }

    #[test]
    fn offering_a_link_dials_nothing_and_changes_no_laptop() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        let offer = pairing.offer(&link(FIRST)).unwrap();
        assert_eq!(offer.laptop, "4d92…6ed6");
        assert!(!offer.replaces);
        assert!(pairing.pending().is_some());
        assert!(pairing.active().is_none());
        assert!(!pairing.replaces());
    }

    #[test]
    fn a_second_laptop_is_cleared_before_the_first_is_forgotten() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        let calls = RefCell::new(Vec::new());

        pairing.offer(&link(FIRST)).unwrap();
        let switched = pairing
            .clear_before_forgetting(|| {
                calls.borrow_mut().push("clear");
                Ok(())
            })
            .unwrap();
        assert!(!switched);
        assert!(calls.borrow().is_empty());
        activate(&mut pairing);
        pairing.clear_pending();
        assert_eq!(pairing.active(), Some(FIRST));

        let offer = pairing.offer(&link(SECOND)).unwrap();
        assert!(offer.replaces);
        let switched = pairing
            .clear_before_forgetting(|| {
                assert_eq!(
                    std::fs::read_to_string(dir.path().join("laptop")).unwrap(),
                    format!("{FIRST}\n")
                );
                calls.borrow_mut().push("clear");
                Ok(())
            })
            .unwrap();
        assert!(switched);
        assert_eq!(*calls.borrow(), ["clear"]);
        assert!(pairing.active().is_none());
        assert!(Pairing::new(dir.path()).active().is_none());
        activate(&mut pairing);
        assert_eq!(pairing.active(), Some(SECOND));
        assert_eq!(Pairing::new(dir.path()).active(), Some(SECOND));
    }

    #[test]
    fn a_taken_link_comes_back_only_when_no_newer_one_has_arrived() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        pairing.offer(&link(FIRST)).unwrap();
        let first = pairing.take().unwrap();
        assert!(pairing.pending().is_none());
        pairing.restore(first.clone());
        assert_eq!(pairing.pending().unwrap().endpoint_id, FIRST);

        pairing.take().unwrap();
        pairing.offer(&link(SECOND)).unwrap();
        pairing.restore(first.clone());
        assert_eq!(pairing.pending().unwrap().endpoint_id, SECOND);

        pairing.clear_pending();
        pairing.offer(&link(FIRST)).unwrap();
        let taken = pairing.take().unwrap();
        pairing.clear_pending();
        pairing.restore(taken);
        assert!(pairing.pending().is_none());
    }

    #[test]
    fn forgetting_removes_the_laptop_from_disk() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        pairing.offer(&relay_link(FIRST)).unwrap();
        activate(&mut pairing);
        pairing.forget().unwrap();
        assert!(pairing.active().is_none());
        assert!(pairing.active_relay().is_none());
        assert!(!dir.path().join("laptop-relay").exists());
        let again = Pairing::new(dir.path());
        assert!(again.active().is_none());
        assert!(again.active_relay().is_none());
    }

    #[test]
    fn a_link_taken_before_a_forget_does_not_come_back() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        pairing.offer(&link(FIRST)).unwrap();
        let taken = pairing.take().unwrap();
        let generation = pairing.generation();

        pairing.forget().unwrap();

        assert_eq!(pairing.generation(), generation + 1);
        pairing.restore(taken);
        assert!(pairing.pending().is_none());
    }

    #[test]
    fn a_laptop_file_that_cannot_be_written_still_changes_the_active_laptop() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        pairing.offer(&link(FIRST)).unwrap();
        activate(&mut pairing);
        pairing.clear_pending();
        std::fs::remove_file(dir.path().join("laptop")).unwrap();
        std::fs::create_dir(dir.path().join("laptop")).unwrap();

        let offer = pairing.offer(&link(SECOND)).unwrap();
        assert!(offer.replaces);
        let second = pairing.pending().cloned().unwrap();
        assert!(pairing.activate(&second).is_err());

        assert_eq!(pairing.active(), Some(SECOND));
    }

    #[test]
    fn a_link_while_one_is_pending_changes_nothing() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        let first = pairing.offer(&link(FIRST)).unwrap();
        assert_eq!(pairing.offer(&link(SECOND)), Err(OfferError::Pending));
        assert_eq!(pairing.pending().unwrap().endpoint_id, FIRST);
        assert_eq!(pairing.pending().unwrap().confirmation(), first.laptop);
        assert_eq!(pairing.pending_offer().unwrap().laptop, "4d92…6ed6");
    }

    #[test]
    fn an_unknown_link_is_rejected_when_nothing_is_pending() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        assert_eq!(
            pairing.offer("not-a-link"),
            Err(OfferError::Rejected(Reject::Scheme))
        );
        assert!(pairing.pending().is_none());
    }

    #[test]
    fn an_iroh_invalid_key_is_endpoint_id_before_fleet_code_or_relay() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        assert_eq!(
            pairing.offer(&format!("handboard://c#v=1&e={UNKNOWN}&f=bad&c=bad&r=bad")),
            Err(OfferError::Rejected(Reject::EndpointId))
        );
    }
}
