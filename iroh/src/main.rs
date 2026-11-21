mod args;
mod backoff;
mod client;
mod forward;
mod key;
mod preface;
#[cfg(test)]
mod testutil;

use std::io::Write as _;
use std::net::SocketAddr;
use std::process::ExitCode;
use std::sync::Arc;
use std::time::Duration;

use iroh::RelayMode;
use iroh::endpoint::{Endpoint, PortmapperConfig, QuicTransportConfig, VarInt, presets};
use tokio::io::AsyncReadExt;

use args::{ClientConfig, Command, Config, Relay};

const ONLINE_WAIT: Duration = Duration::from_secs(10);
const CLOSE_WAIT: Duration = Duration::from_secs(3);

fn main() -> ExitCode {
    match args::parse(std::env::args().skip(1)) {
        Ok(Command::Version) => {
            println!("{}", args::version_line());
            ExitCode::SUCCESS
        }
        Ok(Command::Run(config)) => run(start(config)),
        Ok(Command::Client(config)) => client(config),
        Err(message) => {
            let _ = writeln!(std::io::stderr(), "hand-iroh: {message}\n{}", args::USAGE);
            ExitCode::from(2)
        }
    }
}

fn client(config: ClientConfig) -> ExitCode {
    let dir = &config.key_dir;
    let key = if config.new_key {
        key::load_or_create(dir)
    } else {
        key::load(dir)
    };
    let key = match key {
        Ok(key) => key,
        Err(e) => {
            let _ = writeln!(
                std::io::stderr(),
                "hand-iroh: cannot use the key in {}: {e}",
                dir.display()
            );
            return ExitCode::from(3);
        }
    };
    let _lock = match key::lock(dir) {
        Ok(lock) => lock,
        Err(e) => {
            let _ = writeln!(std::io::stderr(), "hand-iroh: {e}");
            return ExitCode::FAILURE;
        }
    };
    run(client::run(config, key))
}

fn run(work: impl Future<Output = Result<(), String>>) -> ExitCode {
    let runtime = match tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .enable_all()
        .build()
    {
        Ok(runtime) => runtime,
        Err(e) => {
            let _ = writeln!(
                std::io::stderr(),
                "hand-iroh: cannot start the runtime: {e}"
            );
            return ExitCode::FAILURE;
        }
    };
    let outcome = runtime.block_on(work);
    runtime.shutdown_background();
    match outcome {
        Ok(()) => ExitCode::SUCCESS,
        Err(message) => {
            let _ = writeln!(std::io::stderr(), "hand-iroh: {message}");
            ExitCode::FAILURE
        }
    }
}

async fn start(config: Config) -> Result<(), String> {
    let key = key::load_or_create(&config.key_dir)
        .map_err(|e| format!("cannot use the key in {}: {e}", config.key_dir.display()))?;
    let endpoint = builder(&config)
        .secret_key(key)
        .bind()
        .await
        .map_err(|e| format!("cannot open the endpoint: {e}"))?;
    let stop = stop_signals().map_err(|e| format!("cannot listen for signals: {e}"))?;
    let online = config.relay != Relay::None
        && tokio::time::timeout(ONLINE_WAIT, endpoint.online())
            .await
            .is_ok();
    announce(&endpoint, online);
    tokio::select! {
        () = forward::serve(endpoint.clone(), Arc::new(config), forward::Limits::default()) => {}
        () = stdin_closed() => {}
        () = stop => {}
    }
    let _ = tokio::time::timeout(CLOSE_WAIT, endpoint.close()).await;
    Ok(())
}

