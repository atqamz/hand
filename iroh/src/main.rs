mod args;
mod forward;
mod key;
mod preface;
#[cfg(test)]
mod testutil;

use std::process::ExitCode;
use std::sync::Arc;
use std::time::Duration;

use iroh::RelayMode;
use iroh::endpoint::{Endpoint, PortmapperConfig, QuicTransportConfig, VarInt, presets};
use tokio::io::AsyncReadExt;

use args::{Command, Config, Relay};

const ONLINE_WAIT: Duration = Duration::from_secs(10);
const CLOSE_WAIT: Duration = Duration::from_secs(3);

fn main() -> ExitCode {
    match args::parse(std::env::args().skip(1)) {
        Ok(Command::Version) => {
            println!("{}", args::version_line());
            ExitCode::SUCCESS
        }
        Ok(Command::Run(config)) => run(config),
        Err(message) => {
            eprintln!("hand-iroh: {message}\n{}", args::USAGE);
            ExitCode::from(2)
        }
    }
}

fn run(config: Config) -> ExitCode {
    let runtime = match tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .enable_all()
        .build()
    {
        Ok(runtime) => runtime,
        Err(e) => {
            eprintln!("hand-iroh: cannot start the runtime: {e}");
            return ExitCode::FAILURE;
        }
    };
    let outcome = runtime.block_on(start(config));
    runtime.shutdown_background();
    match outcome {
        Ok(()) => ExitCode::SUCCESS,
        Err(message) => {
            eprintln!("hand-iroh: {message}");
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
    if config.relay != Relay::None {
        let _ = tokio::time::timeout(ONLINE_WAIT, endpoint.online()).await;
    }
    announce(&endpoint);
    tokio::select! {
        () = forward::serve(endpoint.clone(), Arc::new(config)) => {}
        () = stopped() => {}
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
            Endpoint::builder(presets::N0).relay_mode(RelayMode::custom([url.clone()]))
        }
        Relay::None => Endpoint::builder(presets::Minimal)
            .relay_mode(RelayMode::Disabled)
            .portmapper_config(PortmapperConfig::Disabled),
    };
    builder
        .alpns(vec![config.alpn.clone().into_bytes()])
        .transport_config(transport)
}

fn announce(endpoint: &Endpoint) {
    let relay = endpoint
        .addr()
        .relay_urls()
        .next()
        .map_or_else(|| "none".to_owned(), |url| url.to_string());
    println!("ready id={} relay={relay}", endpoint.id());
}

async fn stopped() {
    tokio::select! {
        () = stdin_closed() => {}
        _ = tokio::signal::ctrl_c() => {}
        () = terminated() => {}
    }
}

async fn stdin_closed() {
    let mut stdin = tokio::io::stdin();
    let mut sink = [0u8; 256];
    while matches!(stdin.read(&mut sink).await, Ok(n) if n > 0) {}
}

#[cfg(unix)]
async fn terminated() {
    use tokio::signal::unix::{SignalKind, signal};
    match signal(SignalKind::terminate()) {
        Ok(mut term) => {
            term.recv().await;
        }
        Err(_) => std::future::pending().await,
    }
}

#[cfg(not(unix))]
async fn terminated() {
    std::future::pending::<()>().await
}
