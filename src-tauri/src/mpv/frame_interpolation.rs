//! Platform-neutral frame-generation lifecycle, pacing, quality hysteresis and safety gates.
//!
//! Native backends may only reveal generated output after `backend_first_frame` accepts a frame
//! from the current media generation. Every seek/reconfigure/surface loss invalidates old frames.

// The complete controller API is shared by the Windows and Android backends. Some lifecycle and
// pacing hooks remain intentionally dormant while the product capability gate is closed; keeping
// them compiled lets the contract tests cover those transitions without exposing the feature.
#![allow(dead_code)]

use std::collections::VecDeque;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EffectiveState {
    Disabled,
    Probing,
    Active,
    TemporaryBypass,
    UnavailableNoHwdec,
    UnavailableHdrPath,
    UnavailableGraphicSubtitle,
    FallbackPerformance,
    BackendError,
    BackendUnavailable,
}

impl EffectiveState {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Disabled => "disabled",
            Self::Probing => "probing",
            Self::Active => "active",
            Self::TemporaryBypass => "temporary-bypass",
            Self::UnavailableNoHwdec => "unavailable-no-hwdec",
            Self::UnavailableHdrPath => "unavailable-hdr-path",
            Self::UnavailableGraphicSubtitle => "unavailable-graphic-subtitle",
            Self::FallbackPerformance => "fallback-performance",
            Self::BackendError => "backend-error",
            Self::BackendUnavailable => "backend-unavailable",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MediaEvent {
    StartFile,
    FileLoaded,
    VideoReconfig,
    Seek,
    TrackSwitch,
    SurfaceLost,
    SurfaceReady,
    EndFile,
}

#[derive(Debug, Clone)]
pub struct FrameGenerationController {
    requested: bool,
    state: EffectiveState,
    reason: Option<String>,
    generation: u64,
    file_loaded: bool,
    surface_ready: bool,
    hardware_decode_ready: bool,
    hdr_path_ready: bool,
    backend_available: bool,
    graphic_subtitle_active: bool,
    backend_first_frame_ready: bool,
    dropped_frames: u64,
    performance: PerformanceGovernor,
}

impl Default for FrameGenerationController {
    fn default() -> Self {
        Self {
            requested: false,
            state: EffectiveState::Disabled,
            reason: None,
            generation: 0,
            file_loaded: false,
            surface_ready: false,
            hardware_decode_ready: false,
            hdr_path_ready: false,
            backend_available: false,
            graphic_subtitle_active: false,
            backend_first_frame_ready: false,
            dropped_frames: 0,
            performance: PerformanceGovernor::default(),
        }
    }
}

impl FrameGenerationController {
    pub fn set_requested(&mut self, requested: bool) {
        self.requested = requested;
        if !requested {
            self.backend_first_frame_ready = false;
            self.performance.reset();
        }
        self.reevaluate();
    }

    pub fn set_gates(
        &mut self,
        hardware_decode_ready: bool,
        hdr_path_ready: bool,
        backend_available: bool,
    ) {
        self.hardware_decode_ready = hardware_decode_ready;
        self.hdr_path_ready = hdr_path_ready;
        self.backend_available = backend_available;
        if !backend_available {
            self.backend_first_frame_ready = false;
        }
        self.reevaluate();
    }

    pub fn set_graphic_subtitle_active(&mut self, active: bool) {
        self.graphic_subtitle_active = active;
        if active {
            self.backend_first_frame_ready = false;
        }
        self.reevaluate();
    }

    pub fn on_media_event(&mut self, event: MediaEvent) {
        match event {
            MediaEvent::StartFile => {
                self.bump_generation();
                self.file_loaded = false;
                self.state = if self.requested {
                    EffectiveState::TemporaryBypass
                } else {
                    EffectiveState::Disabled
                };
                self.reason = self
                    .requested
                    .then(|| "正在切换媒体，已清空旧帧。".to_string());
            }
            MediaEvent::FileLoaded => {
                self.file_loaded = true;
                self.reevaluate();
            }
            MediaEvent::VideoReconfig | MediaEvent::Seek | MediaEvent::TrackSwitch => {
                self.bump_generation();
                self.state = if self.requested {
                    EffectiveState::TemporaryBypass
                } else {
                    EffectiveState::Disabled
                };
                self.reason = self
                    .requested
                    .then(|| "视频时序已变化，正在等待新一代真实帧。".to_string());
            }
            MediaEvent::SurfaceLost => {
                self.bump_generation();
                self.surface_ready = false;
                self.state = if self.requested {
                    EffectiveState::TemporaryBypass
                } else {
                    EffectiveState::Disabled
                };
                self.reason = self
                    .requested
                    .then(|| "输出表面已重建，暂时旁路插帧。".to_string());
            }
            MediaEvent::SurfaceReady => {
                self.surface_ready = true;
                self.reevaluate();
            }
            MediaEvent::EndFile => {
                self.bump_generation();
                self.file_loaded = false;
                self.state = if self.requested {
                    EffectiveState::TemporaryBypass
                } else {
                    EffectiveState::Disabled
                };
                self.reason = None;
            }
        }
    }

