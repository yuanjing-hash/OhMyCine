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
            jmethodID callback = env->GetStaticMethodID(mpv_MPVLib, "eventEndFile", "(II)V");
            if (callback) env->CallStaticVoidMethod(mpv_MPVLib, callback, (jint)reason, (jint)error);
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
    replace(scripts / "scripts/libplacebo.sh", "-Dvulkan=disabled -Ddemos=false", "-Dvulkan=enabled -Dshaderc=enabled -Dglslang=disabled -Dvk-proc-addr=disabled -Dopengl=enabled -Ddemos=false")
    mpv_script = scripts / "scripts/mpv.sh"
    # Resume workspaces created before correcting mpv's OpenGL option name.
    if "-Dopengl=enabled" in mpv_script.read_text():
        replace(mpv_script, "-Dopengl=enabled", "-Dgl=enabled")
    if "-Degl=enabled" in mpv_script.read_text():
        replace(mpv_script, "-Degl=enabled", "-Degl=disabled -Degl-android=enabled")
    # Android discovers NDK libEGL directly. Generic EGL expects desktop pkg-config.
    replace(mpv_script, "-Diconv=disabled -Dlua=enabled", "-Diconv=disabled -Dlua=enabled -Dvulkan=enabled -Dgl=enabled -Degl=disabled -Degl-android=enabled")
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
    if "        case MPV_EVENT_HOOK: {" in event_text:
        first = event_text.index("        case MPV_EVENT_HOOK: {")
        last = event_text.index("        case MPV_EVENT_PROPERTY_CHANGE:", first)
        event.write_text(event_text[:first] + event_text[last:])
    replace(event, "        case MPV_EVENT_PROPERTY_CHANGE:", LOAD_HOOK_EVENT + "        case MPV_EVENT_PROPERTY_CHANGE:")
    event_text = event.read_text()
    if "        case MPV_EVENT_END_FILE: {" in event_text:
        first = event_text.index("        case MPV_EVENT_END_FILE: {")
        last = event_text.index("        case MPV_EVENT_PROPERTY_CHANGE:", first)
        event.write_text(event_text[:first] + event_text[last:])
    replace(event, "        case MPV_EVENT_PROPERTY_CHANGE:", END_FILE_EVENT + "        case MPV_EVENT_PROPERTY_CHANGE:")
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
    features = (scripts / "deps/mpv/_build-arm64/config.h").read_text()
    if any("#define " + name + " 1" not in features
           for name in ["HAVE_VULKAN", "HAVE_LIBPLACEBO", "HAVE_GL", "HAVE_EGL_ANDROID", "HAVE_LIBASS"]):
        raise RuntimeError("Built mpv feature verification failed")
    placebo_config = next((scripts / "deps/libplacebo/_build-arm64").rglob("config.h"))
    if any("#define " + name + " 1" not in placebo_config.read_text()
           for name in ["PL_HAVE_VULKAN", "PL_HAVE_OPENGL", "PL_HAVE_SHADERC"]):
        raise RuntimeError("Built libplacebo Vulkan/OpenGL/shaderc verification failed")
    # Extract only the fixed release's CA asset; the complete JNI set is built together above.
    apk = work / "official-ca.apk"
    download(MANIFEST["officialApk"], apk)
    (output / "assets").mkdir(exist_ok=True)
    with zipfile.ZipFile(apk) as archive:
        (output / "assets/cacert.pem").write_bytes(archive.read("assets/cacert.pem"))
    readelf = sdk / "android-ndk-r29/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-readelf"
    symbols = subprocess.check_output([str(readelf), "--dyn-syms", "--wide", str(libs / "libplayer.so")], text=True)
    for symbol in ["attachSurface", "setPropertyStringChecked", "commandChecked", "addLoadHook", "continueHook", "stopAsync"]:
        if "Java_is_xyz_mpv_MPVLib_" + symbol not in symbols:
            raise RuntimeError("JNI symbol missing: " + symbol)
    dependencies = {}
    supplied = {path.name for path in libs.glob("*.so")}
    platform = {"libandroid.so", "liblog.so", "libm.so", "libc.so", "libdl.so", "libz.so", "libEGL.so", "libGLESv2.so", "libOpenSLES.so", "libvulkan.so", "libmediandk.so"}
    for path in libs.glob("*.so"):
        dynamic = subprocess.check_output([str(readelf), "-d", str(path)], text=True)
        needed = re.findall(r"\(NEEDED\).*?\[(.*?)\]", dynamic)
        if set(needed) - supplied - platform:
            raise RuntimeError("Unpackaged ELF dependency for " + path.name + ": " + str(needed))
        dependencies[path.name] = needed
    license_origins = export_licenses(output, upstream, scripts, sdk)
    artifact = dict(MANIFEST)
    artifact["sourceManifestSha256"] = hashlib.sha256((BUILDER / "sources.json").read_bytes()).hexdigest()
    builder_hash = hashlib.sha256()
    for name in ["sources.json", "build-runtime.py", "shaderc.sh", "Dockerfile"]:
        builder_hash.update(name.encode())
        builder_hash.update(b"\0")
        builder_hash.update((BUILDER / name).read_bytes())
    artifact["builderInputsSha256"] = builder_hash.hexdigest()
    artifact["jniBridgePatchSha256"] = hashlib.sha256(CHECKED_JNI.encode()).hexdigest()
    artifact["loadHookPatchSha256"] = hashlib.sha256(LOAD_HOOK_EVENT.encode()).hexdigest()
    artifact["endFilePatchSha256"] = hashlib.sha256(END_FILE_EVENT.encode()).hexdigest()
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
