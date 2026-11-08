mod mobile;
mod scanner;

#[cfg(target_os = "android")]
mod keystore;

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};

#[cfg(target_os = "android")]
use hand_android_core::CompanionConfig;
use hand_android_core::{
    CAMERA_FAILED, Companion, ConfirmError, OfferError, OpenError, PAIRING_PENDING, ScanError,
    ScanStatus, may_load, proxy_origin, scan_reply,
};
use tauri::Manager;
use tauri_plugin_opener::OpenerExt;

struct AppState {
    companion: Result<Arc<Companion>, String>,
    lost: AtomicU32,
    launch: AtomicBool,
}

impl AppState {
    fn companion(&self) -> Result<Arc<Companion>, Failure> {
        self.companion.clone().map_err(|message| Failure {
            message,
            retry: false,
        })
    }
}

#[derive(serde::Serialize)]
struct OfferView {
    laptop: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    notice: Option<&'static str>,
}

#[derive(serde::Serialize)]
struct Failure {
    message: String,
    retry: bool,
}

impl Failure {
    fn rejected(error: OfferError) -> Self {
        Self {
            message: error.message().to_owned(),
            retry: false,
        }
    }
}

fn failure(error: ConfirmError, lost: &AtomicU32) -> Failure {
    if matches!(error, ConfirmError::Open(OpenError::Lost)) {
        let count = lost.fetch_add(1, Ordering::Relaxed) + 1;
        if count >= 3 {
            lost.store(0, Ordering::Relaxed);
            return Failure {
                message: "Can't reach your laptop.".to_owned(),
                retry: false,
            };
        }
        return Failure {
            message: "Reconnecting…".to_owned(),
            retry: true,
        };
    }
    lost.store(0, Ordering::Relaxed);
    Failure {
        message: error.message().to_owned(),
        retry: error.retry(),
    }
}

#[tauri::command]
async fn intake_intent(
    app: tauri::AppHandle,
    state: tauri::State<'_, AppState>,
) -> Result<Option<OfferView>, Failure> {
    #[cfg(target_os = "android")]
    let link = match app.try_state::<mobile::Bridge>() {
        Some(bridge) => bridge.take_link().await,
        None => None,
    };
    #[cfg(not(target_os = "android"))]
    let link = {
        let _ = &app;
        None::<String>
    };
    let Some(link) = link else {
        return Ok(None);
    };
    let companion = state.companion()?;
    state.launch.store(false, Ordering::Relaxed);
    match companion.offer_link(&link) {
        Ok(offer) => Ok(Some(OfferView {
            laptop: offer.laptop,
            notice: None,
        })),
        Err(OfferError::Pending) => Ok(companion.pending_offer().map(|offer| OfferView {
            laptop: offer.laptop,
            notice: Some(PAIRING_PENDING),
        })),
        Err(error) => Err(Failure::rejected(error)),
    }
}

#[tauri::command]
fn offer_link(state: tauri::State<'_, AppState>, text: String) -> Result<OfferView, Failure> {
    let offer = state
        .companion()?
        .offer_link(&text)
        .map_err(Failure::rejected)?;
    Ok(OfferView {
        laptop: offer.laptop,
        notice: None,
    })
}

#[tauri::command]
async fn scan_code(app: tauri::AppHandle) -> Result<Option<String>, Failure> {
    #[cfg(target_os = "android")]
    {
        let bridge = app
            .try_state::<scanner::ScannerBridge>()
            .ok_or_else(camera_failure)?;
        let reply = bridge
            .scan(scanner::ScanArgs {
                permission_message: hand_android_core::CAMERA_REFUSED.to_owned(),
                failed_message: CAMERA_FAILED.to_owned(),
            })
            .await
            .map_err(scan_failure)?;
        scan_reply(ScanStatus::parse(reply.status.as_str()), reply.text).map_err(scan_failure)
    }
    #[cfg(not(target_os = "android"))]
    {
        let _ = app;
        Err(camera_failure())
    }
}

fn scan_failure(error: ScanError) -> Failure {
    Failure {
        message: error.message().to_owned(),
        retry: error.retry(),
    }
}

fn camera_failure() -> Failure {
    Failure {
        message: CAMERA_FAILED.to_owned(),
        retry: true,
    }
}

