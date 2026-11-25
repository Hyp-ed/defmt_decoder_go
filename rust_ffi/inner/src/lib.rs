use std::ffi::IntoStringError;
use std::str::FromStr;
use std::{ffi::CString, path::PathBuf, sync::OnceLock};

use defmt_decoder::Table;

struct DefmtCtx {
    table: Table,
}

static DEFMT_CTX_ARR: OnceLock<Vec<DefmtCtx>> = OnceLock::new();

#[repr(i32)]
enum ErrCodes {
    StringParseErr = -1,
}

pub fn init_inner(paths: &[CString], result: &mut [i32]) {
    for (idx, path) in paths.iter().enumerate() {
        let path = match path.clone().into_string() {
            Ok(res) => res,
            Err(_) => {
                result[idx] = ErrCodes::StringParseErr as i32;
                continue;
            }
        };

        let path = PathBuf::from_str(&path);
    }
}
