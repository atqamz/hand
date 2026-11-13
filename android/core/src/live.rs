use std::io;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use tokio::sync::Notify;
use tokio::time::sleep;

use crate::alerts::CookieSource;
use crate::backoff::Backoff;
use crate::dialer::{BACKOFF_RESET, Dialer};
use crate::http::{Method, Request, RequestError, SseStream};

pub const WATCHDOG: Duration = Duration::from_secs(35);
const EVENTS: &str = "/api/v1/events";

#[derive(Clone, Default)]
pub struct Cancel(Arc<CancelInner>);

#[derive(Default)]
struct CancelInner {
    cancelled: AtomicBool,
    notify: Notify,
}

impl Cancel {
    pub fn cancel(&self) {
        self.0.cancelled.store(true, Ordering::SeqCst);
        self.0.notify.notify_one();
    }

    pub fn is_cancelled(&self) -> bool {
        self.0.cancelled.load(Ordering::SeqCst)
    }

    pub async fn cancelled(&self) {
        if self.is_cancelled() {
            return;
        }
        self.0.notify.notified().await;
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum LiveEnd {
    Stopped,
    Unpaired,
}

pub trait LiveSink: Sync {
    fn live(&self, fleet: &str, topic: &str, json: &str) -> io::Result<()>;

    fn link(&self, state: &str, since: u64) -> io::Result<()>;
}

#[derive(Clone)]
pub struct Live {
    dialer: Dialer,
    watchdog: Duration,
}

enum Attempt {
    Stopped,
    Unpaired,
    Lost,
}

impl Live {
    pub fn new(dialer: Dialer) -> Self {
        Self {
            dialer,
            watchdog: WATCHDOG,
        }
    }

    pub fn with_watchdog(mut self, watchdog: Duration) -> Self {
        self.watchdog = watchdog;
        self
    }

    pub async fn run(
        &self,
        fleet: &str,
        cookies: &impl CookieSource,
        sink: &impl LiveSink,
        cancel: &Cancel,
    ) -> LiveEnd {
        let mut backoff = Backoff::default();
        let mut healthy = now_millis();
        loop {
            if cancel.is_cancelled() {
                return LiveEnd::Stopped;
            }
            let started = Instant::now();
            match self
                .attempt(fleet, cookies, sink, cancel, &mut healthy)
                .await
            {
                Ok(()) | Err(Attempt::Stopped) => return LiveEnd::Stopped,
                Err(Attempt::Unpaired) => {
                    let _ = sink.link("unpaired", healthy);
                    return LiveEnd::Unpaired;
                }
                Err(Attempt::Lost) => {
                    if started.elapsed() >= BACKOFF_RESET {
                        backoff.reset();
                    }
                    if sink.link("reconnecting", healthy).is_err() {
                        return LiveEnd::Stopped;
                    }
                    tokio::select! {
                        _ = sleep(backoff.next_delay()) => {}
                        _ = cancel.cancelled() => return LiveEnd::Stopped,
                    }
                }
            }
        }
    }

    async fn attempt(
        &self,
        fleet: &str,
        cookies: &impl CookieSource,
        sink: &impl LiveSink,
        cancel: &Cancel,
        healthy: &mut u64,
    ) -> Result<(), Attempt> {
        let header = match cookies.cookies().await {
            Ok(Some(header)) => header,
            Ok(None) => return Err(Attempt::Unpaired),
            Err(_) => return Err(Attempt::Lost),
        };
        let request = Request::new(Method::Get, fleet, EVENTS, Some(&header), None, &[]).map_err(
            |error| match error {
                RequestError::Unpaired => Attempt::Unpaired,
                _ => Attempt::Lost,
            },
        )?;
        let mut stream = SseStream::open(&self.dialer, &request, self.watchdog)
            .await
            .map_err(failure)?;
        if sink.link("live", 0).is_err() {
            return Err(Attempt::Stopped);
        }
        loop {
            tokio::select! {
                _ = cancel.cancelled() => return Err(Attempt::Stopped),
                event = stream.next(self.watchdog) => match event {
                    Ok(event) => {
                        *healthy = now_millis();
                        if sink.live(fleet, &event.name, &event.data).is_err() {
                            return Err(Attempt::Stopped);
                        }
                    }
                    Err(error) => return Err(failure(error)),
                },
            }
        }
    }
}

fn failure(error: RequestError) -> Attempt {
    match error {
        RequestError::Unpaired => Attempt::Unpaired,
        _ => Attempt::Lost,
    }
}

fn now_millis() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|elapsed| elapsed.as_millis() as u64)
        .unwrap_or(0)
}
