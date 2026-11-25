pub use inner::*;
use std::ffi::CString;

/// # Safety
/// This function takes in an array of paths and an array of integers, so it's the caller's
/// responsibility to make sure that the pointers and lengths are sound
#[unsafe(no_mangle)]
pub unsafe extern "C" fn init(
    arr: *const CString,
    len: usize,
    result_arr: *mut i32,
    result_len: usize,
) {
    unsafe {
        init_inner(
            std::slice::from_raw_parts(arr, len),
            std::slice::from_raw_parts_mut(result_arr, result_len),
        );
    }
}