    /// Returns true only when the completed frame belongs to the current media generation.
    pub fn backend_first_frame(&mut self, generation: u64) -> bool {
        if generation != self.generation || !self.all_gates_ready() {
            return false;
        }
        self.backend_first_frame_ready = true;
        self.reevaluate();
        self.state == EffectiveState::Active
    }

    pub fn backend_failed(&mut self, reason: impl Into<String>) {
        self.backend_first_frame_ready = false;
        self.state = EffectiveState::BackendError;
        self.reason = Some(reason.into());
    }

    pub fn backend_stalled(&mut self, reason: impl Into<String>) {
        self.backend_first_frame_ready = false;
        self.state = EffectiveState::FallbackPerformance;
        self.reason = Some(reason.into());
    }

    pub fn record_model_time(&mut self, milliseconds: f64, target_fps: u16) {
        match self.performance.record(milliseconds, target_fps) {
            PerformanceDecision::Keep => {}
            PerformanceDecision::Lowered => {
                self.state = EffectiveState::FallbackPerformance;
                self.reason = Some("模型耗时超过帧预算，已降低 Flow Scale。".to_string());
            }
            PerformanceDecision::Recovered => self.reevaluate(),
        }
    }

    pub fn record_drop(&mut self) {
        self.dropped_frames = self.dropped_frames.saturating_add(1);
    }

    pub fn record_drops(&mut self, count: u64) {
        self.dropped_frames = self.dropped_frames.saturating_add(count);
    }

    pub fn generation(&self) -> u64 {
        self.generation
    }

    pub fn state(&self) -> EffectiveState {
        self.state
    }

    pub fn reason(&self) -> Option<&str> {
        self.reason.as_deref()
    }

    pub fn flow_scale(&self) -> f64 {
        self.performance.flow_scale()
    }

    pub fn model_time_percentiles(&self) -> (Option<f64>, Option<f64>) {
        self.performance.percentiles()
    }

    pub fn dropped_frames(&self) -> u64 {
        self.dropped_frames
    }

    fn bump_generation(&mut self) {
        self.generation = self.generation.wrapping_add(1);
        self.backend_first_frame_ready = false;
        self.performance.reset_samples();
    }

    fn all_gates_ready(&self) -> bool {
        self.requested
            && self.file_loaded
            && self.surface_ready
            && self.hardware_decode_ready
            && self.hdr_path_ready
            && self.backend_available
            && !self.graphic_subtitle_active
    }

