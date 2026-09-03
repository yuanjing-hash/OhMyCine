//! Reproducible Windows frame-interpolation asset gate.
//!
//! Passing this gate does not make the backend active: live WGC capture, FP16 synthesis and
//! visible scRGB presentation still have independent runtime gates. The DirectML probe itself
//! binds the model's inputs and flow/mask outputs to D3D12 resources; it never accepts `out0`.

use std::{
    ffi::{c_void, CStr},
    os::windows::ffi::OsStrExt,
    path::PathBuf,
    sync::OnceLock,
};

use sha2::{Digest, Sha256};
use windows_sys::Win32::{Foundation::FreeLibrary, System::LibraryLoader::LoadLibraryW};

const MODEL_RELATIVE_PATH: &str =
    "resources/frame-interpolation/models/rife-v4.6/rife-v4.6-flow-mask.onnx";
const MODEL_SHA256: &str = "067f1eb525cebb0f3d737aac9ca26425e6fad3cdf9afffcb674bd8b62aa03a54";
const ONNX_RUNTIME_SHA256: &str =
    "e7eedec6a6f26dc39dc948276a75ef6d2bee3fff944d874ceed0bbd3b97bff40";
const DIRECTML_SHA256: &str = "9c9e6d822561c6c41b90e6994b3e8857cf1d66dbfb1e0c4c799c7c89b4e92da1";

#[derive(Debug, Clone)]
pub struct WindowsFrameInterpolationAssetProbe {
    pub directml_flow_mask_ready: bool,
    pub reason: String,
}

#[derive(Debug, Clone)]
pub struct WindowsFrameInterpolationSessionStatus {
    pub captured_pair: bool,
    pub hidden_first_present: bool,
    pub generated_first_present: bool,
    pub cadence_stalled: bool,
    pub device_lost: bool,
    pub finished: bool,
    pub successful_present_count: u64,
    pub generated_present_count: u64,
    pub dropped_output_ticks: u64,
    pub inference_sample_count: u64,
    pub latest_inference_ms: f64,
    pub measured_output_fps: f64,
    pub reason: String,
}

/// Owns the hidden product WGC/scRGB preflight session. The original mpv HWND remains visible for
/// this object's entire lifetime. `generated_first_present` is the sole reveal gate and remains
/// false until the native backend has actually composed and presented a flow/mask frame.
pub struct WindowsFrameInterpolationSession {
    raw: *mut c_void,
}

unsafe impl Send for WindowsFrameInterpolationSession {}

impl WindowsFrameInterpolationSession {
    pub fn start(
        mpv_wid: &str,
        target_fps: u32,
        hdr_input: bool,
        flow_scale: f64,
    ) -> Result<Self, String> {
        let source_hwnd = mpv_wid
            .parse::<isize>()
            .map_err(|_| "mpv source HWND is not a valid integer".to_string())?;
        if source_hwnd == 0 {
            return Err("mpv source HWND is null".to_string());
        }
        #[cfg(ohmycine_framegen_directml_probe)]
        {
            let model_path = candidate_model_paths()
                .into_iter()
                .find(|path| path.is_file())
                .ok_or_else(|| "Windows RIFE flow/mask ONNX 不存在".to_string())?;
            let model_path = model_path
                .as_os_str()
                .encode_wide()
                .chain(Some(0))
                .collect::<Vec<_>>();
            let mut reason = vec![0_i8; 1024];
            let proxy_size = proxy_size_for_flow_scale(flow_scale);
            let raw = unsafe {
                ohmycine_windows_framegen_start(
                    source_hwnd,
                    model_path.as_ptr(),
                    target_fps,
                    i32::from(hdr_input),
                    proxy_size,
                    reason.as_mut_ptr(),
                    reason.len(),
                )
            };
            if raw.is_null() {
                return Err(c_reason(&reason));
            }
            Ok(Self { raw })
        }
        #[cfg(not(ohmycine_framegen_directml_probe))]
        {
            let _ = source_hwnd;
            Err("当前 Windows 构建未链接 MSVC WGC/DirectML 产品桥；已保持 mpv 原画面。".to_string())
        }
    }

