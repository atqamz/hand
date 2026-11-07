mod mobile;

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};

use hand_android_core::{
    Companion, CompanionConfig, ConfirmError, OfferError, OpenError, PAIRING_PENDING,
};
use tauri::Manager;

struct AppState {
    companion: Arc<Companion>,
    lost: AtomicU32,
    launch: AtomicBool,
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
    state.launch.store(false, Ordering::Relaxed);
    match state.companion.offer_link(&link) {
        Ok(offer) => Ok(Some(OfferView {
            laptop: offer.laptop,
            notice: None,
        })),
        Err(OfferError::Pending) => Ok(state.companion.pending_offer().map(|offer| OfferView {
            laptop: offer.laptop,
            notice: Some(PAIRING_PENDING),
        })),
        Err(error) => Err(Failure::rejected(error)),
    }
}

#[tauri::command]
fn offer_link(state: tauri::State<'_, AppState>, text: String) -> Result<OfferView, Failure> {
    let offer = state
        .companion
        .offer_link(&text)
        .map_err(Failure::rejected)?;
    Ok(OfferView {
        laptop: offer.laptop,
        notice: None,
    })
}

#[tauri::command]
async fn open_board(state: tauri::State<'_, AppState>) -> Result<Option<String>, Failure> {
    if !state.launch.swap(false, Ordering::Relaxed) {
        return Ok(None);
    }
    state
        .companion
        .open_board()
        .await
        .map_err(|error| failure(error, &state.lost))
}

#[tauri::command]
fn cancel_pairing(state: tauri::State<'_, AppState>) {
    state.companion.cancel_pairing();
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
        .companion
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
        .companion
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

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(mobile::init())
        .on_page_load(|webview, payload| {
            if let Some(state) = webview.app_handle().try_state::<AppState>() {
                let secure = secure_for(payload.url(), state.companion.port());
                apply_secure(webview.app_handle(), secure);
            }
        })
        .setup(|app| {
            let data_dir = app.path().app_data_dir()?;
            let companion =
                tauri::async_runtime::block_on(Companion::start(CompanionConfig { data_dir }))?;
            let signals = companion.take_signals();
            app.manage(AppState {
                companion: Arc::new(companion),
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
            open_board,
            cancel_pairing,
            confirm_pairing,
            forget_laptop
        ])
        .build(tauri::generate_context!())
        .expect("the Tauri app builds")
        .run(|app, event| {
            if let tauri::RunEvent::Resumed = event {
                let state = app.state::<AppState>();
                let companion = state.companion.clone();
                tauri::async_runtime::spawn(async move {
                    companion.reconnect_on_resume().await;
                });
            }
        });
}

#[cfg(test)]
mod tests {
    use super::*;

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
