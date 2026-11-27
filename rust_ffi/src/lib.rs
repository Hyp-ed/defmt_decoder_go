pub use inner::*;
use std::{
    ffi::{CStr, c_char},
    panic::{AssertUnwindSafe, catch_unwind},
};

/// Returned by every function if it panicked (a failed assertion included). The reason and
/// location are printed to stderr by the panic hook
pub const DEFMT_DECODER_PANICKED: i32 = -6;

/// Runs `f`, catching a panic so it doesn't unwind into the C caller, which would abort the
/// process. A panic is returned as `DEFMT_DECODER_PANICKED`; the panic hook has already
/// printed it.
///
/// Unwind safety is asserted: the shared state behind the mutexes stays usable after a panic,
/// since every lock recovers from poisoning
fn catch_panic(f: impl FnOnce() -> i32) -> i32 {
    catch_unwind(AssertUnwindSafe(f)).unwrap_or(DEFMT_DECODER_PANICKED)
}

/// Loads the defmt tables of one or more ELF files and sets up one stream per file. Must be
/// called exactly once, before `defmt_decoder_received` and `defmt_decoder_decode`.
///
/// # Parameters
/// - `arr`: array of `len` NUL-terminated, UTF-8 file paths to ELF files with a `.defmt` section
/// - `len`: number of paths in `arr`
/// - `result_arr`: array of `result_len` integers, receives the status of each path
///   (`result_arr[i]` corresponds to `arr[i]`)
/// - `result_len`: length of `result_arr`, must equal `len`
///
/// The position of a path in `arr` is the `idx` later passed to `defmt_decoder_received` and
/// `defmt_decoder_decode`.
///
/// # Per-path status codes (written to `result_arr`)
/// - `0`: success
/// - `-1`: the path is not valid UTF-8
/// - `-2`: the file could not be read
/// - `-3`: the defmt table could not be parsed
/// - `-4`: the file has no `.defmt` section
///
/// A failed path does not prevent the other paths from loading; they can still be decoded.
/// Missing location info (e.g. no debug info) is not an error, frames are then decoded without
/// the `└─ module @ file:line` suffix.
///
/// # Return value
/// - `0`: success, every path loaded
/// - `-1`: `defmt_decoder_ctx_init` was already called; the new tables are discarded and the
///   old ones kept. Takes precedence over `-2`
/// - `-2`: at least one path failed to load, check `result_arr` for which ones. The other
///   paths were loaded and can be decoded
/// - `-6` (`DEFMT_DECODER_PANICKED`): panicked, e.g. a failed assertion below
///
/// `result_arr` is filled in for every return value except `-6`, where it may be partly filled.
///
/// # Safety
/// This function takes in an array of paths and an array of integers, so it's the caller's
/// responsibility to make sure that the pointers and lengths are sound.
/// The function asserts that (a failed assertion returns `-6`):
/// 1. the lengths are the same
/// 2. both pointers are non-null
/// 3. every string in the array is not a null pointer
#[unsafe(no_mangle)]
pub unsafe extern "C" fn defmt_decoder_ctx_init(
    arr: *const *const c_char,
    len: usize,
    result_arr: *mut i32,
    result_len: usize,
) -> i32 {
    catch_panic(|| {
        assert_eq!(len, result_len);
        assert!(!arr.is_null());
        assert!(!result_arr.is_null());

        unsafe {
            let raw_arr = std::slice::from_raw_parts(arr, len);

            let mut strings = vec![];
            for raw_str in raw_arr.iter() {
                assert!(!raw_str.is_null());
                strings.push(CStr::from_ptr(*raw_str));
            }

            init_inner(
                &strings,
                std::slice::from_raw_parts_mut(result_arr, result_len),
            )
        }
    })
}

/// Reads decoded text from the stream of the ELF file at `idx`, like a byte-stream read. Bytes
/// go into the stream with `defmt_decoder_received`.
///
/// Each frame is formatted as UTF-8 text ending in `\n` (not NUL-terminated). If location info
/// is available, a `└─ module @ file:line` line follows the message, before that `\n`. A frame
/// that doesn't fit in `output` is split: the rest is returned first by the next call, so frames
/// come out whole and in order, but one call may end mid-frame (or mid-character).
///
/// # Parameters
/// - `idx`: index of the ELF file in the `arr` passed to `defmt_decoder_ctx_init`
/// - `output`: buffer that receives the text
/// - `output_len`: size of `output` in bytes. Values above `INT32_MAX` are treated as `INT32_MAX`
///
/// # Return value
/// - `> 0`: number of bytes written to `output`
/// - `0`: only if `output_len` is `0`
/// - `-1`: the file at `idx` failed to load (see its status code from `defmt_decoder_ctx_init`)
/// - `-2`: `defmt_decoder_ctx_init` has not been called yet
/// - `-3`: no complete frame has been received yet; call again after `defmt_decoder_received`.
///   Not an error in a stream
/// - `-4`: the next frame was corrupt and has been skipped; call again to continue with the
///   frame after it
/// - `-5`: `idx` is out of bounds of the `arr` passed to `defmt_decoder_ctx_init`
/// - `-6` (`DEFMT_DECODER_PANICKED`): panicked, e.g. a failed assertion below
///
/// # Safety
/// `output` must be valid for writes of `output_len` bytes. The function asserts that `output`
/// is non-null; a failed assertion returns `-6`
#[unsafe(no_mangle)]
pub unsafe extern "C" fn defmt_decoder_decode(
    idx: usize,
    output: *mut u8,
    output_len: usize,
) -> i32 {
    catch_panic(|| {
        assert!(!output.is_null());

        // the return value is an i32, so never write more than it can count
        let output_len = output_len.min(i32::MAX as usize);

        unsafe { decode_inner(idx, std::slice::from_raw_parts_mut(output, output_len)) }
    })
}

/// Adds raw bytes, as sent by the target, to the stream of the ELF file at `idx`. They are
/// buffered until `defmt_decoder_decode` reads the frames out, so `input` doesn't need to start
/// or end at a frame boundary. The encoding (e.g. rzCOBS) is the one the ELF was built with.
///
/// # Parameters
/// - `idx`: index of the ELF file in the `arr` passed to `defmt_decoder_ctx_init`
/// - `input`: raw bytes from the target
/// - `input_len`: number of bytes in `input`, at most `INT32_MAX`
///
/// # Return value
/// - `>= 0`: number of bytes taken, always `input_len`
/// - `-1`: the file at `idx` failed to load (see its status code from `defmt_decoder_ctx_init`)
/// - `-2`: `defmt_decoder_ctx_init` has not been called yet
/// - `-5`: `idx` is out of bounds of the `arr` passed to `defmt_decoder_ctx_init`
/// - `-6` (`DEFMT_DECODER_PANICKED`): panicked, e.g. a failed assertion below
///
/// # Safety
/// `input` must be valid for reads of `input_len` bytes. The function asserts that `input` is
/// non-null (even if `input_len` is `0`); a failed assertion returns `-6`
#[unsafe(no_mangle)]
pub unsafe extern "C" fn defmt_decoder_received(
    idx: usize,
    input: *const u8,
    input_len: usize,
) -> i32 {
    catch_panic(|| {
        assert!(!input.is_null());

        unsafe { received_inner(idx, std::slice::from_raw_parts(input, input_len)) }
    })
}
