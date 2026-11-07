use std::io;

use hand_android_core::{CipherError, KeyCipher};
use serde::{Deserialize, Serialize};
use tauri::plugin::mobile::PluginInvokeError;
use tauri::plugin::{Builder, PluginHandle, TauriPlugin};
use tauri::{Manager, Wry};

const PLUGIN: &str = "hand-keystore";

#[derive(Serialize, Deserialize)]
struct KeyBytes {
    data: Vec<u8>,
}

#[derive(Clone)]
pub struct KeystoreCipher {
    handle: PluginHandle<Wry>,
}

impl KeystoreCipher {
    fn call(&self, command: &str, data: &[u8]) -> Result<Vec<u8>, CipherError> {
        let payload = KeyBytes {
            data: data.to_vec(),
        };
        let response: KeyBytes = self
            .handle
            .run_mobile_plugin(command, payload)
            .map_err(plugin_error)?;
        Ok(response.data)
    }
}

impl KeyCipher for KeystoreCipher {
    fn seal(&self, plaintext: &[u8]) -> Result<Vec<u8>, CipherError> {
        self.call("seal", plaintext)
    }

    fn open(&self, sealed: &[u8]) -> Result<Vec<u8>, CipherError> {
        self.call("open", sealed)
    }

    fn probe(&self) -> Result<(), CipherError> {
        self.call("probe", &[]).map(|_| ())
    }
}

fn plugin_error(error: PluginInvokeError) -> CipherError {
    match &error {
        PluginInvokeError::InvokeRejected(response) => {
            CipherError::from_plugin(response.code.as_deref(), response.message.as_deref())
        }
        _ => CipherError::Io(io::Error::other(error.to_string())),
    }
}

pub fn plugin() -> TauriPlugin<Wry> {
    Builder::new(PLUGIN)
        .setup(|app, api| {
            let handle =
                api.register_android_plugin("io.github.atqamz.handcompanion", "KeystorePlugin")?;
            app.manage(KeystoreCipher { handle });
            Ok(())
        })
        .build()
}
