use std::{ffi::CString, sync::OnceLock};

use defmt_decoder::{Locations, Table};

#[derive(Clone, Debug)]
struct DefmtCtx {
    table: Table,
    locations: Option<Locations>,
}

static DEFMT_CTX_ARR: OnceLock<Vec<Option<DefmtCtx>>> = OnceLock::new();

#[repr(i32)]
enum InitErrCodes {
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
                result[idx] = InitErrCodes::StringEncodingErr as i32;
                continue;
            }
        };

        let content = match std::fs::read(&path) {
            Ok(a) => a,
            Err(e) => {
                println!("Go Defmt Decoder: Read File Error: {}", e);
                result[idx] = InitErrCodes::ReadFileErr as i32;
                continue;
            }
        };

        let table = match Table::parse(&content) {
            Ok(Some(t)) => t,
            Ok(None) => {
                println!("Go Defmt Decoder: NoDefmtSectionFound");
                result[idx] = InitErrCodes::NoDefmtSectionFound as i32;
                continue;
            }
            Err(e) => {
                println!("Go Defmt Decoder: Table Parsing Error: {}", e);
                result[idx] = InitErrCodes::TableParsingErr as i32;
                continue;
            }
        };

        // location info is optional, decoding still works without it
        let locations = match table.get_locations(&content) {
            Ok(l) if !l.is_empty() => Some(l),
            Ok(_) => {
                println!("Go Defmt Decoder: No location info found (missing debug info?)");
                None
            }
            Err(e) => {
                println!("Go Defmt Decoder: Location Parsing Error: {}", e);
                None
            }
        };

        result_vec[idx] = Some(DefmtCtx { table, locations });
    }

    DEFMT_CTX_ARR
        .set(result_vec)
        .expect("Init cannot be called twice");
}

#[repr(i32)]
enum DecodeErrCodes {
    OutputBufferTooSmall = -1,
    EntryFailedToInitialize = -2,
    UnexpectedEOF = -4,
    Malformed = -5,
    CalledBeforeInit = -3,
    IndexOutOfBounds = -6,
}

pub fn decode_inner(
    idx: usize,
    input: &[u8],
    output: &mut [u8],
    consumed: &mut usize,
    colored: bool,
) -> i32 {
    *consumed = 0;

    let Some(ctx) = DEFMT_CTX_ARR.get() else {
        return DecodeErrCodes::CalledBeforeInit as i32;
    };

    let Some(entry) = ctx.get(idx) else {
        return DecodeErrCodes::IndexOutOfBounds as i32;
    };

    let Some(ctx) = entry else {
        return DecodeErrCodes::EntryFailedToInitialize as i32;
    };

    let (frame, frame_size) = match ctx.table.decode(input) {
        Ok(f) => f,
        Err(e) => match e {
            defmt_decoder::DecodeError::UnexpectedEof => {
                return DecodeErrCodes::UnexpectedEOF as i32;
            }
            defmt_decoder::DecodeError::Malformed => {
                return DecodeErrCodes::Malformed as i32;
            }
        },
    };

    let mut formatted = frame.display(colored).to_string();

    if let Some(loc) = ctx
        .locations
        .as_ref()
        .and_then(|locs| locs.get(&frame.index()))
    {
        formatted.push_str(&format!(
            "\n└─ {} @ {}:{}",
            loc.module,
            loc.file.display(),
            loc.line
        ));
    }

    let bytes = formatted.as_bytes();
    if bytes.len() > output.len() || bytes.len() > i32::MAX as usize {
        return DecodeErrCodes::OutputBufferTooSmall as i32;
    }

    output[..bytes.len()].copy_from_slice(bytes);
    *consumed = frame_size;
    bytes.len() as i32
}
