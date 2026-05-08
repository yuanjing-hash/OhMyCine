use std::{
    ffi::{CStr, CString},
    os::raw::{c_char, c_int, c_void},
    ptr,
    sync::{Arc, Mutex},
};

use libmpv_sys::{
    mpv_command, mpv_create, mpv_error_string, mpv_format_MPV_FORMAT_DOUBLE,
    mpv_format_MPV_FORMAT_FLAG, mpv_format_MPV_FORMAT_INT64, mpv_free, mpv_get_property,
    mpv_get_property_string, mpv_handle, mpv_initialize, mpv_set_option_string, mpv_set_property,
    mpv_set_property_string, mpv_terminate_destroy,
};

use super::render::{current_render_state, MpvRenderState};

pub type MpvState = Arc<Mutex<MpvPlayer>>;

pub struct MpvPlayer {
    ctx: *mut mpv_handle,
}

// MpvPlayer is only accessed through Arc<Mutex<_>> in Tauri state. libmpv handles are designed
// to be controlled from multiple threads as long as individual calls are synchronized.
unsafe impl Send for MpvPlayer {}

impl Drop for MpvPlayer {
    fn drop(&mut self) {
        if !self.ctx.is_null() {
            unsafe { mpv_terminate_destroy(self.ctx) };
        }
    }
}

impl MpvPlayer {
    pub fn new() -> Result<Self, String> {
        let ctx = unsafe { mpv_create() };
        if ctx.is_null() {
            return Err("failed to create libmpv context".to_string());
        }

        let player = Self { ctx };
        // The current Tauri integration does not yet provide an mpv render context or native
        // window/surface handle for embedding video in the WebView. Using the normal gpu VO here
        // makes libmpv create a separate, unmanaged mpv window. Until MpvRenderContext is wired,
        // keep playback backend/audio validation available but suppress visible external video.
        player.set_option("force-window", "no")?;
        player.set_option("vo", "null")?;
        player.set_option("video", "no")?;
        player.set_option("hwdec", "auto")?;
        player.set_option("keep-open", "yes")?;
        player.set_option("osc", "no")?;
        player.check_error(unsafe { mpv_initialize(player.ctx) })?;

        Ok(player)
    }

    pub fn load_file(&self, path: &str) -> Result<(), String> {
        self.command(&["loadfile", path, "replace"])
    }

    pub fn render_state(&self) -> MpvRenderState {
        current_render_state()
    }

    #[allow(dead_code)]
    pub(super) fn raw_handle_for_render(&self) -> *mut mpv_handle {
        self.ctx
    }

    pub fn pause(&self) -> Result<(), String> {
        self.set_property("pause", "true")
    }

    pub fn resume(&self) -> Result<(), String> {
        self.set_property("pause", "false")
    }

    pub fn seek(&self, position: f64) -> Result<(), String> {
        self.command(&["seek", &position.to_string(), "absolute"])
    }

    fn set_option(&self, option: &str, value: &str) -> Result<(), String> {
        let option = CString::new(option).map_err(|err| err.to_string())?;
        let value = CString::new(value).map_err(|err| err.to_string())?;
        self.check_error(unsafe { mpv_set_option_string(self.ctx, option.as_ptr(), value.as_ptr()) })
    }

    pub fn set_property(&self, prop: &str, value: &str) -> Result<(), String> {
        let prop = CString::new(prop).map_err(|err| err.to_string())?;

        match prop.to_str().unwrap_or_default() {
            "pause" => {
                let mut value = if value == "true" || value == "1" {
                    1
                } else {
                    0
                };
                self.check_error(unsafe {
                    mpv_set_property(
                        self.ctx,
                        prop.as_ptr(),
                        mpv_format_MPV_FORMAT_FLAG,
                        (&mut value as *mut c_int).cast::<c_void>(),
                    )
                })
            }
            "volume" | "time-pos" | "duration" | "speed" => {
                let mut value = value.parse::<f64>().map_err(|err| err.to_string())?;
                self.check_error(unsafe {
                    mpv_set_property(
                        self.ctx,
                        prop.as_ptr(),
                        mpv_format_MPV_FORMAT_DOUBLE,
                        (&mut value as *mut f64).cast::<c_void>(),
                    )
                })
            }
            "sid" | "aid" => {
                let mut value = value.parse::<i64>().map_err(|err| err.to_string())?;
                self.check_error(unsafe {
                    mpv_set_property(
                        self.ctx,
                        prop.as_ptr(),
                        mpv_format_MPV_FORMAT_INT64,
                        (&mut value as *mut i64).cast::<c_void>(),
                    )
                })
            }
            _ => {
                let value = CString::new(value).map_err(|err| err.to_string())?;
                self.check_error(unsafe {
                    mpv_set_property_string(self.ctx, prop.as_ptr(), value.as_ptr())
                })
            }
        }
    }

