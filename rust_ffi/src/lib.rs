pub use inner::*;
use std::{
    ffi::CString,
    ptr::{null, null_mut},
};

/// # Safety
/// This function takes in an array of paths and an array of integers, so it's the caller's
/// responsibility to make sure that the pointers and lengths are sound.
/// The function asserts that:
/// 1. the lengths are the same
/// 2. both pointers are non-zero
#[unsafe(no_mangle)]
pub unsafe extern "C" fn init(
    arr: *const CString,
    len: usize,
    result_arr: *mut i32,
    result_len: usize,
) {
    assert_eq!(len, result_len);
    assert_ne!(arr, null());
    assert_ne!(result_arr, null_mut());

    unsafe {
        init_inner(
            std::slice::from_raw_parts(arr, len),
            std::slice::from_raw_parts_mut(result_arr, result_len),
        );
    }
}

/// # Safety
/// This function takes in an array of bytes and a result array of bytes, so it's the caller's
/// responsibility to make sure that the pointers and lengths are sound.
/// The function asserts that the pointers are non-zero
/// This function returns the size of bytes written, or error (as a negative number)
/// If the formatted string is larger than the supplied output buffer length, it will return -1
/// On success, the number of input bytes the frame occupied is written to `consumed`,
/// otherwise `consumed` is set to 0
/// If `colored` is true, the log level is wrapped in ANSI color codes, which count towards the
/// output length
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
