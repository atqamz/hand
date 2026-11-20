use std::future::Future;
use std::pin::Pin;

use tokio::time::{Instant, sleep_until};

pub type Sleep = Pin<Box<dyn Future<Output = ()> + Send>>;

pub trait Clock: Send + Sync {
    fn now(&self) -> Instant;
    fn sleep_until(&self, at: Instant) -> Sleep;
}

pub struct Real;

impl Clock for Real {
    fn now(&self) -> Instant {
        Instant::now()
    }

    fn sleep_until(&self, at: Instant) -> Sleep {
        Box::pin(sleep_until(at))
    }
}
