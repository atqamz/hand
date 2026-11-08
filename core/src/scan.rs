use crate::link::CAMERA_REFUSED;

pub const CAMERA_FAILED: &str = "The camera could not start. Try again, or paste the link.";
pub const SCAN_OPEN: &str = "scan_open";
pub const SCAN_BUSY: &str = "A scan is already open. Close it and try again.";
pub const SCAN_UNKNOWN: &str =
    "The scanner returned an unknown status. Try again, or paste the link.";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ScanStatus {
    Code,
    Permission,
    Failed,
    Cancelled,
}

impl ScanStatus {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "code" => Some(Self::Code),
            "permission" => Some(Self::Permission),
            "failed" => Some(Self::Failed),
            "cancelled" => Some(Self::Cancelled),
            _ => None,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ScanError {
    Permission,
    Failed,
    Busy,
    Unknown,
}

impl ScanError {
    pub fn message(self) -> &'static str {
        match self {
            Self::Permission => CAMERA_REFUSED,
            Self::Failed => CAMERA_FAILED,
            Self::Busy => SCAN_BUSY,
            Self::Unknown => SCAN_UNKNOWN,
        }
    }

    pub fn retry(self) -> bool {
        matches!(
            self,
            Self::Permission | Self::Failed | Self::Busy | Self::Unknown
        )
    }
}

pub fn scan_reply(
    status: Option<ScanStatus>,
    text: Option<String>,
) -> Result<Option<String>, ScanError> {
    match status {
        Some(ScanStatus::Code) => match text {
            Some(text) => Ok(Some(text)),
            None => Err(ScanError::Failed),
        },
        Some(ScanStatus::Permission) => Err(ScanError::Permission),
        Some(ScanStatus::Failed) => Err(ScanError::Failed),
        Some(ScanStatus::Cancelled) => Ok(None),
        None => Err(ScanError::Unknown),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::link::LINK_REJECTED;

    const ID: &str = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6";

    fn link() -> String {
        format!("handboard://c#v=1&e={ID}&f=f0123456789ab&c=K7QM3XRD2P")
    }

    #[test]
    fn a_decoded_code_goes_to_the_paste_path_unchanged() {
        let text = format!("  {}\n", link());
        assert_eq!(
            scan_reply(Some(ScanStatus::Code), Some(text.clone())).unwrap(),
            Some(text)
        );
        assert_eq!(
            scan_reply(Some(ScanStatus::Code), None),
            Err(ScanError::Failed)
        );
    }

    #[test]
    fn a_refused_permission_is_retriable_and_says_the_camera_is_needed() {
        let error = scan_reply(Some(ScanStatus::Permission), None).unwrap_err();
        assert_eq!(error.message(), CAMERA_REFUSED);
        assert!(error.retry());
    }

    #[test]
    fn a_camera_failure_is_retriable() {
        let error = scan_reply(Some(ScanStatus::Failed), None).unwrap_err();
        assert_eq!(error.message(), CAMERA_FAILED);
        assert!(error.retry());
    }

    #[test]
    fn a_scan_that_is_already_open_says_so() {
        let error = ScanError::Busy;
        assert_eq!(error.message(), SCAN_BUSY);
        assert_ne!(error.message(), CAMERA_FAILED);
        assert!(error.retry());
    }

    #[test]
    fn cancelling_and_unknown_replies_do_not_leak_a_code() {
        assert_eq!(scan_reply(Some(ScanStatus::Cancelled), None).unwrap(), None);
        let error = scan_reply(None, Some(link())).unwrap_err();
        assert_eq!(error, ScanError::Unknown);
        assert_eq!(error.message(), SCAN_UNKNOWN);
        assert_ne!(error.message(), LINK_REJECTED);
        assert!(!error.message().contains("K7QM3XRD2P"));
    }

    #[test]
    fn the_plugin_reports_an_open_scan_with_the_code_the_shell_matches() {
        const KOTLIN: &str = include_str!(
            "../../src-tauri/gen/android/app/src/main/java/io/github/atqamz/handcompanion/ScannerPlugin.kt"
        );
        assert!(KOTLIN.contains(&format!("private const val SCAN_OPEN = \"{SCAN_OPEN}\"")));
        assert!(KOTLIN.contains("invoke.reject(\"A scan is already open.\", SCAN_OPEN)"));
    }

    #[test]
    fn the_status_names_are_the_plugin_protocol() {
        assert_eq!(ScanStatus::parse("code"), Some(ScanStatus::Code));
        assert_eq!(
            ScanStatus::parse("permission"),
            Some(ScanStatus::Permission)
        );
        assert_eq!(ScanStatus::parse("failed"), Some(ScanStatus::Failed));
        assert_eq!(ScanStatus::parse("cancelled"), Some(ScanStatus::Cancelled));
        assert_eq!(ScanStatus::parse("granted"), None);
    }
}
