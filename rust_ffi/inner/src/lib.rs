use std::{ffi::CString, sync::OnceLock};

use defmt_decoder::Table;

#[derive(Clone, Debug)]
struct DefmtCtx {
    table: Table,
}

static DEFMT_CTX_ARR: OnceLock<Vec<Option<DefmtCtx>>> = OnceLock::new();

#[repr(i32)]
enum ErrCodes {
    StringEncodingErr = -1,
    ReadFileErr = -2,
    TableParsingErr = -3,
    NoDefmtSectionFound = -4,
}

pub fn init_inner(paths: &[CString], result: &mut [i32]) {
    let mut result_vec: Vec<Option<DefmtCtx>> = vec![None; paths.len()];

    for (idx, path) in paths.iter().enumerate() {
        let path = match path.clone().into_string() {
            Ok(res) => res,
            Err(e) => {
                println!("Go Defmt Decoder: String Encoding Error: {}", e);
                result[idx] = ErrCodes::StringEncodingErr as i32;
                continue;
            }
        };

        let content = match std::fs::read(&path) {
            Ok(a) => a,
            Err(e) => {
                println!("Go Defmt Decoder: Read File Error: {}", e);
                result[idx] = ErrCodes::ReadFileErr as i32;
                continue;
            }
        };

        let table = match Table::parse(&content) {
            Ok(Some(t)) => t,
            Ok(None) => {
                println!("Go Defmt Decoder: NoDefmtSectionFound");
                result[idx] = ErrCodes::NoDefmtSectionFound as i32;
                continue;
            }
            Err(e) => {
                println!("Go Defmt Decoder: Table Parsing Error: {}", e);
                result[idx] = ErrCodes::TableParsingErr as i32;
                continue;
            }
        };

        result_vec[idx] = Some(DefmtCtx { table });
    }

    DEFMT_CTX_ARR
        .set(result_vec)
        .expect("Init cannot be called twice");
}
