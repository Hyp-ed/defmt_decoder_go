use std::{
    collections::VecDeque,
    ffi::CStr,
    sync::{Mutex, OnceLock},
};

use defmt_decoder::{Locations, StreamDecoder, Table};

/// Decoding state of a single ELF file
struct DefmtCtx {
    table: Table,
    /// `None` if the ELF has no (or unparsable) debug info
    locations: Option<Locations>,
    /// Formatted text of the last frame that didn't fit in `decode_inner`'s output
    buffered: Mutex<VecDeque<u8>>,
}

/// One entry per path passed to `init_inner`, `None` if that path failed to load.
/// Set once by `init_inner`, read by `decode_inner` and `received_inner`
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
/// `received_inner` and `decode_inner`.
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

        result_vec[idx] = Some(DefmtCtx {
            table,
            locations,
            buffered: Mutex::new(vec![].into()),
        });

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

/// Error codes of `decode_inner` (a non-negative value is the number of bytes written).
/// Shares its values with `ReceivedErrCodes`
#[repr(i32)]
enum DecodeErrCodes {
    /// The file at `idx` failed to load in `init_inner`
    EntryFailedToInitialize = -1,
    /// `init_inner` has not been called yet
    CalledBeforeInit = -2,
    /// No complete frame has been received yet and no text is left over; call again after
    /// `received_inner` got more bytes. Not an error in a stream
    UnexpectedEOF = -3,
    /// The next frame was corrupt and has been skipped; the next call continues with the frame
    /// after it
    Malformed = -4,
    /// `idx` is not smaller than the number of paths passed to `init_inner`
    IndexOutOfBounds = -5,
}

/// Error codes of `received_inner` (a non-negative value is the number of bytes taken).
/// Shares its values with `DecodeErrCodes`
#[repr(i32)]
enum ReceivedErrCodes {
    /// The file at `idx` failed to load in `init_inner`
    EntryFailedToInitialize = -1,
    /// `init_inner` has not been called yet
    CalledBeforeInit = -2,
    /// `idx` is not smaller than the number of paths passed to `init_inner`
    IndexOutOfBounds = -5,
}

/// Reads decoded text from the stream of the file at `idx` into `output`, like a byte-stream
/// read. The bytes come from `received_inner`.
///
/// Each frame is formatted as UTF-8 text ending in `\n`. If location info is available, a
/// `└─ module @ file:line` line follows the message, before that `\n`. A frame that doesn't fit
/// in `output` is split: the rest is kept and returned first by the next call, so frames
/// always come out whole and in order, but one call may end mid-frame (or mid-character).
///
/// Returns the number of bytes written to `output` (`1..=output.len()`, or `0` if `output` is
/// empty), otherwise a `DecodeErrCodes`. Leftover text from the previous frame is returned on
/// its own, without decoding a new frame, so a decode error never hides it.
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

    let mut buffered = ctx.buffered.lock().unwrap_or_else(|e| e.into_inner());

    let mut num_bytes_copied = 0;

    if !buffered.is_empty() {
        for i in output.iter_mut() {
            *i = match buffered.pop_front() {
                Some(b) => {
                    num_bytes_copied += 1;
                    b
                }
                None => break,
            }
        }
    }

    if num_bytes_copied >= output.len() {
        return num_bytes_copied as i32;
    }

    let remaining_len = output.len() - num_bytes_copied;

    let mut g = decoder.lock().unwrap();

    let frame = match g.decode() {
        Ok(f) => f,
        // no full frame yet: hand back the leftovers if there are any
        Err(defmt_decoder::DecodeError::UnexpectedEof) => {
            if num_bytes_copied > 0 {
                return num_bytes_copied as i32;
            }
            return DecodeErrCodes::UnexpectedEOF as i32;
        }
        // the decoder already skipped the bad frame, so this isn't fatal
        Err(defmt_decoder::DecodeError::Malformed) => {
            if num_bytes_copied > 0 {
                println!("Go Defmt Decoder: Malformed frame skipped");
                return num_bytes_copied as i32;
            }
            return DecodeErrCodes::Malformed as i32;
        }
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

    formatted.push('\n');

    let bytes = formatted.as_bytes();
    if bytes.len() > remaining_len {
        for b in bytes[remaining_len..].iter() {
            buffered.push_back(*b);
        }

        output[num_bytes_copied..(num_bytes_copied + remaining_len)]
            .copy_from_slice(&bytes[..remaining_len]);

        return output.len() as i32;
    }

    output[num_bytes_copied..(num_bytes_copied + bytes.len())].copy_from_slice(bytes);
    (num_bytes_copied + bytes.len()) as i32
}

/// Adds `input`, raw bytes as sent by the target (in the encoding of the ELF at `idx`, e.g.
/// rzCOBS), to the stream of the file at `idx`. They are buffered until `decode_inner` reads
/// the frames out. `input` doesn't need to start or end at a frame boundary.
///
/// Returns the number of bytes taken (always `input.len()`), otherwise a `ReceivedErrCodes`.
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