    pub fn get_property(&self, prop: &str) -> Result<String, String> {
        if let Some(value) = self.get_property_string(prop)? {
            return Ok(value);
        }
        if let Ok(value) = self.get_property_double(prop) {
            return Ok(value.to_string());
        }
        if let Ok(value) = self.get_property_int64(prop) {
            return Ok(value.to_string());
        }
        if let Ok(value) = self.get_property_flag(prop) {
            return Ok((value != 0).to_string());
        }
        Err(format!("failed to get mpv property: {prop}"))
    }

    pub fn time_pos(&self) -> f64 {
        self.get_property_double("time-pos").unwrap_or(0.0)
    }

    pub fn duration(&self) -> f64 {
        self.get_property_double("duration").unwrap_or(0.0)
    }

    pub fn paused(&self) -> bool {
        self.get_property_flag("pause").unwrap_or(1) != 0
    }

    fn command(&self, args: &[&str]) -> Result<(), String> {
        let c_args = args
            .iter()
            .map(|arg| CString::new(*arg).map_err(|err| err.to_string()))
            .collect::<Result<Vec<_>, _>>()?;
        let mut raw_args = c_args
            .iter()
            .map(|arg| arg.as_ptr())
            .chain(std::iter::once(ptr::null()))
            .collect::<Vec<*const c_char>>();

        self.check_error(unsafe { mpv_command(self.ctx, raw_args.as_mut_ptr()) })
    }

    fn get_property_string(&self, prop: &str) -> Result<Option<String>, String> {
        let prop = CString::new(prop).map_err(|err| err.to_string())?;
        let value = unsafe { mpv_get_property_string(self.ctx, prop.as_ptr()) };
        if value.is_null() {
            return Ok(None);
        }

        let result = unsafe { CStr::from_ptr(value) }
            .to_string_lossy()
            .into_owned();
        unsafe { mpv_free(value.cast::<c_void>()) };
        Ok(Some(result))
    }

    fn get_property_double(&self, prop: &str) -> Result<f64, String> {
        let prop = CString::new(prop).map_err(|err| err.to_string())?;
        let mut value = 0.0_f64;
        self.check_error(unsafe {
            mpv_get_property(
                self.ctx,
                prop.as_ptr(),
                mpv_format_MPV_FORMAT_DOUBLE,
                (&mut value as *mut f64).cast::<c_void>(),
            )
        })?;
        Ok(value)
    }

    fn get_property_int64(&self, prop: &str) -> Result<i64, String> {
        let prop = CString::new(prop).map_err(|err| err.to_string())?;
        let mut value = 0_i64;
        self.check_error(unsafe {
            mpv_get_property(
                self.ctx,
                prop.as_ptr(),
                mpv_format_MPV_FORMAT_INT64,
                (&mut value as *mut i64).cast::<c_void>(),
            )
        })?;
        Ok(value)
    }

    fn get_property_flag(&self, prop: &str) -> Result<c_int, String> {
        let prop = CString::new(prop).map_err(|err| err.to_string())?;
        let mut value: c_int = 0;
        self.check_error(unsafe {
            mpv_get_property(
                self.ctx,
                prop.as_ptr(),
                mpv_format_MPV_FORMAT_FLAG,
                (&mut value as *mut c_int).cast::<c_void>(),
            )
        })?;
        Ok(value)
    }

    fn check_error(&self, code: i32) -> Result<(), String> {
        if code >= 0 {
            return Ok(());
        }

        let message = unsafe { CStr::from_ptr(mpv_error_string(code)) }
            .to_string_lossy()
            .into_owned();
        Err(message)
    }
}

pub fn create_state() -> Result<MpvState, String> {
    Ok(Arc::new(Mutex::new(MpvPlayer::new()?)))
}