#[tauri::command]
async fn open_board(state: tauri::State<'_, AppState>) -> Result<Option<String>, Failure> {
    if !state.launch.swap(false, Ordering::Relaxed) {
        return Ok(None);
    }
    state
        .companion()?
        .open_board()
        .await
        .map_err(|error| failure(error, &state.lost))
}

#[tauri::command]
fn cancel_pairing(state: tauri::State<'_, AppState>) -> Result<(), Failure> {
    state.companion()?.cancel_pairing();
    Ok(())
}

#[tauri::command]
async fn forget_laptop(
    app: tauri::AppHandle,
    state: tauri::State<'_, AppState>,
) -> Result<(), Failure> {
    let window = app.get_webview_window("main").ok_or_else(|| Failure {
        message: "Can't reach your laptop.".to_owned(),
        retry: false,
    })?;
    window.clear_all_browsing_data().map_err(|error| Failure {
        message: error.to_string(),
        retry: false,
    })?;
    state
        .companion()?
        .forget_laptop()
        .await
        .map_err(|error| Failure {
            message: error.to_string(),
            retry: false,
        })
}

#[tauri::command]
async fn confirm_pairing(
    app: tauri::AppHandle,
    state: tauri::State<'_, AppState>,
) -> Result<(), Failure> {
    let window = app.get_webview_window("main").ok_or_else(|| Failure {
        message: "Can't reach your laptop.".to_owned(),
        retry: false,
    })?;
    let clear = || {
        window
            .clear_all_browsing_data()
            .map_err(|error| std::io::Error::other(error.to_string()))
    };
    let url = state
        .companion()?
        .confirm(clear)
        .await
        .map_err(|error| failure(error, &state.lost))?;
    state.lost.store(0, Ordering::Relaxed);
    let url = tauri::Url::parse(&url).map_err(|_| Failure {
        message: "Can't reach your laptop.".to_owned(),
        retry: false,
    })?;
    window.navigate(url).map_err(|error| Failure {
        message: error.to_string(),
        retry: false,
    })
}

fn secure_for(url: &tauri::Url, port: u16) -> bool {
    if url.scheme() == "tauri" || url.host_str() == Some("tauri.localhost") {
        return true;
    }
    url.host_str() == Some("localhost")
        && url.port() == Some(port)
        && url.path().trim_end_matches('/').ends_with("/pair")
}

fn apply_secure(app: &tauri::AppHandle, secure: bool) {
    #[cfg(target_os = "android")]
    if let Some(bridge) = app.try_state::<mobile::Bridge>() {
        let bridge = bridge.inner().clone();
        tauri::async_runtime::spawn(async move {
            bridge.set_secure(secure).await;
        });
    }
    #[cfg(not(target_os = "android"))]
    {
        let _ = (app, secure);
    }
}

fn bundled_origin(config: &tauri::Config) -> tauri::Url {
    bundled_origin_for(config, cfg!(dev))
}

fn bundled_origin_for(config: &tauri::Config, dev: bool) -> tauri::Url {
    let configured = if dev {
        config.build.dev_url.clone()
    } else if let Some(tauri::utils::config::FrontendDist::Url(url)) =
        config.build.frontend_dist.as_ref()
    {
        Some(url.clone())
    } else {
        None
    };
    configured.unwrap_or_else(|| {
        let scheme = if config
            .app
            .windows
            .iter()
            .find(|window| window.label == "main")
            .is_some_and(|window| window.use_https_scheme)
        {
            "https"
        } else {
            "http"
        };
        tauri::Url::parse(&format!("{scheme}://tauri.localhost"))
            .expect("the bundled origin parses")
    })
}

