use super::*;

pub(super) async fn remux(
    app: &AppHandle,
    video: &Path,
    audio: Option<&Path>,
    output: &Path,
    control: &Arc<AtomicU8>,
) -> Result<(), String> {
    #[cfg(target_os = "android")]
    {
        use std::ffi::{c_char, c_int, c_void, CString};
        extern "C" {
            fn ohmycine_remux(
                video: *const c_char,
                audio: *const c_char,
                output: *const c_char,
                interrupt: extern "C" fn(*mut c_void) -> c_int,
                opaque: *mut c_void,
            ) -> c_int;
        }
        extern "C" fn interrupted(opaque: *mut c_void) -> c_int {
            // The Arc remains alive throughout the blocking native call.
            unsafe {
                ((*(opaque as *const AtomicU8)).load(Ordering::Acquire) != CONTROL_RUNNING) as c_int
            }
        }
        let video = CString::new(video.to_string_lossy().as_bytes())
            .map_err(|_| "Invalid remux input.".to_string())?;
        let audio = audio
            .map(|p| CString::new(p.to_string_lossy().as_bytes()))
            .transpose()
            .map_err(|_| "Invalid remux input.".to_string())?;
        let output = CString::new(output.to_string_lossy().as_bytes())
            .map_err(|_| "Invalid remux output.".to_string())?;
        let control = control.clone();
        let _ = app;
        let code = tauri::async_runtime::spawn_blocking(move || unsafe {
            ohmycine_remux(
                video.as_ptr(),
                audio
                    .as_ref()
                    .map(|v| v.as_ptr())
                    .unwrap_or(std::ptr::null()),
                output.as_ptr(),
                interrupted,
                Arc::as_ptr(&control) as *mut c_void,
            )
        })
        .await
        .map_err(|_| "The local remux worker failed.".to_string())?;
        if code != 0 {
            return Err("The local FFmpeg remux failed or its ABI is incompatible.".to_string());
        }
        Ok(())
    }
    #[cfg(not(target_os = "android"))]
    {
        let executable = executable(app)?;
        remux_executable(&executable, video, audio, output, control).await
    }
}

#[cfg(not(target_os = "android"))]
async fn remux_executable(
    executable: &Path,
    video: &Path,
    audio: Option<&Path>,
    output: &Path,
    control: &AtomicU8,
) -> Result<(), String> {
    use std::process::{Command, Stdio};
    let mut command = Command::new(executable);
    if let Some(parent) = output.parent() {
        command.current_dir(parent);
    }
    command
        .args([
            "-hide_banner",
            "-nostdin",
            "-loglevel",
            "error",
            "-y",
            "-protocol_whitelist",
            "file",
            "-format_whitelist",
            "mov,mpegts,matroska",
            "-enable_drefs",
            "0",
            "-i",
        ])
        .arg(video);
    if let Some(audio) = audio {
        command
            .args([
                "-protocol_whitelist",
                "file",
                "-format_whitelist",
                "mov,mpegts,matroska",
                "-enable_drefs",
                "0",
                "-i",
            ])
            .arg(audio)
            .args(["-map", "0:v:0", "-map", "1:a:0"]);
    } else {
        command.args(["-map", "0:v:0", "-map", "0:a:0?"]);
    }
    command.args(["-c", "copy", "-map_metadata", "-1"]);
    if output.extension().and_then(|v| v.to_str()) == Some("mp4") {
        command.args(["-movflags", "+faststart", "-f", "mp4"]);
    } else {
        command.args(["-f", "matroska"]);
    }
    command
        .arg(output)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(0x08000000); // CREATE_NO_WINDOW
    }
    let mut child = command
        .spawn()
        .map_err(|_| "The local FFmpeg runtime could not be started.".to_string())?;
    let started = Instant::now();
    loop {
        if control.load(Ordering::Acquire) != CONTROL_RUNNING
            || started.elapsed() > Duration::from_secs(7200)
        {
            let _ = child.kill();
            let _ = child.wait();
            return Err("The local remux was stopped.".to_string());
        }
        match child.try_wait() {
            Ok(Some(status)) if status.success() => return Ok(()),
            Ok(Some(_)) => return Err("The local FFmpeg remux failed.".to_string()),
            Ok(None) => tokio::time::sleep(Duration::from_millis(100)).await,
            Err(_) => {
                let _ = child.kill();
                let _ = child.wait();
                return Err("The local remux worker failed.".to_string());
            }
        }
    }
}

#[cfg(not(target_os = "android"))]
fn executable(app: &AppHandle) -> Result<PathBuf, String> {
    #[cfg(windows)]
    {
        let root = app
            .path()
            .resource_dir()
            .map_err(|_| "The bundled remux runtime is unavailable.".to_string())?
            .join("remux");
        #[cfg(debug_assertions)]
        let root = if root.exists() {
            root
        } else {
            Path::new(env!("CARGO_MANIFEST_DIR")).join("resources/remux")
        };
        // The embedded list belongs to the checksum-verified release archive, not the filesystem being verified.
        verify_windows_runtime(&root)?;
        Ok(root.join("ffmpeg.exe"))
    }
    #[cfg(not(windows))]
    {
        let _ = app;
        Err("A bundled offline remux runtime is unavailable on this platform.".to_string())
    }
}

