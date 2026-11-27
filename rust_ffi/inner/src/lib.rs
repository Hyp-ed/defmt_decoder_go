use std::{
    ffi::CStr,
    sync::{Mutex, OnceLock},
};

use defmt_decoder::{Locations, StreamDecoder, Table};

/// Decoding state of a single ELF file
struct DefmtCtx {
    table: Table,
    /// `None` if the ELF has no (or unparsable) debug info
    locations: Option<Locations>,
}

/// One entry per path passed to `init_inner`, `None` if that path failed to load.
/// Set once by `init_inner`, read by `decode_inner`
static DEFMT_CTX_ARR: OnceLock<Vec<Option<DefmtCtx>>> = OnceLock::new();

type Decoder = Mutex<Box<dyn StreamDecoder + Send + Sync + 'static>>;

/// Stream decoders borrowing the tables in `DEFMT_CTX_ARR`, same indices and `None`s.
/// Set by `init_inner` right after `DEFMT_CTX_ARR`
static DECODER_ARR: OnceLock<Vec<Option<Decoder>>> = OnceLock::new();

/// Per-path status codes written to the `result` array of `init_inner` (`0` on success)
#[repr(i32)]
enum InitArrErrCodes {
    /// The path is not valid UTF-8
    StringEncodingErr = -1,
    /// The file could not be read
    ReadFileErr = -2,
    /// The defmt table could not be parsed
    TableParsingErr = -3,
    /// The file has no `.defmt` section
    NoDefmtSectionFound = -4,
}

/// Return codes of `init_inner` (`0` on success)
#[repr(i32)]
enum InitErrCodes {
    /// `init_inner` was already called; the new tables are discarded and the old ones kept
    CalledTwice = -1,
    /// At least one path failed to load, see `result` for which ones.
    /// The other paths were loaded and can be decoded
    AtLeastOneEntryFailed = -2,
}

