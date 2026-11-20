use std::collections::HashMap;
use std::future::Future;
use std::pin::Pin;
use std::sync::{Arc, Mutex};

use iroh::{Endpoint, EndpointAddr};

use crate::dialer::Dialer;

pub type Route =
    Arc<dyn Fn(String) -> Pin<Box<dyn Future<Output = Option<Dialer>> + Send>> + Send + Sync>;

pub fn fixed(dialer: Dialer) -> Route {
    Arc::new(move |_| {
        let dialer = dialer.clone();
        Box::pin(async move { Some(dialer) })
    })
}

struct Held {
    endpoint: Endpoint,
    down: bool,
    hosts: HashMap<String, Dialer>,
}

// Last in the lock order (Companion::store, pairing, session data, this): copy what you need out
// of the guard and drop it before any await.
#[derive(Clone)]
pub struct Dialers(Arc<Mutex<Held>>);

impl Dialers {
    pub fn new(endpoint: Endpoint) -> Self {
        Self(Arc::new(Mutex::new(Held {
            endpoint,
            down: false,
            hosts: HashMap::new(),
        })))
    }

    pub fn endpoint(&self) -> Endpoint {
        self.0.lock().unwrap().endpoint.clone()
    }

    pub fn is_down(&self) -> bool {
        self.0.lock().unwrap().down
    }

    pub fn get(&self, host: &str) -> Option<Dialer> {
        self.0.lock().unwrap().hosts.get(host).cloned()
    }

    pub fn all(&self) -> Vec<Dialer> {
        self.0.lock().unwrap().hosts.values().cloned().collect()
    }

    pub async fn host(&self, host: &str, target: EndpointAddr) -> Dialer {
        let dialer = {
            let mut held = self.0.lock().unwrap();
            let (endpoint, down) = (held.endpoint.clone(), held.down);
            held.hosts
                .entry(host.to_owned())
                .or_insert_with(|| {
                    let dialer = Dialer::new(endpoint, None);
                    if down {
                        dialer.down();
                    }
                    dialer
                })
                .clone()
        };
        let same = dialer
            .target()
            .await
            .is_some_and(|held| held.id == target.id && held.relay_urls().eq(target.relay_urls()));
        if !same {
            dialer.set_target(Some(target)).await;
        }
        dialer
    }

    pub async fn remove(&self, host: &str) {
        let removed = self.0.lock().unwrap().hosts.remove(host);
        if let Some(dialer) = removed {
            dialer.set_target(None).await;
        }
    }

    pub async fn close(&self) {
        let endpoint = {
            let mut held = self.0.lock().unwrap();
            held.down = true;
            held.hosts.values().for_each(Dialer::down);
            held.endpoint.clone()
        };
        endpoint.close().await;
    }

    pub fn open(&self, endpoint: Endpoint) {
        let mut held = self.0.lock().unwrap();
        held.down = false;
        for dialer in held.hosts.values() {
            dialer.open(endpoint.clone());
        }
        held.endpoint = endpoint;
    }
}
