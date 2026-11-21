use std::collections::HashMap;

use crate::link::{Link, Reject, parse_link_with};

pub const PAIRING_PENDING: &str = "Finish or dismiss the current pairing first";

#[derive(Default)]
pub struct Pairing {
    pending: Option<Link>,
    in_flight: Option<String>,
    generation: u64,
    hosts: HashMap<String, u64>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Offer {
    pub host: String,
    pub claim: Option<String>,
    pub clash: bool,
}

impl Offer {
    fn of(link: &Link, clashes: impl Fn(&str, &str) -> bool) -> Self {
        Self {
            host: link.confirmation(),
            claim: link.name.clone(),
            clash: link
                .name
                .as_deref()
                .is_some_and(|claim| clashes(&link.endpoint_id, claim)),
        }
    }
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
    pub fn offer(
        &mut self,
        text: &str,
        clashes: impl Fn(&str, &str) -> bool,
    ) -> Result<Offer, OfferError> {
        if self.pending.is_some() {
            return Err(OfferError::Pending);
        }
        let link = parse_link_with(text, |endpoint| {
            endpoint.parse::<iroh::EndpointId>().is_ok()
        })
        .map_err(OfferError::Rejected)?;
        let offer = Offer::of(&link, clashes);
        self.pending = Some(link);
        Ok(offer)
    }

    pub fn pending(&self) -> Option<&Link> {
        self.pending.as_ref()
    }

    pub fn pending_offer(&self, clashes: impl Fn(&str, &str) -> bool) -> Option<Offer> {
        self.pending.as_ref().map(|link| Offer::of(link, clashes))
    }

    pub fn clear_pending(&mut self) {
        self.pending = None;
        self.in_flight = None;
    }

    pub fn generation(&self, endpoint: &str) -> u64 {
        self.generation
            .wrapping_add(self.hosts.get(endpoint).copied().unwrap_or(0))
    }

    pub fn forget(&mut self) {
        self.in_flight = None;
        self.generation = self.generation.wrapping_add(1);
    }

    pub fn forget_host(&mut self, endpoint: &str) {
        let count = self.hosts.entry(endpoint.to_owned()).or_default();
        *count = count.wrapping_add(1);
        if self
            .pending
            .as_ref()
            .is_some_and(|link| link.endpoint_id == endpoint)
        {
            self.pending = None;
        }
        if self.in_flight.as_deref() == Some(endpoint) {
            self.in_flight = None;
        }
    }

    pub fn take(&mut self) -> Option<Link> {
        let link = self.pending.take()?;
        self.in_flight = Some(link.endpoint_id.clone());
        Some(link)
    }

