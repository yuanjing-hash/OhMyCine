package com.ohmycine.player.updater

import android.app.Activity
import android.content.Intent
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

@InvokeArg
class InstallArgs {
    lateinit var path: String
}

@TauriPlugin
class UpdaterPlugin(private val activity: Activity) : Plugin(activity) {
    @Command
    fun install(invoke: Invoke) {
        try {
            val args = invoke.parseArgs(InstallArgs::class.java)
            val apk = File(args.path).canonicalFile
            val cacheRoot = activity.cacheDir.canonicalFile
            require(apk.isFile && apk.extension.equals("apk", ignoreCase = true)) {
                "Android 更新安装包不存在。"
            }
            require(apk.path.startsWith(cacheRoot.path + File.separator)) {
                "Android 更新安装包不在受信任缓存目录中。"
            }

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && !activity.packageManager.canRequestPackageInstalls()) {
                val permissionIntent = Intent(
                    Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                    Uri.parse("package:${activity.packageName}"),
                ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                activity.runOnUiThread {
                    activity.startActivity(permissionIntent)
                    invoke.reject("请允许 OhMyCine 安装未知应用，然后返回并再次点击安装。")
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
                activity.startActivity(installIntent)
                invoke.resolveObject(mapOf("launched" to true))
            }
        } catch (error: Exception) {
            invoke.reject(error.message ?: "Android 系统安装器启动失败。")
        }
    }
}
