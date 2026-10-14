#!/usr/bin/env python3
"""Build the complete pinned Android JNI runtime in an isolated Linux workspace."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import urllib.request
import zipfile

BUILDER = Path(__file__).resolve().parent
MANIFEST = json.loads((BUILDER / "sources.json").read_text())

CHECKED_JNI = r'''
// OhMyCine: return native status so HDR negotiation cannot report an ignored failure.
#include <string.h>
extern "C" {
jni_func(jint, setPropertyStringChecked, jstring jproperty, jstring jvalue) {
    CHECK_MPV_INIT();
    const char *property = env->GetStringUTFChars(jproperty, NULL);
    const char *value = env->GetStringUTFChars(jvalue, NULL);
    int result = mpv_set_property_string(g_mpv, property, value);
    env->ReleaseStringUTFChars(jproperty, property);
    env->ReleaseStringUTFChars(jvalue, value);
    return result;
}
jni_func(jint, commandChecked, jobjectArray jarray) {
    CHECK_MPV_INIT();
    const char *arguments[128] = {0};
    int len = env->GetArrayLength(jarray);
    if (len >= 128) return MPV_ERROR_INVALID_PARAMETER;
    for (int i = 0; i < len; ++i)
        arguments[i] = env->GetStringUTFChars((jstring)env->GetObjectArrayElement(jarray, i), NULL);
    int result = mpv_command(g_mpv, arguments);
    for (int i = 0; i < len; ++i)
        env->ReleaseStringUTFChars((jstring)env->GetObjectArrayElement(jarray, i), arguments[i]);
    return result;
}
jni_func(jlong, loadFileChecked, jobjectArray jarray) {
    CHECK_MPV_INIT();
    const int len = env->GetArrayLength(jarray);
    if (len < 2 || len >= 128) return MPV_ERROR_INVALID_PARAMETER;
    jstring strings[128] = {0};
    const char *arguments[128] = {0};
    int status = 0;
    for (int i = 0; i < len; ++i) {
        strings[i] = (jstring)env->GetObjectArrayElement(jarray, i);
        if (!strings[i] || !(arguments[i] = env->GetStringUTFChars(strings[i], NULL))) {
            status = MPV_ERROR_INVALID_PARAMETER;
            break;
        }
    }
    mpv_node result = {};
    if (status >= 0) {
        status = strcmp(arguments[0], "loadfile") == 0
            ? mpv_command_ret(g_mpv, arguments, &result) : MPV_ERROR_INVALID_PARAMETER;
    }
    for (int i = 0; i < len; ++i) {
        if (arguments[i]) env->ReleaseStringUTFChars(strings[i], arguments[i]);
        if (strings[i]) env->DeleteLocalRef(strings[i]);
    }
    int64_t entry_id = MPV_ERROR_GENERIC;
    if (status >= 0 && result.format == MPV_FORMAT_NODE_MAP && result.u.list) {
        const mpv_node_list *list = result.u.list;
        for (int i = 0; i < list->num; ++i) {
            if (strcmp(list->keys[i], "playlist_entry_id") == 0
                && list->values[i].format == MPV_FORMAT_INT64) {
                entry_id = list->values[i].u.int64;
                break;
            }
        }
    }
    mpv_free_node_contents(&result);
    if (status < 0) return status;
    if (entry_id <= 0) {
        ALOGE("OHMYCINE_MPV_ENTRY_ID_V1 loadfile returned no valid playlist entry identity");
        return MPV_ERROR_GENERIC;
    }
    return (jlong)entry_id;
}
jni_func(jint, addLoadHook) {
    CHECK_MPV_INIT();
    return mpv_hook_add(g_mpv, 1, "on_load", 0);
}
jni_func(jint, continueHook, jlong id) {
    CHECK_MPV_INIT();
    return mpv_hook_continue(g_mpv, (uint64_t)id);
}
jni_func(jint, stopAsync) {
    CHECK_MPV_INIT();
    const char *arguments[] = {"stop", NULL};
    return mpv_command_async(g_mpv, 0, arguments);
}
}
'''

LOAD_HOOK_EVENT = '''        case MPV_EVENT_HOOK: {
            mpv_event_hook *hook = (mpv_event_hook*)mp_event->data;
            uint64_t id = hook->id;
            bool failed = mpv_set_property_string(g_mpv, "glsl-shaders", "") < 0;
            jmethodID callback = env->GetStaticMethodID(mpv_MPVLib, "eventHook", "(J)V");
            if (callback) env->CallStaticVoidMethod(mpv_MPVLib, callback, (jlong)hook->id);
            if (!callback || env->ExceptionCheck()) {
                env->ExceptionClear();
                failed = true;
            }
            // Native owns continuation exactly once, including an empty observer list.
            if (failed) {
                mpv_set_property_string(g_mpv, "vo", "null");
                const char *stop[] = {"stop", NULL};
                mpv_command_async(g_mpv, 0, stop);
            }
            if (mpv_hook_continue(g_mpv, id) < 0) {
                mpv_set_property_string(g_mpv, "vo", "null");
                const char *stop[] = {"stop", NULL};
                mpv_command_async(g_mpv, 0, stop);
            }
            break;
        }
'''

END_FILE_EVENT = '''        case MPV_EVENT_END_FILE: {
            // Copy native terminal facts before any Java callback can issue commands.
            const mpv_event_end_file *end = (mpv_event_end_file*)mp_event->data;
            const int reason = end ? end->reason : MPV_END_FILE_REASON_ERROR;
            const int error = end ? end->error : MPV_ERROR_GENERIC;
            const int64_t entry_id = end ? end->playlist_entry_id : -1;
            const int64_t insert_id = end ? end->playlist_insert_id : 0;
            const int insert_count = end ? end->playlist_insert_num_entries : 0;
            jmethodID callback = env->GetStaticMethodID(mpv_MPVLib, "eventEndFile", "(IIJJI)V");
            if (callback) env->CallStaticVoidMethod(mpv_MPVLib, callback, (jint)reason, (jint)error,
                (jlong)entry_id, (jlong)insert_id, (jint)insert_count);
            break;
        }
'''

START_FILE_EVENT = '''        case MPV_EVENT_START_FILE: {
            const mpv_event_start_file *start = (mpv_event_start_file*)mp_event->data;
            const int64_t entry_id = start ? start->playlist_entry_id : -1;
            jmethodID callback = env->GetStaticMethodID(mpv_MPVLib, "eventStartFile", "(J)V");
            if (callback) env->CallStaticVoidMethod(mpv_MPVLib, callback, (jlong)entry_id);
            break;
        }
'''


def run(args, cwd=None, env=None):
    print("+", " ".join(map(str, args)), flush=True)
    return subprocess.run(list(map(str, args)), cwd=cwd, env=env, check=True)


def download(spec, dest, algorithm="sha256"):
    key = spec[algorithm]
    if not dest.exists() or hashlib.new(algorithm, dest.read_bytes()).hexdigest() != key:
        print("Downloading", dest.name, flush=True)
        with urllib.request.urlopen(spec["url"], timeout=120) as response, dest.open("wb") as output:
            shutil.copyfileobj(response, output)
    if hashlib.new(algorithm, dest.read_bytes()).hexdigest() != key:
        raise RuntimeError("Source checksum mismatch: " + dest.name)


def checkout(spec, dest):
    if not (dest / ".git").exists():
        dest.mkdir(parents=True, exist_ok=True)
        run(["git", "init", dest])
        run(["git", "remote", "add", "origin", spec["url"]], dest)
    head = subprocess.run(["git", "rev-parse", "--verify", "HEAD"], cwd=dest, text=True, capture_output=True)
    actual = head.stdout.strip() if head.returncode == 0 else None
    if actual is None:
        # Resume a fetch interrupted before the first checkout without discarding it.
        run(["git", "fetch", "--depth=1", "origin", spec["commit"]], dest)
        run(["git", "checkout", "--detach", "FETCH_HEAD"], dest)
    elif actual != spec["commit"]:
        raise RuntimeError("Source commit mismatch: " + str(dest))
    if spec.get("recursive"):
        run(["git", "submodule", "update", "--init", "--recursive", "--depth=1"], dest)


def replace(path, old, new):
    data = path.read_text()
    if new in data:
        return
    if old not in data:
        raise RuntimeError("Pinned patch context changed: " + str(path))
    path.write_text(data.replace(old, new))


def reconfigure_meson(path):
    # Re-resolve static dependency flags as well as changed build options.
    replace(path, "meson setup $build --cross-file", '''meson_reconfigure=()
if [ -f "$build/meson-private/coredata.dat" ]; then
    meson_reconfigure+=(--reconfigure)
fi
meson setup "${meson_reconfigure[@]}" $build --cross-file''')


def install_hdr_egl_source(mpv):
    relative = "video/out/opengl/context_android.c"
    path = mpv / relative
    original = subprocess.check_output(["git", "show", "HEAD:" + relative],
                                       cwd=mpv, text=True)
    current = path.read_text()
    if current != original and "OHMYCINE_ANDROID_HDR_EGL_V1" not in current:
        raise RuntimeError("Unexpected Android EGL source changes: " + str(path))
    path.write_text((BUILDER / "android-hdr-egl.c").read_text())


def export_licenses(output, upstream, scripts, sdk):
    licenses = output / "licenses"
    if licenses.is_symlink():
        raise RuntimeError("Generated license directory must not be a symlink")
    licenses.mkdir(exist_ok=True)
    # Only flat files generated by this exporter are replaced, never source trees.
    for path in licenses.iterdir():
        if not path.is_file() and not path.is_symlink():
            raise RuntimeError("Unexpected entry in generated license directory")
        path.unlink()
    origins = {}

    def retain(component, path, relative):
        identity = component + "/" + relative.as_posix()
        digest = hashlib.sha256(identity.encode()).hexdigest()[:16]
        component_label = re.sub(r"[^A-Za-z0-9_.-]", "-", component)[:20]
        file_label = re.sub(r"[^A-Za-z0-9_.-]", "-", path.stem)[:24]
        extension = ".html" if path.suffix.lower() == ".html" else ".txt"
        target = component_label + "-" + file_label + "-" + digest + extension
        shutil.copyfile(path, licenses / target)
        origins[target] = {"component": component, "path": relative.as_posix()}

    excluded = {".git", ".cache", ".build", "build", "sdk", "output", "downloads", "cache"}
    for name in [*MANIFEST["sources"], "lua"]:
        source = upstream if name == "mpv-android" else scripts / "deps" / name
        for directory, children, files in os.walk(source):
            # mpv-android's dependency/SDK trees are handled by their own fixed
            # sources and the stdlib notice below. Do not even walk build tools.
            children[:] = [child for child in children if child not in excluded
                           and not child.startswith("_build")
                           and not (name == "mpv-android" and child == "buildscripts")]
            for filename in files:
                path = Path(directory) / filename
                relative = path.relative_to(source)
                legal_text = filename.lower().startswith(("license", "licence", "copying", "copyright", "notice"))
                legal_text = legal_text or filename.lower() in {"ftl.txt", "gplv2.txt"}
                legal_text = legal_text or any(part.lower() == "licenses" for part in relative.parts[:-1])
                legal_text = legal_text or (name == "lua" and relative.as_posix() == "doc/readme.html")
                if path.is_file() and legal_text:
                    retain(name, path, relative)

    ndk = sdk / "android-ndk-r29"
    # libc++_shared.so is a distributed runtime. Retain its pinned LLVM notice,
    # plus the NDK sysroot notice, without packaging Python/compiler tool notices.
    for relative in [Path("toolchains/llvm/prebuilt/linux-x86_64/NOTICE"),
                     Path("toolchains/llvm/prebuilt/linux-x86_64/sysroot/NOTICE")]:
        path = ndk / relative
        if not path.is_file():
            raise RuntimeError("Pinned NDK runtime license is missing")
        retain("ndk-libcxx", path, relative)
    return origins


def build(work, jobs):
    work.mkdir(parents=True, exist_ok=True)
    upstream = work / "mpv-android"
    checkout(MANIFEST["sources"]["mpv-android"], upstream)
    scripts = upstream / "buildscripts"
    for name, spec in MANIFEST["sources"].items():
        if name != "mpv-android":
            checkout(spec, scripts / "deps" / name)
    headers = scripts / "deps/libplacebo/3rdparty/Vulkan-Headers"
    actual_headers = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=headers, text=True).strip()
    if actual_headers != MANIFEST["vulkanHeadersCommit"]:
        raise RuntimeError("Unexpected libplacebo Vulkan-Headers pin")

    sdk = scripts / "sdk"
    sdk.mkdir(exist_ok=True)
    ndk_archive = sdk / "android-ndk-r29-linux.zip"
    download(MANIFEST["ndk"], ndk_archive, "sha1")
    if not (sdk / "android-ndk-r29/source.properties").exists():
        # InfoZIP retains the NDK's symlinks and executable modes.
        run(["unzip", "-q", ndk_archive, "-d", sdk])
    if MANIFEST["ndk"]["version"] not in (sdk / "android-ndk-r29/source.properties").read_text():
        raise RuntimeError("Unexpected Android NDK")
    lua_archive = work / "lua-5.2.4.tar.gz"
    download(MANIFEST["lua"], lua_archive)
    if not (scripts / "deps/lua/src/lua.h").exists():
        lua = scripts / "deps/lua"
        lua.mkdir(parents=True, exist_ok=True)
        with tarfile.open(lua_archive) as archive:
            for member in archive.getmembers():
                member.name = "/".join(member.name.split("/")[1:])
                if member.name:
                    archive.extract(member, lua, filter="data")

    # The official release used generated tarballs for this dependency; pinned Git
    # sources need their configure script regenerated with the container tools.
    if not (scripts / "deps/unibreak/configure").exists():
        run(["autoreconf", "-fiv"], scripts / "deps/unibreak")

    replace(scripts / "buildall.sh", "local apilvl=21", "local apilvl=24")
    replace(scripts / "include/depinfo.sh", "dep_libplacebo=()", "dep_shaderc=()\ndep_libplacebo=(shaderc)")
    placebo_script = scripts / "scripts/libplacebo.sh"
    # mpv does not supply pl_vk_inst_create.get_proc_addr. libplacebo must link
    # Android's Vulkan loader and resolve vkGetInstanceProcAddr itself.
    if "-Dvk-proc-addr=disabled" in placebo_script.read_text():
        replace(placebo_script, "-Dvk-proc-addr=disabled", "-Dvk-proc-addr=enabled")
    replace(placebo_script, "-Dvulkan=disabled -Ddemos=false", "-Dvulkan=enabled -Dshaderc=enabled -Dglslang=disabled -Dvk-proc-addr=enabled -Dopengl=enabled -Ddemos=false")
    # A cached Meson build otherwise ignores changed setup options. Reconfigure
    # in place; retain the pinned source/dependency cache rather than deleting it.
    reconfigure_meson(placebo_script)
    mpv_script = scripts / "scripts/mpv.sh"
    # Resume workspaces created before correcting mpv's OpenGL option name.
    if "-Dopengl=enabled" in mpv_script.read_text():
        replace(mpv_script, "-Dopengl=enabled", "-Dgl=enabled")
    if "-Degl=enabled" in mpv_script.read_text():
        replace(mpv_script, "-Degl=enabled", "-Degl=disabled -Degl-android=enabled")
    # Android discovers NDK libEGL directly. Generic EGL expects desktop pkg-config.
    replace(mpv_script, "-Diconv=disabled -Dlua=enabled", "-Diconv=disabled -Dlua=enabled -Dvulkan=enabled -Dgl=enabled -Degl=disabled -Degl-android=enabled")
    reconfigure_meson(mpv_script)
    mpv_source = scripts / "deps/mpv"
    install_hdr_egl_source(mpv_source)
    # The pinned helper requests GLES 2 even when an Android HDR carrier needs
    # GLES 3. Pass the required native version explicitly, including config
    # renderable bits; retain its ordinary default for other callers.
    replace(mpv_source / "video/out/opengl/egl_helpers.h", """    void *user_data;
};""", """    void *user_data;
    int gles_version; // zero preserves the ordinary GLES 2 request
    void (*report_error)(void *user_data, const char *stage, EGLint error);
};""")
    egl_helper = mpv_source / "video/out/opengl/egl_helpers.c"
    replace(egl_helper, """        rend = EGL_OPENGL_ES2_BIT;
        name = "GLES 2.x +";""", """        rend = cb.gles_version >= 3 ? EGL_OPENGL_ES3_BIT_KHR : EGL_OPENGL_ES2_BIT;
        name = cb.gles_version >= 3 ? "GLES 3.x +" : "GLES 2.x +";""")
    replace(egl_helper, """    if (!eglBindAPI(api)) {
        MP_VERBOSE(ctx, "Could not bind API!\\n");""", """    if (!eglBindAPI(api)) {
        if (cb.report_error)
            cb.report_error(cb.user_data, "api-bind", eglGetError());
        MP_VERBOSE(ctx, "Could not bind API!\\n");""")
    replace(egl_helper, """    if (!eglChooseConfig(display, attributes, NULL, 0, &num_configs))
        num_configs = 0;

    EGLConfig *configs = talloc_array(NULL, EGLConfig, num_configs);
    if (!eglChooseConfig(display, attributes, configs, num_configs, &num_configs))
        num_configs = 0;

    if (!num_configs) {
        talloc_free(configs);
        MP_MSG(ctx, msgl, "Could not choose EGLConfig for %s!\\n", name);
        return false;
    }""", """    if (!eglChooseConfig(display, attributes, NULL, 0, &num_configs)) {
        if (cb.report_error)
            cb.report_error(cb.user_data, "config-count", eglGetError());
        return false;
    }
    if (!num_configs) {
        if (cb.report_error)
            cb.report_error(cb.user_data, "config-match", EGL_SUCCESS);
        MP_MSG(ctx, msgl, "Could not choose EGLConfig for %s!\\n", name);
        return false;
    }

    EGLConfig *configs = talloc_array(NULL, EGLConfig, num_configs);
    if (!eglChooseConfig(display, attributes, configs, num_configs, &num_configs)) {
        if (cb.report_error)
            cb.report_error(cb.user_data, "config-list", eglGetError());
        talloc_free(configs);
        return false;
    }
    if (!num_configs) {
        if (cb.report_error)
            cb.report_error(cb.user_data, "config-match", EGL_SUCCESS);
        talloc_free(configs);
        return false;
    }""")
    replace(egl_helper, """    if (chosen < 0 || chosen == num_configs) {
        talloc_free(configs);""", """    if (chosen < 0 || chosen == num_configs) {
        if (cb.report_error)
            cb.report_error(cb.user_data, "config-refine", EGL_SUCCESS);
        talloc_free(configs);""")
    replace(egl_helper, """            es ? EGL_CONTEXT_CLIENT_VERSION : EGL_NONE, 2,""",
            """            es ? EGL_CONTEXT_CLIENT_VERSION : EGL_NONE, cb.gles_version >= 3 ? 3 : 2,""")
    replace(egl_helper, """        MP_MSG(ctx, msgl, "Could not create EGL context for %s (error=%d)!\\n",
               name, eglGetError());""", """        EGLint error = eglGetError();
        if (cb.report_error)
            cb.report_error(cb.user_data, "context-create", error);
        MP_MSG(ctx, msgl, "Could not create EGL context for %s (error=%d)!\\n",
               name, error);""")
    # EGL's component type defaults to FIXED. Include floating-point configs
    # in automatic enumeration so Android's refiner can select real FP16 when
    # no ten-bit config is available. Explicit output formats retain their
    # exact fixed/float type, and drivers without the extension are unchanged.
    replace(mpv_source / "video/out/opengl/egl_helpers.c", """        opts->output_format && has_float_format_ext ? EGL_COLOR_COMPONENT_TYPE_EXT : EGL_NONE,
        request_float_fmt ? EGL_COLOR_COMPONENT_TYPE_FLOAT_EXT : EGL_COLOR_COMPONENT_TYPE_FIXED_EXT,""", """        has_float_format_ext ? EGL_COLOR_COMPONENT_TYPE_EXT : EGL_NONE,
        !opts->output_format ? EGL_DONT_CARE :
            request_float_fmt ? EGL_COLOR_COMPONENT_TYPE_FLOAT_EXT : EGL_COLOR_COMPONENT_TYPE_FIXED_EXT,""")
    # MediaCodec's current external RGB route drops Dolby Vision RPU metadata.
    # Reject known DV before creating that decoder instead of silently treating
    # its HEVC base layer as the complete Dolby Vision presentation.
    mediacodec = scripts / "deps/ffmpeg/libavcodec/mediacodecdec.c"
    replace(mediacodec, '#include "libavutil/common.h"',
            '#include "libavutil/common.h"\n#include "libavutil/dovi_meta.h"')
    replace(mediacodec, """    MediaCodecH264DecContext *s = avctx->priv_data;

    if (s->use_ndk_codec < 0)""", """    MediaCodecH264DecContext *s = avctx->priv_data;

    const AVPacketSideData *dovi_sd = ff_get_coded_side_data(avctx, AV_PKT_DATA_DOVI_CONF);
    if (dovi_sd) {
        if (!dovi_sd->data || dovi_sd->size < sizeof(AVDOVIDecoderConfigurationRecord)) {
            av_log(avctx, AV_LOG_ERROR, "Invalid Dolby Vision configuration for hardware output.\\n");
            return AVERROR_INVALIDDATA;
        }
        const AVDOVIDecoderConfigurationRecord *dovi = (const void *)dovi_sd->data;
        av_log(avctx, AV_LOG_ERROR, "Dolby Vision profile %u hardware output is unsupported by the current external RGB path; RPU-preserving output is required.\\n", dovi->dv_profile);
        return AVERROR(ENOSYS);
    }

    if (s->use_ndk_codec < 0)""")
    # External OES samplers otherwise default to lowp under GLES, which loses
    # the precision supplied by MediaCodec's opaque hardware buffers.
    replace(scripts / "deps/libplacebo/src/dispatch.c",
            "} else if (gpu->glsl.gles && prefix != ' ') {",
            "} else if (gpu->glsl.gles && (prefix != ' ' || tex->sampler_type == PL_SAMPLER_EXTERNAL)) {")
    # gpu-next does not read mpv's legacy fbo-format option. Keep actual HDR
    # intermediate formats above eight bits inside the renderer itself.
    replace(scripts / "deps/libplacebo/src/renderer.c",
            """        if (params->force_low_bit_depth_fbos && configs[i].depth > 8)
            continue;
""", """        bool hdr_gles = rr->gpu->glsl.gles &&
            (pl_color_space_is_hdr(&pass->image.color) ||
             pl_color_space_is_hdr(&pass->target.color) ||
             pass->image.repr.sys == PL_COLOR_SYSTEM_DOLBYVISION);
        if (hdr_gles && configs[i].depth < 16)
            continue;
        if (params->force_low_bit_depth_fbos && configs[i].depth > 8)
            continue;
""")
    replace(scripts / "deps/libplacebo/src/renderer.c", """    find_fbo_format(pass);
    pass_fix_frames(pass);""", """    find_fbo_format(pass);
    if (pass->rr->gpu->glsl.gles && !pass->fbofmt[4] &&
        (pl_color_space_is_hdr(&pass->image.color) ||
         pl_color_space_is_hdr(&pass->target.color) ||
         pass->image.repr.sys == PL_COLOR_SYSTEM_DOLBYVISION)) {
        PL_ERR(pass->rr, "Android HDR requires high precision intermediate textures.");
        goto error;
    }
    pass_fix_frames(pass);""")
    # Strict hardware initialization/reinitialization failure is not a natural
    # EOF, even when the decoder already presented frames earlier in the file.
    strict_video = mpv_source / "player/video.c"
    old_strict_error = """            MP_ERR(mpctx, "Hardware decoding failed with software fallback disabled.\\n");
            mpctx->error_playing = MPV_ERROR_GENERIC;"""
    if old_strict_error in strict_video.read_text():
        # Upgrade only this known managed patch while retaining the cache.
        replace(strict_video, old_strict_error, """            MP_ERR(mpctx, "Hardware decoding failed with software fallback disabled.\\n");
            if (mpctx->error_playing >= 0)
                mpctx->error_playing = MPV_ERROR_GENERIC;""")
    replace(mpv_source / "player/video.c", """        if (check_for_forced_eof(mpctx)) {
            uninit_video_chain(mpctx);
            handle_force_window(mpctx, true);
            return;
        }""", """        if (check_for_forced_eof(mpctx)) {
            MP_ERR(mpctx, "Hardware decoding failed with software fallback disabled.\\n");
            if (mpctx->error_playing >= 0)
                mpctx->error_playing = MPV_ERROR_GENERIC;
            mpctx->stop_play = PT_ERROR;
            uninit_video_chain(mpctx);
            handle_force_window(mpctx, true);
            mp_wakeup_core(mpctx);
            return;
        }""")
    replace(upstream / "app/src/main/jni/Application.mk", "APP_PLATFORM := android-21", "APP_PLATFORM := android-24")
    jni = upstream / "app/src/main/jni/property.cpp"
    if "setPropertyStringChecked" not in jni.read_text():
        jni.write_text(jni.read_text() + CHECKED_JNI)
    elif CHECKED_JNI not in jni.read_text():
        # A managed workspace may have been started before the hook patch landed.
        data = jni.read_text()
        jni.write_text(data[:data.index("// OhMyCine: return native status")] + CHECKED_JNI)
    event = upstream / "app/src/main/jni/event.cpp"
    event_text = event.read_text()
    managed_cases = ["        case MPV_EVENT_START_FILE: {", "        case MPV_EVENT_HOOK: {",
                     "        case MPV_EVENT_END_FILE: {"]
    if any(case in event_text for case in managed_cases):
        first = min(event_text.index(case) for case in managed_cases if case in event_text)
        last = event_text.index("        case MPV_EVENT_PROPERTY_CHANGE:", first)
        event.write_text(event_text[:first] + event_text[last:])
    replace(event, "        case MPV_EVENT_PROPERTY_CHANGE:",
            START_FILE_EVENT + LOAD_HOOK_EVENT + END_FILE_EVENT + "        case MPV_EVENT_PROPERTY_CHANGE:")
    replace(event, "        }\n    }\n\n    g_vm->DetachCurrentThread();", '''        }
        // Never carry a pending Java exception into the next native callback.
        if (env->ExceptionCheck()) {
            env->ExceptionClear();
            mpv_set_property_string(g_mpv, "vo", "null");
            const char *stop[] = {"stop", NULL};
            mpv_command_async(g_mpv, 0, stop);
        }
    }

    g_vm->DetachCurrentThread();''')
    shutil.copyfile(BUILDER / "shaderc.sh", scripts / "scripts/shaderc.sh")
    (scripts / "scripts/shaderc.sh").chmod(0o755)
    prefix = scripts / "prefix/arm64"
    (prefix / "lib/pkgconfig").mkdir(parents=True, exist_ok=True)
    for name in ["usr", "local"]:
        if not (prefix / name).exists():
            (prefix / name).symlink_to(".", target_is_directory=True)
    (prefix / "include").mkdir(exist_ok=True)
    for item in (headers / "include").iterdir():
        shutil.copytree(item, prefix / "include" / item.name, dirs_exist_ok=True)
    (prefix / "lib/pkgconfig/vulkan.pc").write_text(
        "prefix=/usr/local\nincludedir=${prefix}/include\nName: Vulkan\n"
        "Description: Android Vulkan loader and pinned headers\nVersion: 1.4.313\n"
        "Libs: -lvulkan\nCflags: -I${includedir}\n")
    env = os.environ.copy()
    env["cores"] = str(jobs)
    # bash without -e ignores the upstream shebang's fail-fast flag, otherwise
    # one failed dependency can be followed by misleading JNI/linker errors.
    run(["bash", "-e", "buildall.sh", "--arch", "arm64", "mpv"], scripts, env)
    env["PREFIX64"] = str(prefix)
    run([sdk / "android-ndk-r29/ndk-build", "-C", upstream / "app/src/main", "-j" + str(jobs)], env=env)

    output = work / "output"
    libs = output / "lib/arm64-v8a"
    libs.mkdir(parents=True, exist_ok=True)
    for path in (upstream / "app/src/main/libs/arm64-v8a").glob("*.so"):
        shutil.copyfile(path, libs / path.name)
    mpv = libs / "libmpv.so"
    if not mpv.exists() or b"androidvk\x00" not in mpv.read_bytes():
        raise RuntimeError("Built libmpv has no androidvk context")
    for marker in [b"OHMYCINE_ANDROID_HDR_EGL_V1",
                   b"OHMYCINE_EGL_FAILURE_V2", b"OHMYCINE_EGL_BOOTSTRAP_V2", b"GLES 3.x +",
                   b"Android HDR requires high precision intermediate textures.",
                   b"Hardware decoding failed with software fallback disabled."]:
        if marker not in mpv.read_bytes():
            raise RuntimeError("Built libmpv lacks the hardware HDR patch: " + marker.decode())
    if b"RPU-preserving output is required." not in (libs / "libavcodec.so").read_bytes():
        raise RuntimeError("Built FFmpeg lacks the known Dolby Vision hardware guard")
    features = (scripts / "deps/mpv/_build-arm64/config.h").read_text()
    if any("#define " + name + " 1" not in features
           for name in ["HAVE_VULKAN", "HAVE_LIBPLACEBO", "HAVE_GL", "HAVE_EGL_ANDROID", "HAVE_LIBASS"]):
        raise RuntimeError("Built mpv feature verification failed")
    placebo_config = prefix / "include/libplacebo/config.h"
    if any("#define " + name + " 1" not in placebo_config.read_text()
           for name in ["PL_HAVE_VULKAN", "PL_HAVE_OPENGL", "PL_HAVE_SHADERC", "PL_HAVE_VK_PROC_ADDR"]):
        raise RuntimeError("Built libplacebo Vulkan/loader/OpenGL/shaderc verification failed")
    # Extract only the fixed release's CA asset; the complete JNI set is built together above.
    apk = work / "official-ca.apk"
    download(MANIFEST["officialApk"], apk)
    (output / "assets").mkdir(exist_ok=True)
    with zipfile.ZipFile(apk) as archive:
        (output / "assets/cacert.pem").write_bytes(archive.read("assets/cacert.pem"))
    readelf = sdk / "android-ndk-r29/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-readelf"
    mpv_symbols = subprocess.check_output([str(readelf), "--dyn-syms", "--wide", str(mpv)], text=True)
    if not re.search(r"\bUND\s+vkGetInstanceProcAddr(?:@\S+)?(?:\s|$)", mpv_symbols):
        raise RuntimeError("Built libmpv does not import Android's Vulkan loader")
    symbols = subprocess.check_output([str(readelf), "--dyn-syms", "--wide", str(libs / "libplayer.so")], text=True)
    for symbol in ["attachSurface", "setPropertyStringChecked", "commandChecked", "loadFileChecked", "addLoadHook", "continueHook", "stopAsync"]:
        if "Java_is_xyz_mpv_MPVLib_" + symbol not in symbols:
            raise RuntimeError("JNI symbol missing: " + symbol)
    if not re.search(r"\bUND\s+mpv_command_ret(?:@\S+)?(?:\s|$)", symbols):
        raise RuntimeError("JNI does not consume the authoritative loadfile result")
    for marker in [b"OHMYCINE_MPV_ENTRY_ID_V1", b"eventStartFile\0", b"eventEndFile\0", b"(IIJJI)V\0"]:
        if marker not in (libs / "libplayer.so").read_bytes():
            raise RuntimeError("JNI playlist identity callback missing: " + marker.decode())
    dependencies = {}
    supplied = {path.name for path in libs.glob("*.so")}
    platform = {"libandroid.so", "liblog.so", "libm.so", "libc.so", "libdl.so", "libz.so", "libEGL.so", "libGLESv2.so", "libOpenSLES.so", "libvulkan.so", "libmediandk.so"}
    for path in libs.glob("*.so"):
        dynamic = subprocess.check_output([str(readelf), "-d", str(path)], text=True)
        needed = re.findall(r"\(NEEDED\).*?\[(.*?)\]", dynamic)
        if set(needed) - supplied - platform:
            raise RuntimeError("Unpackaged ELF dependency for " + path.name + ": " + str(needed))
        dependencies[path.name] = needed
    if "libvulkan.so" not in dependencies.get("libmpv.so", []):
        raise RuntimeError("Built libmpv has no Android Vulkan loader dependency")
    license_origins = export_licenses(output, upstream, scripts, sdk)
    artifact = dict(MANIFEST)
    artifact["sourceManifestSha256"] = hashlib.sha256((BUILDER / "sources.json").read_bytes()).hexdigest()
    builder_hash = hashlib.sha256()
    for name in ["sources.json", "build-runtime.py", "shaderc.sh", "Dockerfile", "android-hdr-egl.c"]:
        builder_hash.update(name.encode())
        builder_hash.update(b"\0")
        builder_hash.update((BUILDER / name).read_bytes())
    artifact["builderInputsSha256"] = builder_hash.hexdigest()
    artifact["jniBridgePatchSha256"] = hashlib.sha256(CHECKED_JNI.encode()).hexdigest()
    artifact["loadHookPatchSha256"] = hashlib.sha256(LOAD_HOOK_EVENT.encode()).hexdigest()
    artifact["endFilePatchSha256"] = hashlib.sha256(END_FILE_EVENT.encode()).hexdigest()
    artifact["startFilePatchSha256"] = hashlib.sha256(START_FILE_EVENT.encode()).hexdigest()
    artifact["files"] = {str(path.relative_to(output)): hashlib.sha256(path.read_bytes()).hexdigest()
                         for path in sorted(output.rglob("*"))
                         if path.is_file() and path != output / "runtime-manifest.json"}
    artifact["elfDependencies"] = dependencies
    artifact["licenseSources"] = license_origins
    (output / "runtime-manifest.json").write_text(json.dumps(artifact, indent=2) + "\n")
    with tarfile.open(work / (MANIFEST["runtimeId"] + ".tar.gz"), "w:gz") as archive:
        for path in sorted(output.iterdir()):
            archive.add(path, arcname=path.name)
    print("Complete runtime:", output, flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--work-dir", type=Path, default=Path("/work/runtime"))
    parser.add_argument("--jobs", type=int, default=4)
    args = parser.parse_args()
    if args.jobs < 1 or args.jobs > 8:
        parser.error("jobs must be between 1 and 8")
    build(args.work_dir.resolve(), args.jobs)