    pub fn poll(&self) -> Result<WindowsFrameInterpolationSessionStatus, String> {
        #[cfg(ohmycine_framegen_directml_probe)]
        {
            let mut captured_pair = 0;
            let mut hidden_first_present = 0;
            let mut generated_first_present = 0;
            let mut cadence_stalled = 0;
            let mut device_lost = 0;
            let mut finished = 0;
            let mut successful_present_count = 0;
            let mut generated_present_count = 0;
            let mut dropped_output_ticks = 0;
            let mut inference_sample_count = 0;
            let mut latest_inference_micros = 0;
            let mut measured_output_fps = 0.0;
            let mut reason = vec![0_i8; 1024];
            let ok = unsafe {
                ohmycine_windows_framegen_poll(
                    self.raw,
                    &mut captured_pair,
                    &mut hidden_first_present,
                    &mut generated_first_present,
                    &mut cadence_stalled,
                    &mut device_lost,
                    &mut finished,
                    &mut successful_present_count,
                    &mut generated_present_count,
                    &mut dropped_output_ticks,
                    &mut inference_sample_count,
                    &mut latest_inference_micros,
                    &mut measured_output_fps,
                    reason.as_mut_ptr(),
                    reason.len(),
                )
            };
            if ok != 1 {
                return Err(c_reason(&reason));
            }
            Ok(WindowsFrameInterpolationSessionStatus {
                captured_pair: captured_pair == 1,
                hidden_first_present: hidden_first_present == 1,
                generated_first_present: generated_first_present == 1,
                cadence_stalled: cadence_stalled == 1,
                device_lost: device_lost == 1,
                finished: finished == 1,
                successful_present_count,
                generated_present_count,
                dropped_output_ticks,
                inference_sample_count,
                latest_inference_ms: latest_inference_micros as f64 / 1_000.0,
                measured_output_fps,
                reason: c_reason(&reason),
            })
        }
        #[cfg(not(ohmycine_framegen_directml_probe))]
        Err("Windows frame-generation product bridge is not linked".to_string())
    }

    pub fn update_timing(
        &self,
        media_pts_seconds: f64,
        source_fps: f64,
        timing_reliable: bool,
        paused: bool,
    ) {
        #[cfg(ohmycine_framegen_directml_probe)]
        unsafe {
            ohmycine_windows_framegen_update_timing(
                self.raw,
                media_pts_seconds,
                source_fps,
                i32::from(timing_reliable),
                i32::from(paused),
            );
        }
        #[cfg(not(ohmycine_framegen_directml_probe))]
        let _ = (media_pts_seconds, source_fps, timing_reliable, paused);
    }

    pub fn reveal_after_safe_gates(&self) -> Result<(), String> {
        #[cfg(ohmycine_framegen_directml_probe)]
        {
            let mut reason = vec![0_i8; 1024];
            let ok = unsafe {
                ohmycine_windows_framegen_reveal_after_safe_gates(
                    self.raw,
                    reason.as_mut_ptr(),
                    reason.len(),
                )
            };
            (ok == 1).then_some(()).ok_or_else(|| c_reason(&reason))
        }
        #[cfg(not(ohmycine_framegen_directml_probe))]
        Err("Windows frame-generation product bridge is not linked".to_string())
    }
}

fn proxy_size_for_flow_scale(flow_scale: f64) -> u32 {
    // 48 is a balanced-profile selector. The native bridge maps it to an
    // orientation-aware 64x32/32x64 tensor so every RIFE axis remains aligned
    // to 32 and DirectML never reaches an invalid Concat shape.
    if flow_scale <= 0.5 {
        32
    } else if flow_scale <= 0.67 {
        48
    } else {
        64
    }
}

impl Drop for WindowsFrameInterpolationSession {
    fn drop(&mut self) {
        #[cfg(ohmycine_framegen_directml_probe)]
        unsafe {
            ohmycine_windows_framegen_stop(self.raw);
        }
        self.raw = std::ptr::null_mut();
    }
}

