#[cfg(target_os = "android")]
use std::collections::HashMap;

#[cfg(target_os = "android")]
use tauri::Manager;
use tauri::Wry;

#[derive(serde::Deserialize)]
pub struct PluginConfig {
    #[serde(rename = "localUrl")]
    #[allow(dead_code)]
    pub local_url: String,
}

#[cfg(target_os = "android")]
#[derive(Clone)]
pub struct Bridge(tauri::plugin::PluginHandle<tauri::Wry>);

#[cfg(target_os = "android")]
impl Bridge {
    pub async fn take_link(&self) -> Option<String> {
        self.0
            .run_mobile_plugin_async::<Option<String>>("takeLink", ())
            .await
            .ok()
            .flatten()
    }

    pub async fn set_secure(&self, secure: bool) {
        let mut args = HashMap::new();
        args.insert("secure", secure);
        let _ = self
            .0
            .run_mobile_plugin_async::<()>("setSecure", args)
            .await;
    }

    pub async fn show_recover_actions(&self) {
        let _ = self
            .0
            .run_mobile_plugin_async::<()>("showRecoverActions", ())
            .await;
    }
}

pub fn init() -> tauri::plugin::TauriPlugin<Wry, PluginConfig> {
    tauri::plugin::Builder::<Wry, PluginConfig>::new("companion")
        .setup(|app, api| {
            #[cfg(target_os = "android")]
            {
                let handle = api
                    .register_android_plugin("io.github.atqamz.handcompanion", "CompanionPlugin")?;
                app.manage(Bridge(handle));
            }
            #[cfg(not(target_os = "android"))]
            {
                let _ = (app, api);
            }
            Ok(())
        })
        .build()
}