    pub fn restore(&mut self, link: Link) {
        if self.in_flight.take().is_some() {
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

    fn never(_: &str, _: &str) -> bool {
        false
    }

    fn link(endpoint: &str) -> String {
        format!("handboard://c#v=1&e={endpoint}&f=f0123456789ab&c=K7QM3XRD2P")
    }

    #[test]
    fn offering_a_link_dials_nothing_and_pends_it() {
        let mut pairing = Pairing::default();
        let offer = pairing.offer(&link(FIRST), never).unwrap();
        assert_eq!(offer.host, "4d92…6ed6");
        assert!(pairing.pending().is_some());
    }

    #[test]
    fn a_taken_link_comes_back_only_when_no_newer_one_has_arrived() {
        let mut pairing = Pairing::default();
        pairing.offer(&link(FIRST), never).unwrap();
        let first = pairing.take().unwrap();
        assert!(pairing.pending().is_none());
        pairing.restore(first.clone());
        assert_eq!(pairing.pending().unwrap().endpoint_id, FIRST);

        pairing.take().unwrap();
        pairing.offer(&link(SECOND), never).unwrap();
        pairing.restore(first.clone());
        assert_eq!(pairing.pending().unwrap().endpoint_id, SECOND);

        pairing.clear_pending();
        pairing.offer(&link(FIRST), never).unwrap();
        let taken = pairing.take().unwrap();
        pairing.clear_pending();
        pairing.restore(taken);
        assert!(pairing.pending().is_none());
    }

    #[test]
    fn a_link_taken_before_a_forget_does_not_come_back() {
        let mut pairing = Pairing::default();
        pairing.offer(&link(FIRST), never).unwrap();
        let taken = pairing.take().unwrap();
        let generation = pairing.generation(FIRST);

        pairing.forget();

        assert_eq!(pairing.generation(FIRST), generation + 1);
        pairing.restore(taken);
        assert!(pairing.pending().is_none());
    }

    #[test]
    fn a_link_taken_before_its_hosts_forget_does_not_come_back_but_another_hosts_does() {
        let mut pairing = Pairing::default();
        pairing.offer(&link(FIRST), never).unwrap();
        let taken = pairing.take().unwrap();

        pairing.forget_host(SECOND);
        pairing.restore(taken.clone());
        assert_eq!(pairing.pending().unwrap().endpoint_id, FIRST);

        pairing.clear_pending();
        pairing.offer(&link(FIRST), never).unwrap();
        let taken = pairing.take().unwrap();
        pairing.forget_host(FIRST);
        pairing.restore(taken);
        assert!(pairing.pending().is_none());
    }

    #[test]
    fn forgetting_one_host_moves_only_that_hosts_generation_and_drops_its_pending_link() {
        let mut pairing = Pairing::default();
        let (first, second) = (pairing.generation(FIRST), pairing.generation(SECOND));
        pairing.offer(&link(FIRST), never).unwrap();

        pairing.forget_host(SECOND);
        assert_eq!(pairing.generation(FIRST), first);
        assert_ne!(pairing.generation(SECOND), second);
        assert!(pairing.pending().is_some());

        pairing.forget_host(FIRST);
        assert_ne!(pairing.generation(FIRST), first);
        assert!(pairing.pending().is_none());
    }

    #[test]
    fn a_link_while_one_is_pending_changes_nothing() {
        let mut pairing = Pairing::default();
        let first = pairing.offer(&link(FIRST), never).unwrap();
        assert_eq!(
            pairing.offer(&link(SECOND), never),
            Err(OfferError::Pending)
        );
        assert_eq!(pairing.pending().unwrap().endpoint_id, FIRST);
        assert_eq!(pairing.pending().unwrap().confirmation(), first.host);
        assert_eq!(pairing.pending_offer(never).unwrap().host, "4d92…6ed6");
    }

    #[test]
    fn an_unknown_link_is_rejected_when_nothing_is_pending() {
        let mut pairing = Pairing::default();
        assert_eq!(
            pairing.offer("not-a-link", never),
            Err(OfferError::Rejected(Reject::Scheme))
        );
        assert!(pairing.pending().is_none());
    }

    #[test]
    fn an_iroh_invalid_key_is_endpoint_id_before_fleet_code_or_relay() {
        let mut pairing = Pairing::default();
        assert_eq!(
            pairing.offer(
                &format!("handboard://c#v=1&e={UNKNOWN}&f=bad&c=bad&r=bad"),
                never
            ),
            Err(OfferError::Rejected(Reject::EndpointId))
        );
    }

    fn named(endpoint: &str, name: &str) -> String {
        format!("{}&n={name}", link(endpoint))
    }

    #[test]
    fn the_claim_survives_pending_offer_and_a_second_link_keeps_the_first_claim() {
        let mut pairing = Pairing::default();
        let offer = pairing.offer(&named(FIRST, "work-laptop"), never).unwrap();
        assert_eq!(offer.claim.as_deref(), Some("work-laptop"));
        assert_eq!(
            pairing.offer(&named(SECOND, "other"), never),
            Err(OfferError::Pending)
        );
        assert_eq!(pairing.pending_offer(never), Some(offer));
        assert_eq!(
            pairing.pending().unwrap().name.as_deref(),
            Some("work-laptop")
        );
    }

    #[test]
    fn a_link_without_a_name_has_no_claim_and_no_clash() {
        let mut pairing = Pairing::default();
        let offer = pairing.offer(&link(FIRST), |_, _| true).unwrap();
        assert_eq!((offer.claim, offer.clash), (None, false));
    }

    #[test]
    fn the_clash_is_asked_of_the_links_own_endpoint_and_claim() {
        let mut pairing = Pairing::default();
        let held = |endpoint: &str, claim: &str| endpoint == FIRST && claim == "work-laptop";
        assert!(
            pairing
                .offer(&named(FIRST, "work-laptop"), held)
                .unwrap()
                .clash
        );
        assert!(!pairing.pending_offer(never).unwrap().clash);
        assert!(pairing.pending_offer(held).unwrap().clash);
        pairing.clear_pending();
        assert!(!pairing.offer(&named(FIRST, "other"), held).unwrap().clash);
        pairing.clear_pending();
        assert!(
            !pairing
                .offer(&named(SECOND, "work-laptop"), held)
                .unwrap()
                .clash
        );
    }
}