/// Loads the defmt table (and location info, if present) of each ELF file in `paths`.
///
/// `result[i]` receives the status of `paths[i]`: `0` on success, otherwise an
/// `InitArrErrCodes`. The index of a path in `paths` is the `idx` later passed to
/// `decode_inner`.
///
/// Returns `0` if every path loaded, otherwise an `InitErrCodes`. `CalledTwice` takes
/// precedence over `AtLeastOneEntryFailed`; `result` is filled in either way.
///
/// # Panics
/// If `result` is shorter than `paths`
pub fn init_inner(paths: &[&CStr], result: &mut [i32]) -> i32 {
    let mut result_vec: Vec<Option<DefmtCtx>> = vec![];
    for _ in 0..paths.len() {
        result_vec.push(None);
    }
    let mut failed: bool = false;

    for (idx, path) in paths.iter().enumerate() {
        let path = match path.to_str() {
            Ok(res) => res,
            Err(e) => {
                println!("Go Defmt Decoder: String Encoding Error: {}", e);
                result[idx] = InitArrErrCodes::StringEncodingErr as i32;
                failed = true;
                continue;
            }
        };

        let content = match std::fs::read(path) {
            Ok(a) => a,
            Err(e) => {
                println!("Go Defmt Decoder: Read File Error: {}", e);
                result[idx] = InitArrErrCodes::ReadFileErr as i32;
                failed = true;
                continue;
            }
        };

        let table = match Table::parse(&content) {
            Ok(Some(t)) => t,
            Ok(None) => {
                println!("Go Defmt Decoder: NoDefmtSectionFound");
                result[idx] = InitArrErrCodes::NoDefmtSectionFound as i32;
                failed = true;
                continue;
            }
            Err(e) => {
                println!("Go Defmt Decoder: Table Parsing Error: {}", e);
                result[idx] = InitArrErrCodes::TableParsingErr as i32;
                failed = true;
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

        result[idx] = 0;
    }

    let Ok(_) = DEFMT_CTX_ARR.set(result_vec) else {
        return InitErrCodes::CalledTwice as i32;
    };

    // DEFMT_CTX_ARR is now 'static, so the decoders can borrow from it
    let ctx_arr: &'static [Option<DefmtCtx>] = DEFMT_CTX_ARR.get().unwrap();
    let decoders = ctx_arr
        .iter()
        .map(|c| c.as_ref().map(|c| Mutex::new(c.table.new_stream_decoder())))
        .collect();
    // only the caller that won the set above gets here, so this can't fail
    let _ = DECODER_ARR.set(decoders);

    if failed {
        return InitErrCodes::AtLeastOneEntryFailed as i32;
    }

    0
}

/// Return codes of `decode_inner` (a non-negative value is the length of the formatted string)
#[repr(i32)]
enum DecodeErrCodes {
    /// The file at `idx` failed to load in `init_inner`
    EntryFailedToInitialize = -1,
    /// `init_inner` has not been called yet
    CalledBeforeInit = -2,
    /// `input` ends in the middle of a frame; retry once more bytes are available
    UnexpectedEOF = -3,
    /// `input` does not start with a valid frame
    Malformed = -4,
    /// `idx` is not smaller than the number of paths passed to `init_inner`
    IndexOutOfBounds = -5,
}

/// Return codes of `decode_inner` (a non-negative value is the length of the formatted string)
#[repr(i32)]
enum ReceivedErrCodes {
    /// The file at `idx` failed to load in `init_inner`
    EntryFailedToInitialize = -1,
    /// `init_inner` has not been called yet
    CalledBeforeInit = -2,
    /// `idx` is not smaller than the number of paths passed to `init_inner`
    IndexOutOfBounds = -5,
}

/// Decodes the frame at the start of `input` with the table of the file at `idx`, and writes
/// it as a UTF-8 string (not NUL-terminated) to `output`. If location info is available, a
/// `\n└─ module @ file:line` line is appended.
///
/// If a frame was decoded, returns the length of the formatted string and sets `consumed` to
/// the number of input bytes the frame occupied. The string is written only if it fits in
/// `output`; if the returned length exceeds `output.len()`, nothing is written and the caller
/// should retry with a buffer of at least that size. On error, returns a `DecodeErrCodes` and
/// sets `consumed` to `0`.
pub fn decode_inner(idx: usize, output: &mut [u8]) -> i32 {
    // DECODER_ARR is set last, so DEFMT_CTX_ARR is set too if it is
    let Some(decoders) = DECODER_ARR.get() else {
        return DecodeErrCodes::CalledBeforeInit as i32;
    };

    let Some(entry) = decoders.get(idx) else {
        return DecodeErrCodes::IndexOutOfBounds as i32;
    };

    let Some(decoder) = entry else {
        return DecodeErrCodes::EntryFailedToInitialize as i32;
    };

    // same indices and `None`s as DECODER_ARR
    let Some(ctx) = &DEFMT_CTX_ARR.get().unwrap()[idx] else {
        unreachable!()
    };

    let mut g = decoder.lock().unwrap();

    let frame = match g.decode() {
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

    let mut formatted = frame.display(false).to_string();

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
        return bytes.len() as i32;
    }

    output[..bytes.len()].copy_from_slice(bytes);
    bytes.len() as i32
}

pub fn received_inner(idx: usize, input: &[u8]) -> i32 {
    let Some(decoders) = DECODER_ARR.get() else {
        return ReceivedErrCodes::CalledBeforeInit as i32;
    };

    let Some(entry) = decoders.get(idx) else {
        return ReceivedErrCodes::IndexOutOfBounds as i32;
    };

    let Some(decoder) = entry else {
        return ReceivedErrCodes::EntryFailedToInitialize as i32;
    };

    let Some(_) = &DEFMT_CTX_ARR.get().unwrap()[idx] else {
        unreachable!()
    };

    let mut guard = decoder.lock().unwrap_or_else(|e| e.into_inner());
    guard.received(input);

    input.len() as i32
}
