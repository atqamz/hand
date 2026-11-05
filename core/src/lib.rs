pub fn about() -> String {
    format!("core {}", env!("CARGO_PKG_VERSION"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn about_names_the_core_and_its_crate_version() {
        let text = about();
        let (name, version) = text.split_once(' ').unwrap();
        assert_eq!(name, "core");
        assert_eq!(version, env!("CARGO_PKG_VERSION"));
        assert_eq!(version.split('.').count(), 3);
    }
}