#[cfg(ohmycine_framegen_directml_probe)]
unsafe extern "C" {
    fn ohmycine_windows_framegen_start(
        source_hwnd: isize,
        model_path: *const u16,
        target_fps: u32,
        hdr_input: i32,
        proxy_size: u32,
        reason: *mut std::ffi::c_char,
        reason_capacity: usize,
    ) -> *mut c_void;
    fn ohmycine_windows_framegen_poll(
        session: *mut c_void,
        captured_pair: *mut i32,
        hidden_first_present: *mut i32,
        generated_first_present: *mut i32,
        cadence_stalled: *mut i32,
        device_lost: *mut i32,
        finished: *mut i32,
        successful_present_count: *mut u64,
        generated_present_count: *mut u64,
        dropped_output_ticks: *mut u64,
        inference_sample_count: *mut u64,
        latest_inference_micros: *mut u64,
        measured_output_fps: *mut f64,
        reason: *mut std::ffi::c_char,
        reason_capacity: usize,
    ) -> i32;
    fn ohmycine_windows_framegen_update_timing(
        session: *mut c_void,
        media_pts_seconds: f64,
        source_fps: f64,
        timing_reliable: i32,
        paused: i32,
    );
    fn ohmycine_windows_framegen_reveal_after_safe_gates(
        session: *mut c_void,
        reason: *mut std::ffi::c_char,
        reason_capacity: usize,
    ) -> i32;
    fn ohmycine_windows_framegen_stop(session: *mut c_void);
}

#[cfg(ohmycine_framegen_directml_probe)]
fn c_reason(buffer: &[i8]) -> String {
    unsafe { CStr::from_ptr(buffer.as_ptr()) }
        .to_string_lossy()
        .into_owned()
}

pub fn probe() -> &'static WindowsFrameInterpolationAssetProbe {
    static PROBE: OnceLock<WindowsFrameInterpolationAssetProbe> = OnceLock::new();
    PROBE.get_or_init(run_probe)
}

fn run_probe() -> WindowsFrameInterpolationAssetProbe {
    let Some(model_path) = candidate_model_paths()
        .into_iter()
        .find(|path| path.is_file())
    else {
        return failed("Windows RIFE flow/mask ONNX 未安装或未随应用打包。");
    };
    let model = match std::fs::read(&model_path) {
        Ok(model) => model,
        Err(error) => return failed(format!("Windows RIFE ONNX 无法读取：{error}")),
    };
    if format!("{:x}", Sha256::digest(model)) != MODEL_SHA256 {
        return failed("Windows RIFE flow/mask ONNX 完整性校验失败。");
    }

    let runtime_directory = candidate_runtime_directories()
        .into_iter()
        .find(|directory| {
            directory.join("onnxruntime.dll").is_file() && directory.join("DirectML.dll").is_file()
        });
    let Some(runtime_directory) = runtime_directory else {
        return failed("onnxruntime.dll 或 DirectML.dll 未安装到应用运行目录。");
    };
    for (file, expected) in [
        ("onnxruntime.dll", ONNX_RUNTIME_SHA256),
        ("DirectML.dll", DIRECTML_SHA256),
    ] {
        let path = runtime_directory.join(file);
        let Ok(bytes) = std::fs::read(path) else {
            return failed(format!("{file} 无法读取。"));
        };
        if format!("{:x}", Sha256::digest(bytes)) != expected {
            return failed(format!("{file} 完整性校验失败。"));
        }
    }
    // Load DirectML first and keep it resident while probing ONNX Runtime so
    // dependency resolution is deterministic in development and packaged layouts.
    let directml = load_absolute(runtime_directory.join("DirectML.dll"));
    if directml.is_null() {
        return failed("DirectML.dll 加载失败。");
    }
    let onnxruntime = load_absolute(runtime_directory.join("onnxruntime.dll"));
    if onnxruntime.is_null() {
        unsafe { FreeLibrary(directml) };
        return failed("onnxruntime.dll 加载失败。");
    }
    unsafe {
        FreeLibrary(onnxruntime);
        FreeLibrary(directml);
    }

    #[cfg(ohmycine_framegen_directml_probe)]
    if let Err(reason) = probe_directml_model(&model_path) {
        return failed(format!("DirectML flow/mask 推理自检失败：{reason}"));
    }
    #[cfg(not(ohmycine_framegen_directml_probe))]
    return failed("当前构建未链接 ONNX Runtime DirectML 推理自检桥。");

    WindowsFrameInterpolationAssetProbe {
        directml_flow_mask_ready: true,
        reason: "DirectML flow/mask 已用 GPU 常驻 D3D12 张量通过自检，等待实时 WGC→FP16 合成→scRGB 首帧呈现。"
            .to_string(),
    }
}

