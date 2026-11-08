#[cfg(target_os = "android")]
use hand_android_core::{SCAN_OPEN, ScanError};
#[cfg(target_os = "android")]
use tauri::Manager;
use tauri::Wry;
#[cfg(target_os = "android")]
use tauri::plugin::mobile::{ErrorResponse, PluginInvokeError};

#[cfg(target_os = "android")]
#[derive(Clone)]
pub struct ScannerBridge(tauri::plugin::PluginHandle<Wry>);

#[cfg(target_os = "android")]
impl ScannerBridge {
    pub async fn scan(&self, args: ScanArgs) -> Result<ScanReply, ScanError> {
        self.0
            .run_mobile_plugin_async::<ScanReply>("scanCode", args)
            .await
            .map_err(|error| match error {
                PluginInvokeError::InvokeRejected(ErrorResponse { code, .. })
                    if code.as_deref() == Some(SCAN_OPEN) =>
                {
                    ScanError::Busy
                }
                _ => ScanError::Failed,
            })
    }
}

#[derive(serde::Deserialize)]
pub struct ScanReply {
    pub status: String,
    #[serde(default)]
    pub text: Option<String>,
}

#[cfg(target_os = "android")]
#[derive(Clone, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScanArgs {
    pub permission_message: String,
    pub failed_message: String,
}

pub fn init() -> tauri::plugin::TauriPlugin<Wry> {
    tauri::plugin::Builder::<Wry>::new("scanner")
        .setup(|app, api| {
            #[cfg(target_os = "android")]
            {
                let handle =
                    api.register_android_plugin("io.github.atqamz.handcompanion", "ScannerPlugin")?;
                app.manage(ScannerBridge(handle));
            }
            #[cfg(not(target_os = "android"))]
            {
                let _ = (app, api);
            }
            Ok(())
        })
        .build()
}
