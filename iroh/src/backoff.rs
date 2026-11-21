use std::time::{Duration, SystemTime, UNIX_EPOCH};

pub const MAX_SECONDS: u64 = 30;
pub const JITTER: f64 = 0.25;

#[derive(Debug, Default)]
pub struct Backoff {
    failures: u32,
}

impl Backoff {
    pub fn reset(&mut self) {
        self.failures = 0;
    }

    #[cfg(test)]
    pub fn failures(&self) -> u32 {
        self.failures
    }

    pub fn next_delay(&mut self) -> Duration {
        self.next_with(clock_unit())
    }

    pub fn next_with(&mut self, unit: f64) -> Duration {
        let base = (1u64 << self.failures).min(MAX_SECONDS);
        self.failures = (self.failures + 1).min(16);
        Duration::from_secs_f64(base as f64 * (1.0 + JITTER * unit.clamp(0.0, 1.0)))
    }
}

fn clock_unit() -> f64 {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |since| since.subsec_nanos());
    f64::from(nanos % 1_000_000) / 999_999.0
}

#[cfg(test)]
mod tests {
    use std::collections::HashSet;
    use std::time::Instant;

    use super::*;

    #[test]
    fn doubles_to_thirty_seconds_and_stays_there() {
        let mut backoff = Backoff::default();
        for base in [1, 2, 4, 8, 16, 30, 30, 30, 30] {
            assert_eq!(backoff.next_with(0.0), Duration::from_secs(base));
        }
        for _ in 0..100 {
            assert!(backoff.next_with(1.0) <= Duration::from_millis(37_500));
        }
    }

    #[test]
    fn jitter_adds_up_to_a_quarter_and_no_more() {
        for (unit, expected) in [
            (0.0, 8.0),
            (0.5, 9.0),
            (1.0, 10.0),
            (7.0, 10.0),
            (-1.0, 8.0),
        ] {
            let mut backoff = Backoff::default();
            for _ in 0..3 {
                backoff.next_with(0.0);
            }
            assert_eq!(backoff.next_with(unit), Duration::from_secs_f64(expected));
        }
    }

    #[test]
    fn the_clock_gives_every_delay_its_own_jitter() {
        let deadline = Instant::now() + Duration::from_secs(5);
        let mut seen = HashSet::new();
        while seen.len() < 2 {
            assert!(Instant::now() < deadline, "every delay was the same");
            let mut backoff = Backoff::default();
            let delay = backoff.next_delay();
            assert!(delay >= Duration::from_secs(1) && delay <= Duration::from_millis(1250));
            seen.insert(delay);
        }
    }

    #[test]
    fn reset_starts_again_at_one_second() {
        let mut backoff = Backoff::default();
        for _ in 0..5 {
            backoff.next_with(0.0);
        }
        backoff.reset();
        assert_eq!(backoff.failures(), 0);
        assert_eq!(backoff.next_with(0.0), Duration::from_secs(1));
    }
}
