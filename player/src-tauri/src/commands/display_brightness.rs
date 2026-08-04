use super::player_shared::MpvDisplayBrightnessState;

#[cfg(windows)]
use raw_window_handle::{HasWindowHandle, RawWindowHandle};
#[cfg(windows)]
use windows_sys::Win32::{
    Devices::Display::{
        DestroyPhysicalMonitors, GetMonitorBrightness, GetNumberOfPhysicalMonitorsFromHMONITOR,
        GetPhysicalMonitorsFromHMONITOR, SetMonitorBrightness, PHYSICAL_MONITOR,
    },
    Foundation::{HANDLE, HWND},
    Graphics::Gdi::{MonitorFromWindow, MONITOR_DEFAULTTONEAREST},
};
#[cfg(windows)]
use wmi::{COMLibrary, WMIConnection};

pub async fn state(window: tauri::Window) -> Result<MpvDisplayBrightnessState, String> {
    #[cfg(windows)]
    {
        let hwnd = desktop_window_handle(&window)?;
        return tauri::async_runtime::spawn_blocking(move || windows_display_brightness(hwnd))
            .await
            .map_err(|_| "系统亮度读取任务异常结束。".to_string());
    }

    #[cfg(not(windows))]
    {
        let _ = window;
        Ok(unsupported())
    }
}

pub async fn set(window: tauri::Window, level: f64) -> Result<MpvDisplayBrightnessState, String> {
    if !level.is_finite() || !(0.0..=100.0).contains(&level) {
        return Err("屏幕亮度无效。".to_string());
    }

    #[cfg(windows)]
    {
        let hwnd = desktop_window_handle(&window)?;
        return tauri::async_runtime::spawn_blocking(move || {
            windows_set_display_brightness(hwnd, level)
        })
        .await
        .map_err(|_| "系统亮度设置任务异常结束。".to_string());
    }

    #[cfg(not(windows))]
    {
        let _ = window;
        Ok(unsupported())
    }
}

fn unsupported() -> MpvDisplayBrightnessState {
    MpvDisplayBrightnessState {
        supported: false,
        level: 50.0,
    }
}

#[cfg(windows)]
fn desktop_window_handle(window: &tauri::Window) -> Result<isize, String> {
    let handle = window
        .window_handle()
        .map_err(|_| "无法获取播放器窗口句柄。".to_string())?;
    match handle.as_raw() {
        RawWindowHandle::Win32(handle) => Ok(handle.hwnd.get()),
        _ => Err("当前窗口不支持系统亮度控制。".to_string()),
    }
}

#[cfg(windows)]
fn windows_display_brightness(hwnd: isize) -> MpvDisplayBrightnessState {
    if let Some(level) = ddc_display_brightness(hwnd).or_else(wmi_display_brightness) {
        return MpvDisplayBrightnessState {
            supported: true,
            level,
        };
    }
    unsupported()
}

#[cfg(windows)]
fn windows_set_display_brightness(hwnd: isize, level: f64) -> MpvDisplayBrightnessState {
    if set_ddc_display_brightness(hwnd, level) || set_wmi_display_brightness(level) {
        return MpvDisplayBrightnessState {
            supported: true,
            level,
        };
    }
    unsupported()
}

#[cfg(windows)]
fn ddc_display_brightness(hwnd: isize) -> Option<f64> {
    let monitors = physical_monitors_for_window(hwnd)?;
    for monitor in monitors.handles() {
        let mut minimum = 0;
        let mut current = 0;
        let mut maximum = 0;
        if unsafe { GetMonitorBrightness(monitor, &mut minimum, &mut current, &mut maximum) } != 0
            && maximum > minimum
        {
            return Some(brightness_percent(minimum, current, maximum));
        }
    }
    None
}

#[cfg(windows)]
fn set_ddc_display_brightness(hwnd: isize, level: f64) -> bool {
    let Some(monitors) = physical_monitors_for_window(hwnd) else {
        return false;
    };
    let mut changed = false;
    for monitor in monitors.handles() {
        let mut minimum = 0;
        let mut current = 0;
        let mut maximum = 0;
        if unsafe { GetMonitorBrightness(monitor, &mut minimum, &mut current, &mut maximum) } == 0
            || maximum <= minimum
        {
            continue;
        }
        let target = brightness_value(minimum, maximum, level);
        changed |= unsafe { SetMonitorBrightness(monitor, target) } != 0;
    }
    changed
}

#[cfg(windows)]
struct PhysicalMonitors(Vec<PHYSICAL_MONITOR>);

#[cfg(windows)]
impl PhysicalMonitors {
    fn handles(&self) -> impl Iterator<Item = HANDLE> + '_ {
        self.0.iter().map(|monitor| unsafe {
            std::ptr::read_unaligned(std::ptr::addr_of!(monitor.hPhysicalMonitor))
        })
    }
}

