use std::sync::Arc;

use hand_android_core::{Companion, CompanionConfig, parse_target};
use tauri::Manager;

struct AppState {
    companion: Arc<Companion>,
}

#[tauri::command]
fn proxy_url(state: tauri::State<AppState>) -> String {
    state.companion.url("/")
}

#[tauri::command]
fn phone_endpoint_id(state: tauri::State<AppState>) -> String {
    state.companion.endpoint_id().to_string()
}

#[tauri::command]
async fn open_board(
    state: tauri::State<'_, AppState>,
    endpoint_id: String,
    relay: Option<String>,
    fleet: Option<String>,
) -> Result<String, String> {
    let target = parse_target(&endpoint_id, relay.as_deref())?;
    let path = match fleet.map(|fleet| fleet.trim().to_owned()) {
        Some(fleet) if !fleet.is_empty() => {
            if !valid_fleet(&fleet) {
                return Err(
                    "the fleet ID must be f and 12 lowercase hexadecimal characters".to_owned(),
                );
            }
            format!("/{fleet}/pair")
        }
        _ => "/".to_owned(),
    };
    state.companion.set_target(Some(target)).await;
    Ok(state.companion.url(&path))
}

fn valid_fleet(fleet: &str) -> bool {
    fleet.len() == 13
        && fleet.starts_with('f')
        && fleet[1..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .setup(|app| {
            let data_dir = app.path().app_data_dir()?;
            let companion =
                tauri::async_runtime::block_on(Companion::start(CompanionConfig { data_dir }))?;
            app.manage(AppState {
                companion: Arc::new(companion),
            });
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            proxy_url,
            phone_endpoint_id,
            open_board
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
