use std::fs;

fn pin(manifest: &str) -> String {
    let path = format!("{}/{manifest}", env!("CARGO_MANIFEST_DIR"));
    let text = fs::read_to_string(&path).unwrap();
    text.lines()
        .find(|line| line.starts_with("iroh = "))
        .unwrap_or_else(|| panic!("{path} pins no iroh"))
        .to_owned()
}

#[test]
fn the_sidecar_and_the_android_core_pin_the_same_iroh() {
    assert_eq!(pin("Cargo.toml"), pin("../android/core/Cargo.toml"));
    assert!(pin("Cargo.toml").contains("\"="), "the pin is exact");
}
