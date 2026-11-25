pub use inner::*;
use std::{
    ffi::{CStr, c_char},
    ptr::{null, null_mut},
};

/// Loads the defmt tables of one or more ELF files. Must be called exactly once, before `decode`.
///
/// # Parameters
/// - `arr`: array of `len` NUL-terminated, UTF-8 file paths to ELF files with a `.defmt` section
/// - `len`: number of paths in `arr`
/// - `result_arr`: array of `result_len` integers, receives the status of each path
///   (`result_arr[i]` corresponds to `arr[i]`)
/// - `result_len`: length of `result_arr`, must equal `len`
///
/// The position of a path in `arr` is the `idx` later passed to `decode`.
///
/// # Per-path status codes (written to `result_arr`)
/// - `0`: success
/// - `-1`: the path is not valid UTF-8
/// - `-2`: the file could not be read
/// - `-3`: the defmt table could not be parsed
/// - `-4`: the file has no `.defmt` section
///
/// A failed path does not fail the whole call; the other paths can still be decoded.
/// Missing location info (e.g. no debug info) is not an error, frames are then decoded without
/// the `└─ module @ file:line` suffix.
///
/// # Return value
/// - `0`: success (check `result_arr` for the status of each path)
/// - `-1`: `init` was already called; the new tables are discarded and the old ones kept
///
/// # Safety
/// This function takes in an array of paths and an array of integers, so it's the caller's
/// responsibility to make sure that the pointers and lengths are sound.
/// The function asserts that:
/// 1. the lengths are the same
/// 2. both pointers are non-zero
/// 3. every string in the array is not a null pointer
#[unsafe(no_mangle)]
pub unsafe extern "C" fn init(
    arr: *const *const c_char,
    len: usize,
    result_arr: *mut i32,
    result_len: usize,
) -> i32 {
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
}

/// Decodes a single defmt frame from the start of `input` into a human-readable string.
///
/// # Parameters
/// - `idx`: index of the ELF file in the `arr` passed to `init`
/// - `input`: raw defmt bytes, starting at a frame boundary
/// - `input_len`: number of bytes in `input`
/// - `output`: buffer that receives the formatted UTF-8 string (not NUL-terminated)
/// - `output_len`: size of `output` in bytes
/// - `consumed`: on success, receives the number of input bytes the frame occupied, so the
///   next frame starts at `input + *consumed`; set to `0` on any error
/// - `colored`: if true, the log level is wrapped in ANSI color codes, which count towards the
///   output length
///
/// # Return value
/// - `>= 0`: success, the number of bytes written to `output`
/// - `-1`: `output` is too small for the formatted string; nothing is written, retry with a
///   larger buffer
/// - `-2`: the file at `idx` failed to initialize (see its status code from `init`)
/// - `-3`: `init` has not been called yet
/// - `-4`: `input` ends in the middle of a frame; retry once more bytes are available
/// - `-5`: `input` does not contain a valid frame
/// - `-6`: `idx` is out of bounds of the `arr` passed to `init`
///
/// # Safety
/// This function takes in an input byte array and an output byte buffer, so it's the caller's
/// responsibility to make sure that the pointers and lengths are sound.
/// The function asserts that the pointers are non-zero
#[unsafe(no_mangle)]
pub unsafe extern "C" fn decode(
    idx: usize,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
    consumed: *mut usize,
    colored: bool,
) -> i32 {
    assert_ne!(input, null());
    assert_ne!(output, null_mut());
    assert_ne!(consumed, null_mut());

    unsafe {
        decode_inner(
            idx,
            std::slice::from_raw_parts(input, input_len),
            std::slice::from_raw_parts_mut(output, output_len),
            &mut *consumed,
            colored,
        )
    }
}