#[cfg(windows)]
fn verify_windows_runtime(root: &Path) -> Result<(), String> {
    let metadata = fs::symlink_metadata(root)
        .map_err(|_| "Run setup:remux before building this Player.".to_string())?;
    if !metadata.is_dir() || metadata_is_link_or_reparse(&metadata) {
        return Err("The remux runtime directory is unsafe.".to_string());
    }
    for line in include_str!("../../native/remux-windows.sha256").lines() {
        let (expected, name) = line
            .split_once("  ")
            .ok_or_else(|| "The remux runtime manifest is invalid.".to_string())?;
        if Path::new(name).components().count() != 1 {
            return Err("The remux runtime manifest is invalid.".to_string());
        }
        let path = root.join(name);
        let metadata = fs::symlink_metadata(&path)
            .map_err(|_| "The bundled remux runtime is incomplete.".to_string())?;
        if !metadata.is_file()
            || metadata_is_link_or_reparse(&metadata)
            || format!(
                "{:x}",
                Sha256::digest(
                    fs::read(path)
                        .map_err(|_| "The bundled remux runtime is unreadable.".to_string())?
                )
            ) != expected
        {
            return Err("The bundled remux runtime checksum is invalid.".to_string());
        }
    }
    Ok(())
}

#[cfg(all(test, windows))]
mod tests {
    use super::*;
    use std::process::{Command, Stdio};
    #[tokio::test]
    async fn actual_ffmpeg_remuxes_dash_pair_and_hls_init_segments() {
        let runtime = Path::new(env!("CARGO_MANIFEST_DIR")).join("resources/remux");
        verify_windows_runtime(&runtime).unwrap();
        let executable = runtime.join("ffmpeg.exe");
        let root = std::env::temp_dir().join(format!("ohmycine-remux-test-{}", random_id()));
        fs::create_dir(&root).unwrap();
        let run = |args: &[&str], output: &Path| {
            let mut child = Command::new(&executable)
                .current_dir(&root)
                .args(["-nostdin", "-hide_banner", "-loglevel", "error", "-y"])
                .args(args)
                .arg(output)
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .spawn()
                .unwrap();
            let started = Instant::now();
            loop {
                if let Some(status) = child.try_wait().unwrap() {
                    assert!(status.success());
                    break;
                }
                if started.elapsed() > Duration::from_secs(30) {
                    let _ = child.kill();
                    let _ = child.wait();
                    panic!("FFmpeg fixture generation timed out");
                }
                std::thread::sleep(Duration::from_millis(20));
            }
        };
        let video = root.join("video.mp4");
        let audio = root.join("audio.m4a");
        run(
            &[
                "-f",
                "lavfi",
                "-i",
                "testsrc=size=64x64:rate=10",
                "-t",
                "1",
                "-an",
                "-c:v",
                "mpeg4",
            ],
            &video,
        );
        run(
            &[
                "-f",
                "lavfi",
                "-i",
                "sine=frequency=440",
                "-t",
                "1",
                "-vn",
                "-c:a",
                "aac",
            ],
            &audio,
        );
        let output = root.join("dash.mp4");
        remux_executable(
            &executable,
            &video,
            Some(&audio),
            &output,
            &AtomicU8::new(CONTROL_RUNNING),
        )
        .await
        .unwrap();
        assert!(fs::metadata(&output).unwrap().len() > 0);
        assert!(Command::new(&executable)
            .args(["-nostdin", "-loglevel", "error", "-i"])
            .arg(&output)
            .args(["-map", "0:v:0", "-map", "0:a:0", "-c", "copy", "-f", "null", "-"])
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status()
            .unwrap()
            .success());
        let playlist = root.join("vod.m3u8");
        run(
            &[
                "-f",
                "lavfi",
                "-i",
                "testsrc=size=64x64:rate=10",
                "-t",
                "1",
                "-an",
                "-c:v",
                "mpeg4",
                "-g",
                "2",
                "-hls_time",
                "0.2",
                "-hls_playlist_type",
                "vod",
                "-hls_segment_type",
                "fmp4",
                "-f",
                "hls",
            ],
            &playlist,
        );
        let mut combined = fs::read(root.join("init.mp4")).unwrap();
        let playlist_text = fs::read_to_string(&playlist).unwrap();
        for name in playlist_text
            .lines()
            .filter(|line| !line.starts_with('#') && !line.is_empty())
        {
            combined.extend_from_slice(&fs::read(root.join(name)).unwrap());
        }
        let track = root.join("video.track");
        fs::write(&track, combined).unwrap();
        let hls = root.join("hls.mp4");
        remux_executable(
            &executable,
            &track,
            None,
            &hls,
            &AtomicU8::new(CONTROL_RUNNING),
        )
        .await
        .unwrap();
        assert!(fs::metadata(hls).unwrap().len() > 0);
        let stopped = root.join("stopped.mp4");
        assert!(remux_executable(
            &executable,
            &track,
            None,
            &stopped,
            &AtomicU8::new(CONTROL_CANCEL)
        )
        .await
        .is_err());
        let malicious = root.join("malicious.track");
        fs::write(
            &malicious,
            format!(
                "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\n{}\n#EXT-X-ENDLIST\n",
                video.display()
            ),
        )
        .unwrap();
        assert!(remux_executable(
            &executable,
            &malicious,
            None,
            &root.join("bad.mp4"),
            &AtomicU8::new(CONTROL_RUNNING)
        )
        .await
        .is_err());
        assert!(video.exists());
        for entry in fs::read_dir(&root).unwrap() {
            fs::remove_file(entry.unwrap().path()).unwrap();
        }
        fs::remove_dir(root).unwrap();
    }
}
