pub use inner::*;
use std::{
    ffi::{CStr, c_char},
    ptr::{null, null_mut},
};

/// Loads the defmt tables of one or more ELF files. Must be called exactly once, before
/// `defmt_decoder_decode`.
///
/// # Parameters
/// - `arr`: array of `len` NUL-terminated, UTF-8 file paths to ELF files with a `.defmt` section
/// - `len`: number of paths in `arr`
/// - `result_arr`: array of `result_len` integers, receives the status of each path
///   (`result_arr[i]` corresponds to `arr[i]`)
/// - `result_len`: length of `result_arr`, must equal `len`
///
/// The position of a path in `arr` is the `idx` later passed to `defmt_decoder_decode`.
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
///
/// `result_arr` is filled in for every return value.
///
/// # Safety
/// This function takes in an array of paths and an array of integers, so it's the caller's
/// responsibility to make sure that the pointers and lengths are sound.
/// The function asserts that:
/// 1. the lengths are the same
/// 2. both pointers are non-zero
/// 3. every string in the array is not a null pointer
#[unsafe(no_mangle)]
pub unsafe extern "C" fn defmt_decoder_ctx_init(
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
/// - `idx`: index of the ELF file in the `arr` passed to `defmt_decoder_ctx_init`
/// - `input`: raw defmt bytes, starting at a frame boundary
/// - `input_len`: number of bytes in `input`
/// - `output`: buffer that receives the formatted UTF-8 string (not NUL-terminated)
/// - `output_len`: size of `output` in bytes
/// - `consumed`: if a frame was decoded, receives the number of input bytes the frame occupied,
///   so the next frame starts at `input + *consumed`; set to `0` on any error
///
/// # Return value
/// - `>= 0`: the length in bytes of the formatted string. If it is `<= output_len`, the string
///   was written to `output`. Otherwise `output` is too small and nothing is written; retry
///   with a buffer of at least the returned size (`consumed` is still set)
/// - `-1`: the file at `idx` failed to load (see its status code from `defmt_decoder_ctx_init`)
/// - `-2`: `defmt_decoder_ctx_init` has not been called yet
/// - `-3`: `input` ends in the middle of a frame; retry once more bytes are available
/// - `-4`: `input` does not start with a valid frame
/// - `-5`: `idx` is out of bounds of the `arr` passed to `defmt_decoder_ctx_init`
///
/// # Safety
/// This function takes in an input byte array and an output byte buffer, so it's the caller's
/// responsibility to make sure that the pointers and lengths are sound.
/// The function asserts that the pointers are non-zero
#[unsafe(no_mangle)]
pub unsafe extern "C" fn defmt_decoder_decode(
    idx: usize,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
    consumed: *mut usize,
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
        )
    }
}
