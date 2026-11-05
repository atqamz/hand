fn main() {
    println!("cargo:rerun-if-changed=Cargo.lock");
    let lock = std::fs::read_to_string("Cargo.lock").expect("Cargo.lock is next to Cargo.toml");
    let version = lock
        .split("name = \"iroh\"\nversion = \"")
        .nth(1)
        .and_then(|rest| rest.split('"').next())
        .expect("Cargo.lock locks iroh");
    println!("cargo:rustc-env=IROH_VERSION={version}");
}