#[cfg(ohmycine_framegen_directml_probe)]
fn probe_directml_model(model_path: &std::path::Path) -> Result<(), String> {
    unsafe extern "C" {
        fn ohmycine_probe_directml_flow_mask(
            model_path: *const u16,
            reason: *mut std::ffi::c_char,
            reason_capacity: usize,
        ) -> i32;
    }
    let wide = model_path
        .as_os_str()
        .encode_wide()
        .chain(Some(0))
        .collect::<Vec<_>>();
    let mut reason = vec![0_i8; 1024];
    let result = unsafe {
        ohmycine_probe_directml_flow_mask(wide.as_ptr(), reason.as_mut_ptr(), reason.len())
    };
    if result == 1 {
        Ok(())
    } else {
        let message = unsafe { CStr::from_ptr(reason.as_ptr()) }
            .to_string_lossy()
            .into_owned();
        Err(message)
    }
}

fn candidate_model_paths() -> Vec<PathBuf> {
    let mut candidates = Vec::new();
    if let Ok(executable) = std::env::current_exe() {
        if let Some(directory) = executable.parent() {
            candidates.push(directory.join(MODEL_RELATIVE_PATH));
        }
    }
    candidates.push(PathBuf::from(env!("CARGO_MANIFEST_DIR")).join(MODEL_RELATIVE_PATH));
    candidates
}

fn candidate_runtime_directories() -> Vec<PathBuf> {
    let mut candidates = Vec::new();
    if let Ok(executable) = std::env::current_exe() {
        if let Some(directory) = executable.parent() {
            candidates.push(directory.to_path_buf());
        }
    }
    candidates.push(PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("lib/frame-interpolation"));
    candidates
}

fn load_absolute(path: PathBuf) -> windows_sys::Win32::Foundation::HMODULE {
    let wide = path
        .as_os_str()
        .encode_wide()
        .chain(Some(0))
        .collect::<Vec<_>>();
    unsafe { LoadLibraryW(wide.as_ptr()) }
}

fn failed(reason: impl Into<String>) -> WindowsFrameInterpolationAssetProbe {
    WindowsFrameInterpolationAssetProbe {
        directml_flow_mask_ready: false,
        reason: reason.into(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn quality_scales_select_distinct_aligned_proxy_profiles() {
        assert_eq!(proxy_size_for_flow_scale(1.0), 64);
        assert_eq!(proxy_size_for_flow_scale(0.67), 48);
        assert_eq!(proxy_size_for_flow_scale(0.5), 32);
    }

    #[test]
    fn pinned_development_model_has_the_expected_digest_when_installed() {
        let path = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join(MODEL_RELATIVE_PATH);
        if path.is_file() {
            let bytes = std::fs::read(path).unwrap();
            assert_eq!(format!("{:x}", Sha256::digest(bytes)), MODEL_SHA256);
        }
    }

    #[test]
    fn pinned_development_directml_runtime_has_expected_digests_when_installed() {
        let directory = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("lib/frame-interpolation");
        for (file, expected) in [
            ("onnxruntime.dll", ONNX_RUNTIME_SHA256),
            ("DirectML.dll", DIRECTML_SHA256),
        ] {
            let path = directory.join(file);
            if path.is_file() {
                let bytes = std::fs::read(path).unwrap();
                assert_eq!(format!("{:x}", Sha256::digest(bytes)), expected);
            }
        }
    }

    #[cfg(ohmycine_framegen_directml_probe)]
    #[test]
    fn directml_executes_the_pinned_flow_mask_model() {
        let result = run_probe();
        assert!(
            result
                .reason
                .starts_with("DirectML flow/mask 已用 GPU 常驻 D3D12 张量通过自检"),
            "{}",
            result.reason
        );
    }
}
