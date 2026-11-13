use iroh::endpoint::{ConnectionError, ReadError, TransportErrorCode, WriteError};

pub const FULL: u64 = 1;
pub const IDLE: u64 = 2;
pub const BOARD_UNREACHABLE: u64 = 3;
pub const STREAM_IDLE: u64 = 4;
pub const BOARD_ERROR: u64 = 5;

pub fn connection_code(error: &ConnectionError) -> Option<u64> {
    match error {
        ConnectionError::ApplicationClosed(close) => Some(close.error_code.into_inner()),
        _ => None,
    }
}

pub fn read_code(error: &ReadError) -> Option<u64> {
    match error {
        ReadError::Reset(code) => Some(code.into_inner()),
        _ => None,
    }
}

pub fn write_code(error: &WriteError) -> Option<u64> {
    match error {
        WriteError::Stopped(code) => Some(code.into_inner()),
        _ => None,
    }
}

pub fn is_refusal(error: &ConnectionError) -> bool {
    match error {
        ConnectionError::ApplicationClosed(close) => close.error_code.into_inner() == FULL,
        ConnectionError::ConnectionClosed(close) => {
            close.error_code == TransportErrorCode::CONNECTION_REFUSED
        }
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use iroh::endpoint::{ApplicationClose, VarInt};

    use super::*;

    #[test]
    fn the_codes_are_the_contracts_numbers() {
        assert_eq!(
            [FULL, IDLE, BOARD_UNREACHABLE, STREAM_IDLE, BOARD_ERROR],
            [1, 2, 3, 4, 5]
        );
    }

    #[test]
    fn connection_codes_are_read_from_an_application_close() {
        let close = ConnectionError::ApplicationClosed(ApplicationClose {
            error_code: VarInt::from_u32(2),
            reason: Default::default(),
        });
        assert_eq!(connection_code(&close), Some(IDLE));
        assert!(!is_refusal(&close));
    }

    #[test]
    fn a_close_with_code_one_is_a_full_laptop() {
        let full = ConnectionError::ApplicationClosed(ApplicationClose {
            error_code: VarInt::from_u32(1),
            reason: Default::default(),
        });
        assert!(is_refusal(&full));
    }

    #[test]
    fn reset_and_stopped_codes_are_read_from_the_stream_errors() {
        assert_eq!(
            read_code(&ReadError::Reset(VarInt::from_u32(4))),
            Some(STREAM_IDLE)
        );
        assert_eq!(
            write_code(&WriteError::Stopped(VarInt::from_u32(5))),
            Some(BOARD_ERROR)
        );
        assert_eq!(read_code(&ReadError::ClosedStream), None);
    }
}