pub fn builder(config: &Config) -> iroh::endpoint::Builder {
    let transport = QuicTransportConfig::builder()
        .max_concurrent_bidi_streams(VarInt::from_u32(forward::STREAMS_PER_CONNECTION))
        .max_concurrent_uni_streams(VarInt::from_u32(0))
        .build();
    let builder = match &config.relay {
        Relay::Default => Endpoint::builder(presets::N0),
        Relay::Custom(url) => {
            Endpoint::builder(presets::Minimal).relay_mode(RelayMode::custom([url.clone()]))
        }
        Relay::None => offline(config.bind),
    };
    builder
        .alpns(vec![forward::ALPN.to_vec()])
        .transport_config(transport)
}

pub fn offline(bind: SocketAddr) -> iroh::endpoint::Builder {
    Endpoint::builder(presets::Minimal)
        .relay_mode(RelayMode::Disabled)
        .portmapper_config(PortmapperConfig::Disabled)
        .clear_ip_transports()
        .bind_addr(bind)
        .expect("a loopback address is a valid bind address")
}

fn announce(endpoint: &Endpoint, online: bool) {
    let relay = online
        .then(|| {
            endpoint
                .addr()
                .relay_urls()
                .next()
                .map(|url| url.to_string())
        })
        .flatten()
        .unwrap_or_else(|| "none".to_owned());
    println!("ready id={} relay={relay}", endpoint.id());
}

async fn stdin_closed() {
    let mut stdin = tokio::io::stdin();
    let mut sink = [0u8; 256];
    while matches!(stdin.read(&mut sink).await, Ok(n) if n > 0) {}
}

#[cfg(unix)]
fn stop_signals() -> std::io::Result<impl Future<Output = ()>> {
    use tokio::signal::unix::{SignalKind, signal};
    let mut term = signal(SignalKind::terminate())?;
    let mut interrupt = signal(SignalKind::interrupt())?;
    Ok(async move {
        tokio::select! {
            _ = term.recv() => {}
            _ = interrupt.recv() => {}
        }
    })
}

#[cfg(not(unix))]
fn stop_signals() -> std::io::Result<impl Future<Output = ()>> {
    Ok(async {
        let _ = tokio::signal::ctrl_c().await;
    })
}

#[cfg(test)]
mod tests {
    use std::net::Ipv4Addr;
    use std::path::PathBuf;

    use iroh::{RelayUrl, Watcher};

    use super::*;
    use crate::args::Secret;

    fn config(relay: Relay) -> Config {
        Config {
            board: "127.0.0.1:7778".parse().unwrap(),
            secret: Secret::parse("0123456789abcdef0123456789abcdef").unwrap(),
            key_dir: PathBuf::new(),
            relay,
            bind: SocketAddr::from((Ipv4Addr::LOCALHOST, 0)),
            max_connections: 4,
            idle: Duration::from_secs(60),
        }
    }

    #[tokio::test]
    async fn no_relay_means_no_relay_no_lookup_no_port_mapping_and_loopback_only() {
        let builder = builder(&config(Relay::None));
        assert!(format!("{builder:?}").contains("portmapper_config: Disabled"));
        let endpoint = builder.bind().await.unwrap();
        assert!(endpoint.address_lookup().unwrap().is_empty());
        assert!(endpoint.home_relay_status().get().is_empty());
        assert!(endpoint.addr().relay_urls().next().is_none());
        let sockets = endpoint.bound_sockets();
        assert!(!sockets.is_empty());
        assert!(
            sockets.iter().all(|addr| addr.ip().is_loopback()),
            "{sockets:?}"
        );
        endpoint.close().await;
    }

    #[tokio::test]
    async fn a_custom_relay_replaces_the_public_ones_and_publishes_to_no_dns() {
        let url: RelayUrl = "http://127.0.0.1:1".parse().unwrap();
        let endpoint = builder(&config(Relay::Custom(url.clone())))
            .bind()
            .await
            .unwrap();
        assert!(endpoint.address_lookup().unwrap().is_empty());
        let relays = endpoint.home_relay_status().get();
        assert!(relays.iter().all(|relay| relay.url() == &url), "{relays:?}");
        endpoint.close().await;
    }
}
