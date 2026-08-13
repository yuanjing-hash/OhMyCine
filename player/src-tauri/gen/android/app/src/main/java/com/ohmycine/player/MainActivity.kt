package com.ohmycine.player

import android.graphics.Color
import android.content.Intent
import android.os.Bundle
import androidx.activity.result.ActivityResult
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.SystemBarStyle
import androidx.activity.enableEdgeToEdge

class MainActivity : TauriActivity() {
  private lateinit var localMediaPickerLauncher: ActivityResultLauncher<Intent>
  private lateinit var downloadDirectoryPickerLauncher: ActivityResultLauncher<Intent>
  private var localMediaPickerCallback: ((ActivityResult) -> Unit)? = null
  private var downloadDirectoryPickerCallback: ((ActivityResult) -> Unit)? = null

  override fun onCreate(savedInstanceState: Bundle?) {
    enableEdgeToEdge(
      statusBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
      navigationBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
    )
    super.onCreate(savedInstanceState)
    localMediaPickerLauncher = registerForActivityResult(
      ActivityResultContracts.StartActivityForResult(),
    ) { result ->
      val callback = localMediaPickerCallback
      localMediaPickerCallback = null
      callback?.invoke(result)
    }
    downloadDirectoryPickerLauncher = registerForActivityResult(
      ActivityResultContracts.StartActivityForResult(),
    ) { result ->
      val callback = downloadDirectoryPickerCallback
      downloadDirectoryPickerCallback = null
      callback?.invoke(result)
    }
  }

  fun launchLocalMediaPicker(intent: Intent, callback: (ActivityResult) -> Unit) {
    check(::localMediaPickerLauncher.isInitialized) { "Android 文件选择器尚未初始化。" }
    check(localMediaPickerCallback == null) { "已有文件选择操作正在进行。" }
    check(!isFinishing && !isDestroyed) { "Android 页面已关闭，无法打开文件选择器。" }

    localMediaPickerCallback = callback
    try {
      localMediaPickerLauncher.launch(intent)
    } catch (error: Exception) {
      localMediaPickerCallback = null
      throw error
    }
  }

  fun launchDownloadDirectoryPicker(intent: Intent, callback: (ActivityResult) -> Unit) {
    check(::downloadDirectoryPickerLauncher.isInitialized) { "Android 下载目录选择器尚未初始化。" }
    check(downloadDirectoryPickerCallback == null) { "已有下载目录选择操作正在进行。" }
    check(!isFinishing && !isDestroyed) { "Android 页面已关闭，无法选择下载目录。" }
    downloadDirectoryPickerCallback = callback
    try {
      downloadDirectoryPickerLauncher.launch(intent)
    } catch (error: Exception) {
      downloadDirectoryPickerCallback = null
      throw error
    }
  }
}
