//! Source color facts and observed renderer targets are separate from physical display output.

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum MpvColorOutputMode {
    Hdr,
    Sdr,
    #[default]
    Unknown,
}

#[derive(Clone, Debug, Default, serde::Serialize, serde::Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct MpvColorDiagnostics {
    pub source_gamma: Option<String>,
    pub source_primaries: Option<String>,
    pub source_pixel_format: Option<String>,
    pub source_hardware_pixel_format: Option<String>,
    pub dolby_vision_profile: Option<u8>,
    pub target_gamma: Option<String>,
    pub target_primaries: Option<String>,
    /// The GPU target format is not proof of physical swapchain/channel bit depth.
    pub target_pixel_format: Option<String>,
    pub output_mode: MpvColorOutputMode,
    pub gpu_context: Option<String>,
    /// None means unavailable; an empty list means a queried display reported no HDR types.
    pub display_hdr_types: Option<Vec<String>>,
    pub reason: Option<String>,
}

#[cfg(not(mobile))]
pub(super) fn color_metadata(value: Option<String>) -> Option<String> {
    let value = value?;
    let value = value.trim();
    // These properties are enum/format tokens, never logs, paths, URLs or native pointers.
    if value.is_empty()
        || value.len() > 64
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"._-".contains(&byte))
    {
        return None;
    }
    let normalized = value.to_ascii_lowercase();
    if matches!(
        normalized.as_str(),
        "unknown" | "auto" | "none" | "null" | "no"
    ) {
        None
    } else {
        Some(value.to_string())
    }
}

#[cfg(not(mobile))]
pub(super) fn transfer_mode(gamma: Option<&str>) -> MpvColorOutputMode {
    match gamma.map(str::to_ascii_lowercase).as_deref() {
        Some("pq" | "st2084" | "smpte2084" | "smpte-st-2084" | "hlg" | "arib-std-b67") => {
            MpvColorOutputMode::Hdr
        }
        Some(
            "bt.1886" | "srgb" | "gamma1.8" | "gamma2.0" | "gamma2.2" | "gamma2.4" | "gamma2.6"
            | "gamma2.8",
        ) => MpvColorOutputMode::Sdr,
        _ => MpvColorOutputMode::Unknown,
    }
}

#[cfg(not(mobile))]
pub(super) fn target_transfer_mode(gamma: Option<&str>) -> MpvColorOutputMode {
    // scRGB is an observed HDR carrier, not a rule for ordinary linear source video/FSR.
    if gamma.is_some_and(|value| value.eq_ignore_ascii_case("scrgb")) {
        MpvColorOutputMode::Hdr
    } else {
        transfer_mode(gamma)
    }
}