    fn reevaluate(&mut self) {
        let (state, reason) = if !self.requested {
            (EffectiveState::Disabled, None)
        } else if !self.hardware_decode_ready {
            (
                EffectiveState::UnavailableNoHwdec,
                Some("当前媒体未使用硬件解码，视频插帧已自动关闭。".to_string()),
            )
        } else if !self.hdr_path_ready {
            (
                EffectiveState::UnavailableHdrPath,
                Some("当前显示链无法保持 FP16 HDR，视频插帧已自动关闭。".to_string()),
            )
        } else if !self.backend_available {
            (
                EffectiveState::BackendUnavailable,
                Some("视频插帧原生后端尚未通过自检。".to_string()),
            )
        } else if self.graphic_subtitle_active {
            (
                EffectiveState::UnavailableGraphicSubtitle,
                Some("当前图形字幕需要在视频层合成，已自动旁路插帧。".to_string()),
            )
        } else if !self.file_loaded || !self.surface_ready || !self.backend_first_frame_ready {
            (
                EffectiveState::Probing,
                Some("正在准备 GPU 插帧管线。".to_string()),
            )
        } else {
            (EffectiveState::Active, None)
        };
        self.state = state;
        self.reason = reason;
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum PerformanceDecision {
    Keep,
    Lowered,
    Recovered,
}

#[derive(Debug, Clone)]
struct PerformanceGovernor {
    samples: VecDeque<f64>,
    scale_index: usize,
    recovery_samples: usize,
}

impl Default for PerformanceGovernor {
    fn default() -> Self {
        Self {
            samples: VecDeque::with_capacity(120),
            scale_index: 0,
            recovery_samples: 0,
        }
    }
}

impl PerformanceGovernor {
    const SCALES: [f64; 3] = [1.0, 0.67, 0.5];

    fn record(&mut self, milliseconds: f64, target_fps: u16) -> PerformanceDecision {
        if !milliseconds.is_finite() || milliseconds < 0.0 || target_fps == 0 {
            return PerformanceDecision::Keep;
        }
        if self.samples.len() == 120 {
            self.samples.pop_front();
        }
        self.samples.push_back(milliseconds);
        if self.samples.len() < 30 {
            return PerformanceDecision::Keep;
        }
        let budget = 1000.0 / f64::from(target_fps);
        let (_, p95) = self.percentiles();
        let p95 = p95.unwrap_or_default();
        if p95 > budget * 0.8 && self.scale_index + 1 < Self::SCALES.len() {
            self.scale_index += 1;
            self.recovery_samples = 0;
            self.samples.clear();
            return PerformanceDecision::Lowered;
        }
        if self.scale_index > 0 && p95 < budget * 0.55 {
            self.recovery_samples += 1;
            if self.recovery_samples >= 300 {
                self.scale_index -= 1;
                self.recovery_samples = 0;
                self.samples.clear();
                return PerformanceDecision::Recovered;
            }
        } else {
            self.recovery_samples = 0;
        }
        PerformanceDecision::Keep
    }

    fn flow_scale(&self) -> f64 {
        Self::SCALES[self.scale_index]
    }

    fn percentiles(&self) -> (Option<f64>, Option<f64>) {
        if self.samples.is_empty() {
            return (None, None);
        }
        let mut values = self.samples.iter().copied().collect::<Vec<_>>();
        values.sort_by(f64::total_cmp);
        let percentile = |value: f64| {
            let index = ((values.len() - 1) as f64 * value).round() as usize;
            values[index]
        };
        (Some(percentile(0.50)), Some(percentile(0.95)))
    }

    fn reset_samples(&mut self) {
        self.samples.clear();
        self.recovery_samples = 0;
    }

    fn reset(&mut self) {
        self.reset_samples();
        self.scale_index = 0;
    }
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct ScheduledFrame {
    pub presentation_ns: i64,
    pub timestep: f32,
}

/// Exact rational output grid used for CFR and VFR sources. Input PTS values must be monotonic.
#[derive(Debug, Clone)]
pub struct CadenceScheduler {
    target_numerator: u32,
    target_denominator: u32,
    next_tick: Option<i128>,
}

impl CadenceScheduler {
    pub fn new(target_numerator: u32, target_denominator: u32) -> Option<Self> {
        (target_numerator > 0 && target_denominator > 0).then_some(Self {
            target_numerator,
            target_denominator,
            next_tick: None,
        })
    }

    pub fn reset(&mut self) {
        self.next_tick = None;
    }

    pub fn schedule_between(&mut self, earlier_ns: i64, later_ns: i64) -> Vec<ScheduledFrame> {
        if later_ns <= earlier_ns {
            self.reset();
            return Vec::new();
        }
        let period_numerator = 1_000_000_000_i128 * i128::from(self.target_denominator);
        let rate = i128::from(self.target_numerator);
        let first_tick_after =
            |timestamp: i64| (i128::from(timestamp) * rate).div_euclid(period_numerator) + 1;
        let mut tick = self
            .next_tick
            .unwrap_or_else(|| first_tick_after(earlier_ns))
            .max(first_tick_after(earlier_ns));
        let mut scheduled = Vec::new();
        loop {
            let presentation = tick * period_numerator / rate;
            if presentation >= i128::from(later_ns) {
                break;
            }
            let presentation_ns = presentation as i64;
            let timestep = (presentation_ns - earlier_ns) as f64 / (later_ns - earlier_ns) as f64;
            scheduled.push(ScheduledFrame {
                presentation_ns,
                timestep: timestep.clamp(0.0, 1.0) as f32,
            });
            tick += 1;
        }
        self.next_tick = Some(tick);
        scheduled
    }
}

pub fn should_synthesize(luma_sad: f32, confidence: f32, pts_gap_ms: f64) -> bool {
    luma_sad.is_finite()
        && confidence.is_finite()
        && pts_gap_ms.is_finite()
        && luma_sad <= 0.32
        && confidence >= 0.20
        && (0.0..=250.0).contains(&pts_gap_ms)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn never_activates_before_current_generation_first_frame() {
        let mut controller = FrameGenerationController::default();
        controller.set_requested(true);
        controller.on_media_event(MediaEvent::SurfaceReady);
        controller.set_gates(true, true, true);
        controller.on_media_event(MediaEvent::FileLoaded);
        let stale_generation = controller.generation();
        controller.on_media_event(MediaEvent::Seek);
        assert_eq!(controller.state(), EffectiveState::TemporaryBypass);
        assert!(!controller.backend_first_frame(stale_generation));
        assert_ne!(controller.state(), EffectiveState::Active);
        assert!(controller.backend_first_frame(controller.generation()));
        assert_eq!(controller.state(), EffectiveState::Active);
    }

    #[test]
    fn hardware_decode_and_hdr_are_hard_gates() {
        let mut controller = FrameGenerationController::default();
        controller.set_requested(true);
        controller.on_media_event(MediaEvent::FileLoaded);
        controller.on_media_event(MediaEvent::SurfaceReady);
        controller.set_gates(false, true, true);
        assert_eq!(controller.state(), EffectiveState::UnavailableNoHwdec);
        controller.set_gates(true, false, true);
        assert_eq!(controller.state(), EffectiveState::UnavailableHdrPath);
    }

    #[test]
    fn rational_scheduler_handles_24_to_60_without_three_two_repeats() {
        let mut scheduler = CadenceScheduler::new(60, 1).unwrap();
        let generated = scheduler.schedule_between(0, 41_666_667);
        assert_eq!(generated.len(), 2);
        assert!((generated[0].timestep - 0.4).abs() < 0.01);
        assert!((generated[1].timestep - 0.8).abs() < 0.01);
    }

    #[test]
    fn scene_cut_and_bad_timestamps_bypass_synthesis() {
        assert!(should_synthesize(0.1, 0.8, 41.0));
        assert!(!should_synthesize(0.5, 0.8, 41.0));
        assert!(!should_synthesize(0.1, 0.1, 41.0));
        assert!(!should_synthesize(0.1, 0.8, 500.0));
    }

    #[test]
    fn performance_governor_degrades_after_sustained_over_budget() {
        let mut controller = FrameGenerationController::default();
        for _ in 0..30 {
            controller.record_model_time(15.0, 60);
        }
        assert_eq!(controller.flow_scale(), 0.67);
        assert_eq!(controller.state(), EffectiveState::FallbackPerformance);
    }

    #[test]
    fn stalled_native_cadence_revokes_active_and_accounts_for_all_drops() {
        let mut controller = FrameGenerationController::default();
        controller.set_requested(true);
        controller.on_media_event(MediaEvent::SurfaceReady);
        controller.on_media_event(MediaEvent::FileLoaded);
        controller.set_gates(true, true, true);
        assert!(controller.backend_first_frame(controller.generation()));
        controller.record_drops(7);
        controller.backend_stalled("native present watchdog");
        assert_eq!(controller.state(), EffectiveState::FallbackPerformance);
        assert_eq!(controller.dropped_frames(), 7);
        assert_eq!(controller.reason(), Some("native present watchdog"));
    }

    #[test]
    fn graphic_subtitles_are_a_hard_gate() {
        let mut controller = FrameGenerationController::default();
        controller.set_requested(true);
        controller.on_media_event(MediaEvent::SurfaceReady);
        controller.on_media_event(MediaEvent::FileLoaded);
        controller.set_gates(true, true, true);
        controller.set_graphic_subtitle_active(true);
        assert!(!controller.backend_first_frame(controller.generation()));
        assert_eq!(
            controller.state(),
            EffectiveState::UnavailableGraphicSubtitle
        );
    }
}
