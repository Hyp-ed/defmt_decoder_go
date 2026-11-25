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
