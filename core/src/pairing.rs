use std::fs;
use std::io;
use std::path::{Path, PathBuf};

use crate::link::{Link, Reject, parse_link_with};

const LAPTOP_FILE: &str = "laptop";

pub const PAIRING_PENDING: &str = "Finish or dismiss the current pairing first";

pub struct Pairing {
    dir: PathBuf,
    pending: Option<Link>,
    active: Option<String>,
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
        let active = fs::read_to_string(dir.join(LAPTOP_FILE))
            .ok()
            .map(|text| text.trim().to_owned())
            .filter(|endpoint| valid_endpoint(endpoint));
        Self {
            dir: dir.to_owned(),
            pending: None,
            active,
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
    }

    pub fn active(&self) -> Option<&str> {
        self.active.as_deref()
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
        match fs::remove_file(self.dir.join(LAPTOP_FILE)) {
            Ok(()) => Ok(()),
            Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
            Err(error) => Err(error),
        }
    }

    pub fn activate(&mut self) -> io::Result<()> {
        let Some(pending) = &self.pending else {
            return Ok(());
        };
        fs::write(
            self.dir.join(LAPTOP_FILE),
            format!("{}\n", pending.endpoint_id),
        )?;
        self.active = Some(pending.endpoint_id.clone());
        Ok(())
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

    fn link(endpoint: &str) -> String {
        format!("handboard://c#v=1&e={endpoint}&f=f0123456789ab&c=K7QM3XRD2P")
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
        pairing.activate().unwrap();
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
        pairing.activate().unwrap();
        assert_eq!(pairing.active(), Some(SECOND));
        assert_eq!(Pairing::new(dir.path()).active(), Some(SECOND));
    }

    #[test]
    fn forgetting_removes_the_laptop_from_disk() {
        let dir = TempDir::new();
        let mut pairing = Pairing::new(dir.path());
        pairing.offer(&link(FIRST)).unwrap();
        pairing.activate().unwrap();
        pairing.forget().unwrap();
        assert!(pairing.active().is_none());
        assert!(Pairing::new(dir.path()).active().is_none());
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