fn origin_guard(bundled: tauri::Url) -> tauri::plugin::TauriPlugin<tauri::Wry> {
    tauri::plugin::Builder::new("board-origin")
        .js_init_script(include_str!("external_links.js"))
        .on_navigation(move |webview, url| {
            let proxy = webview.try_state::<AppState>().and_then(|state| {
                state
                    .companion
                    .as_ref()
                    .ok()
                    .map(|companion| proxy_origin(companion.port()))
            });
            if may_load(url, &bundled, proxy.as_ref()) {
                return true;
            }
            let _ = webview.opener().open_url(url.as_str(), None::<&str>);
            false
        })
        .build()
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let context = tauri::generate_context!();
    let bundled = bundled_origin(context.config());
    let builder = tauri::Builder::default()
        .plugin(mobile::init())
        .plugin(scanner::init())
        .plugin(tauri_plugin_opener::Builder::new().build())
        .plugin(origin_guard(bundled));
    #[cfg(target_os = "android")]
    let builder = builder.plugin(keystore::plugin());
    builder
        .on_page_load(|webview, payload| {
            if let Some(state) = webview.app_handle().try_state::<AppState>()
                && let Ok(companion) = &state.companion
            {
                let secure = secure_for(payload.url(), companion.port());
                apply_secure(webview.app_handle(), secure);
            }
        })
        .setup(|app| {
            let data_dir = app.path().app_data_dir()?;
            #[cfg(target_os = "android")]
            let companion = {
                let cipher = Arc::new(app.state::<keystore::KeystoreCipher>().inner().clone());
                tauri::async_runtime::block_on(Companion::start(CompanionConfig {
                    data_dir,
                    cipher,
                }))
                .map(Arc::new)
                .map_err(|error| format!("the phone's key could not start the companion: {error}"))
            };
            #[cfg(not(target_os = "android"))]
            let companion = {
                let _ = data_dir;
                Err::<Arc<Companion>, String>(
                    "the Android Keystore is only available on a phone".to_owned(),
                )
            };
            let signals = companion
                .as_ref()
                .ok()
                .and_then(|companion| companion.take_signals());
            app.manage(AppState {
                companion,
                lost: AtomicU32::new(0),
                launch: AtomicBool::new(true),
            });
            let handle = app.handle().clone();
            tauri::async_runtime::spawn(async move {
                let Some(mut signals) = signals else {
                    return;
                };
                #[cfg(not(target_os = "android"))]
                let _ = &handle;
                while signals.recv().await.is_some() {
                    #[cfg(target_os = "android")]
                    if let Some(bridge) = handle.try_state::<mobile::Bridge>() {
                        bridge.show_recover_actions().await;
                    }
                }
            });
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            intake_intent,
            offer_link,
            scan_code,
            open_board,
            cancel_pairing,
            confirm_pairing,
            forget_laptop
        ])
        .build(context)
        .expect("the Tauri app builds")
        .run(|app, event| {
            if let tauri::RunEvent::Resumed = event {
                let state = app.state::<AppState>();
                if let Ok(companion) = &state.companion {
                    let companion = companion.clone();
                    tauri::async_runtime::spawn(async move {
                        companion.reconnect_on_resume().await;
                    });
                }
            }
        });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_bundled_origin_follows_the_app_config() {
        let config = tauri::Config::default();
        assert_eq!(
            bundled_origin_for(&config, true).as_str(),
            "http://tauri.localhost/"
        );
        assert_eq!(
            bundled_origin_for(&config, false).as_str(),
            "http://tauri.localhost/"
        );

        let mut https = tauri::Config::default();
        https.app.windows = vec![tauri::utils::config::WindowConfig {
            use_https_scheme: true,
            ..Default::default()
        }];
        assert_eq!(
            bundled_origin_for(&https, false).as_str(),
            "https://tauri.localhost/"
        );
    }

    #[test]
    fn the_bundled_origin_follows_a_dev_or_dist_url() {
        let mut config = tauri::Config::default();
        config.build.dev_url = Some(tauri::Url::parse("http://localhost:1420/").unwrap());
        config.build.frontend_dist = Some(tauri::utils::config::FrontendDist::Url(
            tauri::Url::parse("https://app.example.test/").unwrap(),
        ));
        assert_eq!(
            bundled_origin_for(&config, true).as_str(),
            "http://localhost:1420/"
        );
        assert_eq!(
            bundled_origin_for(&config, false).as_str(),
            "https://app.example.test/"
        );
    }

    #[test]
    fn flag_secure_covers_the_pairing_screen_and_the_pair_page() {
        let local = tauri::Url::parse("http://tauri.localhost/").unwrap();
        let pair =
            tauri::Url::parse("http://localhost:50123/f0123456789ab/pair#K7QM3XRD2P").unwrap();
        let fleet = tauri::Url::parse("http://localhost:50123/f0123456789ab/").unwrap();
        let other = tauri::Url::parse("https://example.test/pair").unwrap();
        assert!(secure_for(&local, 50123));
        assert!(secure_for(&pair, 50123));
        assert!(!secure_for(&fleet, 50123));
        assert!(!secure_for(&other, 50123));
    }
}