#[cfg(windows)]
impl Drop for PhysicalMonitors {
    fn drop(&mut self) {
        unsafe {
            DestroyPhysicalMonitors(self.0.len() as u32, self.0.as_ptr());
        }
    }
}

#[cfg(windows)]
fn physical_monitors_for_window(hwnd: isize) -> Option<PhysicalMonitors> {
    let logical_monitor = unsafe { MonitorFromWindow(hwnd as HWND, MONITOR_DEFAULTTONEAREST) };
    if logical_monitor.is_null() {
        return None;
    }
    let mut count = 0;
    if unsafe { GetNumberOfPhysicalMonitorsFromHMONITOR(logical_monitor, &mut count) } == 0
        || count == 0
    {
        return None;
    }
    let mut monitors = vec![unsafe { std::mem::zeroed::<PHYSICAL_MONITOR>() }; count as usize];
    if unsafe { GetPhysicalMonitorsFromHMONITOR(logical_monitor, count, monitors.as_mut_ptr()) }
        == 0
    {
        return None;
    }
    Some(PhysicalMonitors(monitors))
}

#[cfg(windows)]
#[derive(serde::Deserialize)]
#[serde(rename = "WmiMonitorBrightness")]
struct WmiMonitorBrightness {
    #[serde(rename = "CurrentBrightness")]
    current_brightness: u8,
    #[serde(rename = "Active")]
    active: bool,
}

#[cfg(windows)]
#[derive(serde::Deserialize)]
#[serde(rename = "WmiMonitorBrightnessMethods")]
struct WmiMonitorBrightnessMethods {
    #[serde(rename = "__Path")]
    path: String,
    #[serde(rename = "Active")]
    active: bool,
}

#[cfg(windows)]
#[derive(serde::Serialize)]
struct WmiSetBrightnessInput {
    #[serde(rename = "Timeout")]
    timeout: u32,
    #[serde(rename = "Brightness")]
    brightness: u8,
}

#[cfg(windows)]
#[derive(serde::Deserialize)]
struct WmiSetBrightnessOutput {
    #[serde(rename = "ReturnValue")]
    return_value: u32,
}

#[cfg(windows)]
fn wmi_display_brightness() -> Option<f64> {
    let connection =
        WMIConnection::with_namespace_path("ROOT\\WMI", COMLibrary::new().ok()?).ok()?;
    connection
        .raw_query::<WmiMonitorBrightness>(
            "SELECT CurrentBrightness, Active FROM WmiMonitorBrightness WHERE Active = TRUE",
        )
        .ok()?
        .into_iter()
        .find(|monitor| monitor.active)
        .map(|monitor| f64::from(monitor.current_brightness))
}

#[cfg(windows)]
fn set_wmi_display_brightness(level: f64) -> bool {
    let Ok(com_library) = COMLibrary::new() else {
        return false;
    };
    let Ok(connection) = WMIConnection::with_namespace_path("ROOT\\WMI", com_library) else {
        return false;
    };
    let Ok(methods) = connection.raw_query::<WmiMonitorBrightnessMethods>(
        "SELECT __Path, Active FROM WmiMonitorBrightnessMethods WHERE Active = TRUE",
    ) else {
        return false;
    };
    let brightness = level.round().clamp(0.0, 100.0) as u8;
    methods
        .into_iter()
        .filter(|method| method.active)
        .any(|method| {
            connection
                .exec_instance_method::<WmiMonitorBrightnessMethods, WmiSetBrightnessOutput>(
                    method.path,
                    "WmiSetBrightness",
                    WmiSetBrightnessInput {
                        timeout: 0,
                        brightness,
                    },
                )
                .is_ok_and(|output| output.return_value == 0)
        })
}

#[cfg(any(windows, test))]
fn brightness_percent(minimum: u32, current: u32, maximum: u32) -> f64 {
    if maximum <= minimum {
        return 50.0;
    }
    (f64::from(current.saturating_sub(minimum)) / f64::from(maximum - minimum) * 100.0)
        .clamp(0.0, 100.0)
}

#[cfg(any(windows, test))]
fn brightness_value(minimum: u32, maximum: u32, level: f64) -> u32 {
    if maximum <= minimum {
        return minimum;
    }
    minimum + (f64::from(maximum - minimum) * level.clamp(0.0, 100.0) / 100.0).round() as u32
}

#[cfg(test)]
mod tests {
    use super::{brightness_percent, brightness_value};

    #[test]
    fn maps_physical_monitor_brightness_ranges() {
        assert_eq!(brightness_percent(10, 55, 100), 50.0);
        assert_eq!(brightness_value(10, 100, 50.0), 55);
        assert_eq!(brightness_value(0, 255, 100.0), 255);
        assert_eq!(brightness_value(0, 255, 0.0), 0);
    }
}
