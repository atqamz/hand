mod mobile;
mod scanner;

#[cfg(target_os = "android")]
mod host;

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};

use hand_android_core::{
    CAMERA_FAILED, Companion, ConfirmError, OfferError, OpenError, OpenLinkError, PAIRING_PENDING,
    ScanError, ScanStatus, is_open_link, may_load, proxy_origin, scan_reply,
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
#[serde(untagged)]
enum Intake {
    Offer(OfferView),
    Open { url: String },
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

fn not_cleared() -> Failure {
    Failure {
        message: "Can't clear the saved login. Try again.".to_owned(),
        retry: false,
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
) -> Result<Option<Intake>, Failure> {
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
    if is_open_link(&link) {
        let result = match companion.open_link(&link).await {
            Ok(url) => Ok(Some(Intake::Open { url })),
            Err(OpenLinkError::Pending) => Ok(pending_offer(&companion)),
            Err(OpenLinkError::Rejected) => match companion.open_board().await {
                Ok(Some(url)) => Ok(Some(Intake::Open { url })),
                _ => Err(Failure {
                    message: OpenLinkError::Rejected.message().to_owned(),
                    retry: false,
                }),
            },
            Err(OpenLinkError::Failed(ConfirmError::Open(
                OpenError::Unreachable | OpenError::Lost,
            ))) => Err(unreachable_failure(&companion)),
            Err(error) => Err(Failure {
                message: error.message().to_owned(),
                retry: false,
            }),
        };
        settle_launch(&state.launch, &result);
        return result;
    }
    state.launch.store(false, Ordering::Relaxed);
    match companion.offer_link(&link) {
        Ok(offer) => Ok(Some(Intake::Offer(OfferView {
            laptop: offer.laptop,
            notice: None,
        }))),
        Err(OfferError::Pending) => Ok(pending_offer(&companion)),
        Err(error) => Err(Failure::rejected(error)),
    }
}

fn unreachable_failure(companion: &Companion) -> Failure {
    Failure {
        message: companion.cant_reach(),
        retry: true,
    }
}

fn settle_launch<T, E>(launch: &AtomicBool, result: &Result<Option<T>, E>) {
    if matches!(result, Ok(Some(_))) {
        launch.store(false, Ordering::Relaxed);
    }
}

fn pending_offer(companion: &Companion) -> Option<Intake> {
    companion.pending_offer().map(|offer| {
        Intake::Offer(OfferView {
            laptop: offer.laptop,
            notice: Some(PAIRING_PENDING),
        })
    })
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
    if !state.launch.load(Ordering::Relaxed) {
        return Ok(None);
    }
    let companion = state.companion()?;
    let result = companion.open_board().await;
    settle_launch(&state.launch, &result);
    result.map_err(|error| match error {
        ConfirmError::Open(OpenError::Unreachable | OpenError::Lost) => {
            unreachable_failure(&companion)
        }
        error => Failure {
            message: error.message().to_owned(),
            retry: error.retry(),
        },
    })
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
    forget_alerts().map_err(|_| not_cleared())?;
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
    let clear_failed = AtomicBool::new(false);
    let clear = || {
        let cleared = window
            .clear_all_browsing_data()
            .map_err(|error| std::io::Error::other(error.to_string()))
            .and_then(|()| forget_alerts());
        clear_failed.store(cleared.is_err(), Ordering::Relaxed);
        cleared
    };
    let url = state.companion()?.confirm(clear).await.map_err(|error| {
        if clear_failed.load(Ordering::Relaxed) {
            not_cleared()
        } else {
            failure(error, &state.lost)
        }
    })?;
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

fn forget_alerts() -> std::io::Result<()> {
    #[cfg(target_os = "android")]
    return host::forget_alerts();
    #[cfg(not(target_os = "android"))]
    Ok(())
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

fn ensure_alerts(app: &tauri::AppHandle) {
    #[cfg(target_os = "android")]
    if let Some(bridge) = app.try_state::<mobile::Bridge>() {
        let bridge = bridge.inner().clone();
        tauri::async_runtime::spawn(async move {
            bridge.ensure_alerts().await;
        });
    }
    #[cfg(not(target_os = "android"))]
    {
        let _ = app;
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
    builder
        .on_page_load(|webview, payload| {
            if let Some(state) = webview.app_handle().try_state::<AppState>()
                && let Ok(companion) = &state.companion
            {
                let secure = secure_for(payload.url(), companion.port());
                apply_secure(webview.app_handle(), secure);
                if matches!(payload.event(), tauri::webview::PageLoadEvent::Finished)
                    && payload.url().host_str() == Some("localhost")
                {
                    ensure_alerts(webview.app_handle());
                }
            }
        })
        .setup(|app| {
            #[cfg(target_os = "android")]
            let companion = host::companion();
            #[cfg(not(target_os = "android"))]
            let companion = {
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
    fn the_intake_reply_names_the_board_url_or_the_offer() {
        let open = Intake::Open {
            url: "http://localhost:50123/f0123456789ab/task/t3#r5".to_owned(),
        };
        assert_eq!(
            serde_json::to_string(&open).unwrap(),
            r#"{"url":"http://localhost:50123/f0123456789ab/task/t3#r5"}"#
        );
        let offer = Intake::Offer(OfferView {
            laptop: "4d92…6ed6".to_owned(),
            notice: Some(PAIRING_PENDING),
        });
        let json: serde_json::Value = serde_json::to_value(&offer).unwrap();
        assert_eq!(json["laptop"], "4d92…6ed6");
        assert_eq!(json["notice"], PAIRING_PENDING);
        assert!(json.get("url").is_none());
    }

    #[test]
    fn the_launch_gate_closes_only_when_the_board_opens() {
        let launch = AtomicBool::new(true);
        let failed: Result<Option<String>, ConfirmError> =
            Err(ConfirmError::Open(OpenError::Unreachable));
        settle_launch(&launch, &failed);
        assert!(launch.load(Ordering::Relaxed));
        settle_launch(&launch, &Ok::<Option<String>, ConfirmError>(None));
        assert!(launch.load(Ordering::Relaxed));
        settle_launch(
            &launch,
            &Ok::<Option<String>, ConfirmError>(Some("http://localhost:1/".to_owned())),
        );
        assert!(!launch.load(Ordering::Relaxed));
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
