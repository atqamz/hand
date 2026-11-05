#[tauri::command]
fn about() -> String {
    hand_android_core::about()
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![about])
        .run(tauri::generate_context!())
        .expect("the Tauri app runs");
}
