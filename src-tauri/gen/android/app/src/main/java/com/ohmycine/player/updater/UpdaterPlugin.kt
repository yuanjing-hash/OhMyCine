package com.ohmycine.player.updater

import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.Plugin
import java.io.File
import java.security.MessageDigest

@InvokeArg
class InstallArgs {
    lateinit var path: String
    lateinit var version: String
}

@InvokeArg
class DownloadArgs {
    var version: String = ""
    var downloaded: Long = 0
    var total: Long? = null
}

@InvokeArg
class FinishArgs { var ready: Boolean = false }

@TauriPlugin
class UpdaterPlugin(private val activity: Activity) : Plugin(activity) {
    @Command
    fun directory(invoke: Invoke) {
        try {
            val root = File(activity.filesDir, "updates")
            require(root.mkdirs() || root.isDirectory)
            require(root.canonicalFile.parentFile == activity.filesDir.canonicalFile)
            invoke.resolveObject(mapOf("path" to root.canonicalPath))
        } catch (_: Exception) {
            invoke.reject("无法创建私有更新目录。")
        }
    }

    @Command
    fun startDownload(invoke: Invoke) {
        try {
            val args = invoke.parseArgs(DownloadArgs::class.java)
            UpdaterService.begin(activity.applicationContext, args.version, args.downloaded, args.total) { active ->
                invoke.resolveObject(mapOf("active" to active))
            }
        } catch (_: Exception) {
            invoke.reject("后台下载服务未能启动，请返回应用后继续下载。")
        }
    }

    @Command
    fun downloadProgress(invoke: Invoke) {
        val args = invoke.parseArgs(DownloadArgs::class.java)
        invoke.resolveObject(mapOf("active" to UpdaterService.progress(args.downloaded, args.total)))
    }

    @Command
    fun finishDownload(invoke: Invoke) {
        val args = invoke.parseArgs(FinishArgs::class.java)
        UpdaterService.finish(args.ready)
        invoke.resolveObject(mapOf("finished" to true))
    }

    @Command
    fun install(invoke: Invoke) {
        try {
            val args = invoke.parseArgs(InstallArgs::class.java)
            val apk = File(args.path).canonicalFile
            val updateRoot = File(activity.filesDir, "updates").canonicalFile
            require(apk.isFile && apk.extension.equals("apk", ignoreCase = true)) {
                "Android 更新安装包不存在。"
            }
            require(apk.parentFile == updateRoot && apk.name == "package.apk") {
                "Android 更新安装包不在私有更新目录中。"
            }
            try {
                validatePackage(apk, args.version)
            } catch (_: Exception) {
                invoke.resolveObject(mapOf("status" to "invalidPackage"))
                return
            }

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && !activity.packageManager.canRequestPackageInstalls()) {
                val permissionIntent = Intent(
                    Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                    Uri.parse("package:${activity.packageName}"),
                ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                activity.runOnUiThread {
                    try {
                        activity.startActivity(permissionIntent)
                        invoke.resolveObject(mapOf("status" to "permissionRequired"))
                    } catch (_: Exception) {
                        invoke.reject("无法打开安装权限设置，请返回应用后重试安装。")
                    }
                }
                return
            }

            val uri = FileProvider.getUriForFile(
                activity,
                "${activity.packageName}.fileprovider",
                apk,
            )
            val installIntent = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, "application/vnd.android.package-archive")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            activity.runOnUiThread {
                try {
                    activity.startActivity(installIntent)
                    invoke.resolveObject(mapOf("status" to "launched"))
                } catch (_: Exception) {
                    invoke.reject("Android 系统安装器启动失败，请返回应用后重试安装。")
                }
            }
        } catch (_: Exception) {
            invoke.reject("Android 更新安装包不可用或系统安装器无法启动，请重新检测更新。")
        }
    }

    @Suppress("DEPRECATION")
    private fun validatePackage(apk: File, version: String) {
        val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P)
            PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES
        val archive = activity.packageManager.getPackageArchiveInfo(apk.path, flags)
            ?: error("Android 更新安装包无效。")
        require(archive.packageName == activity.packageName && archive.versionName == version) {
            "Android 更新安装包的应用或版本不匹配。"
        }
        val installed = activity.packageManager.getPackageInfo(activity.packageName, flags)
        val archiveSignatures = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P)
            archive.signingInfo?.apkContentsSigners else archive.signatures
        val installedSignatures = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P)
            installed.signingInfo?.apkContentsSigners else installed.signatures
        require(!archiveSignatures.isNullOrEmpty() && !installedSignatures.isNullOrEmpty()) {
            "无法验证 Android 更新安装包签名。"
        }
        val hashes = installedSignatures.map { MessageDigest.getInstance("SHA-256").digest(it.toByteArray()).toList() }.toSet()
        require(archiveSignatures.all { MessageDigest.getInstance("SHA-256").digest(it.toByteArray()).toList() in hashes }) {
            "Android 更新安装包与当前应用签名不一致。"
        }
    }
}
