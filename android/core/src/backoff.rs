use std::time::Duration;

const BASE_MAX: u64 = 30;
const JITTER: f64 = 0.25;

#[derive(Debug, Default)]
pub struct Backoff {
    failures: u32,
}

impl Backoff {
    pub fn reset(&mut self) {
        self.failures = 0;
    }

    pub fn next_delay(&mut self) -> Duration {
        let base = match self.failures {
            0 => 1,
            1 => 2,
            2 => 4,
            3 => 8,
            4 => 16,
            _ => BASE_MAX,
        };
        self.failures = (self.failures + 1).min(5);
        let jitter = rand::random_range(0.0..=JITTER);
        Duration::from_secs_f64(base as f64 * (1.0 + jitter))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn doubles_to_thirty_seconds_with_at_most_a_quarter_of_jitter() {
        let mut backoff = Backoff::default();
        let bases = [1, 2, 4, 8, 16, 30, 30, 30];
        for base in bases {
            let delay = backoff.next_delay();
            let low = Duration::from_secs(base);
            let high = Duration::from_secs_f64(base as f64 * 1.25);
            assert!(delay >= low, "{delay:?} < {low:?}");
            assert!(delay <= high, "{delay:?} > {high:?}");
        }
    }

    #[test]
    fn reset_starts_again_at_one_second() {
        let mut backoff = Backoff::default();
        for _ in 0..4 {
            backoff.next_delay();
        }
        backoff.reset();
        let delay = backoff.next_delay();
        assert!(delay >= Duration::from_secs(1));
        assert!(delay <= Duration::from_millis(1250));
    }
}
