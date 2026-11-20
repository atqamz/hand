use std::mem;

use crate::link::{Link, Reject, parse_link_with};

pub const PAIRING_PENDING: &str = "Finish or dismiss the current pairing first";

#[derive(Default)]
pub struct Pairing {
    pending: Option<Link>,
    in_flight: bool,
    generation: u64,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Offer {
    pub host: String,
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
    pub fn offer(&mut self, text: &str) -> Result<Offer, OfferError> {
        if self.pending.is_some() {
            return Err(OfferError::Pending);
        }
        let link = parse_link_with(text, |endpoint| {
            endpoint.parse::<iroh::EndpointId>().is_ok()
        })
        .map_err(OfferError::Rejected)?;
        let host = link.confirmation();
        self.pending = Some(link);
        Ok(Offer { host })
    }

    pub fn pending(&self) -> Option<&Link> {
        self.pending.as_ref()
    }

    pub fn pending_offer(&self) -> Option<Offer> {
        self.pending.as_ref().map(|link| Offer {
            host: link.confirmation(),
        })
    }

    pub fn clear_pending(&mut self) {
        self.pending = None;
        self.in_flight = false;
    }

    pub fn generation(&self) -> u64 {
        self.generation
    }

    pub fn forget(&mut self) {
        self.in_flight = false;
        self.generation = self.generation.wrapping_add(1);
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
}

#[cfg(test)]
mod tests {
    use super::*;

    const FIRST: &str = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6";
    const SECOND: &str = "d6e458a66697094c1bc7b54cb83fea66a8db57c49f37514944579712cd7e43c5";
    const UNKNOWN: &str = "b6fcff633c251a1f5a8598247c85986e1acc1d0a8d6a3e055f7c4f737843783b";

    fn link(endpoint: &str) -> String {
        format!("handboard://c#v=1&e={endpoint}&f=f0123456789ab&c=K7QM3XRD2P")
    }

    #[test]
    fn offering_a_link_dials_nothing_and_pends_it() {
        let mut pairing = Pairing::default();
        let offer = pairing.offer(&link(FIRST)).unwrap();
        assert_eq!(offer.host, "4d92…6ed6");
        assert!(pairing.pending().is_some());
    }

    #[test]
    fn a_taken_link_comes_back_only_when_no_newer_one_has_arrived() {
        let mut pairing = Pairing::default();
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
    fn a_link_taken_before_a_forget_does_not_come_back() {
        let mut pairing = Pairing::default();
        pairing.offer(&link(FIRST)).unwrap();
        let taken = pairing.take().unwrap();
        let generation = pairing.generation();

        pairing.forget();

        assert_eq!(pairing.generation(), generation + 1);
        pairing.restore(taken);
        assert!(pairing.pending().is_none());
    }

    #[test]
    fn a_link_while_one_is_pending_changes_nothing() {
        let mut pairing = Pairing::default();
        let first = pairing.offer(&link(FIRST)).unwrap();
        assert_eq!(pairing.offer(&link(SECOND)), Err(OfferError::Pending));
        assert_eq!(pairing.pending().unwrap().endpoint_id, FIRST);
        assert_eq!(pairing.pending().unwrap().confirmation(), first.host);
        assert_eq!(pairing.pending_offer().unwrap().host, "4d92…6ed6");
    }

    #[test]
    fn an_unknown_link_is_rejected_when_nothing_is_pending() {
        let mut pairing = Pairing::default();
        assert_eq!(
            pairing.offer("not-a-link"),
            Err(OfferError::Rejected(Reject::Scheme))
        );
        assert!(pairing.pending().is_none());
    }

    #[test]
    fn an_iroh_invalid_key_is_endpoint_id_before_fleet_code_or_relay() {
        let mut pairing = Pairing::default();
        assert_eq!(
            pairing.offer(&format!("handboard://c#v=1&e={UNKNOWN}&f=bad&c=bad&r=bad")),
            Err(OfferError::Rejected(Reject::EndpointId))
        );
    }
}
