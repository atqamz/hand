use std::fmt;
use std::io;
use std::path::PathBuf;

use iroh::EndpointAddr;
use iroh::endpoint::{Endpoint, QuicTransportConfig, VarInt, presets};

use crate::dialer::{ALPN, Dialer, STREAMS_PER_CONNECTION};
use crate::key;
use crate::proxy::{self, Proxy};

pub struct CompanionConfig {
    pub data_dir: PathBuf,
}

#[derive(Clone)]
pub struct Companion {
    dialer: Dialer,
    port: u16,
}

impl Companion {
    pub async fn start(config: CompanionConfig) -> Result<Self, StartError> {
        std::fs::create_dir_all(&config.data_dir)?;
        let secret_key = key::load_or_create(&config.data_dir)?;
        let transport = QuicTransportConfig::builder()
            .max_concurrent_bidi_streams(VarInt::from_u32(STREAMS_PER_CONNECTION as u32))
            .max_concurrent_uni_streams(VarInt::from_u32(0))
            .build();
        let endpoint = Endpoint::builder(presets::N0)
            .secret_key(secret_key)
            .alpns(vec![ALPN.to_vec()])
            .transport_config(transport)
            .bind()
            .await
            .map_err(|error| StartError::Bind(error.to_string()))?;
        let (proxy, listeners) = Proxy::bind(&config.data_dir).await?;
        let dialer = Dialer::new(endpoint, None);
        for listener in listeners {
            tokio::spawn(proxy::serve(listener, dialer.clone()));
        }
        Ok(Self {
            dialer,
            port: proxy.port(),
        })
    }

    pub fn port(&self) -> u16 {
        self.port
    }

    pub fn endpoint_id(&self) -> iroh::EndpointId {
        self.dialer.endpoint_id()
    }

    pub fn url(&self, path: &str) -> String {
        let path = if path.starts_with('/') {
            path.to_owned()
        } else {
            format!("/{path}")
        };
        format!("http://localhost:{}{path}", self.port)
    }

    pub async fn set_target(&self, target: Option<EndpointAddr>) {
        self.dialer.set_target(target).await;
    }

    pub async fn reconnect_on_resume(&self) {
        self.dialer.disconnect().await;
    }
}

#[derive(Debug)]
pub enum StartError {
    Io(io::Error),
    Bind(String),
}

impl fmt::Display for StartError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Io(error) => write!(f, "{error}"),
            Self::Bind(error) => write!(f, "cannot open the iroh endpoint: {error}"),
        }
    }
}

impl std::error::Error for StartError {}

impl From<io::Error> for StartError {
    fn from(error: io::Error) -> Self {
        Self::Io(error)
    }
}
