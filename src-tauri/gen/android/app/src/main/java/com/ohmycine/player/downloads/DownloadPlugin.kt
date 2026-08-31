package com.ohmycine.player.downloads

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.provider.DocumentsContract
import android.util.Base64
import androidx.activity.result.ActivityResult
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.Plugin
import com.ohmycine.player.MainActivity
import java.io.FileNotFoundException
import java.security.MessageDigest

@InvokeArg class DirectoryArgs { lateinit var uri: String }
@InvokeArg class PrepareArgs { lateinit var directoryUri: String; lateinit var destinationName: String }
@InvokeArg class WriteArgs { lateinit var documentUri: String; lateinit var data: String; var truncate: Boolean = false }
@InvokeArg class ReadArgs { lateinit var rootUri: String; lateinit var path: String; var offset: Long = 0; var length: Int = 0 }
@InvokeArg class FinalizeArgs { lateinit var partialUri: String; lateinit var destinationName: String }
@InvokeArg class OwnedDocumentArgs { lateinit var directoryUri: String; lateinit var destinationName: String }
@InvokeArg class ProgressArgs {
    lateinit var taskId: String
    lateinit var title: String
    var downloaded: Long = 0
    var total: Long? = null
    lateinit var state: String
}

@TauriPlugin
class DownloadPlugin(private val activity: Activity) : Plugin(activity) {
    @Command
    fun pickDirectory(invoke: Invoke) {
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
            addFlags(Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION)
            addFlags(Intent.FLAG_GRANT_PREFIX_URI_PERMISSION)
        }
        val host = activity as? MainActivity ?: return invoke.reject("Android 下载目录选择器不可用。")
        activity.runOnUiThread {
            try {
                host.launchDownloadDirectoryPicker(intent) { result -> resolvePicker(invoke, result) }
            } catch (error: Exception) {
                invoke.reject(error.message ?: "Android 下载目录选择器启动失败。")
            }
        }
    }

    @Command fun validateDirectory(invoke: Invoke) = resolve(invoke) {
        val root = requireWritableTree(invoke.parseArgs(DirectoryArgs::class.java).uri)
        mapOf("name" to treeName(root))
    }

    @Command fun availableName(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(PrepareArgs::class.java)
        val root = requireWritableTree(args.directoryUri)
        mapOf("destinationName" to availableName(root, safeName(args.destinationName)))
    }

    @Command fun prepareDocument(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(PrepareArgs::class.java)
        val root = requireWritableTree(args.directoryUri)
        val name = safeName(args.destinationName)
        val partialName = ".$name.ohmycine-part"
        val existing = findChild(root, partialName)
        val partial = existing ?: DocumentsContract.createDocument(
            activity.contentResolver, rootDocument(root), "application/octet-stream", partialName,
        ) ?: error("无法创建 Android 下载临时文件。")
        mapOf(
            "partialUri" to partial.toString(),
            "destinationName" to name,
            "existingBytes" to documentSize(partial),
        )
    }

    @Command fun writeChunk(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(WriteArgs::class.java)
        val uri = requireOwnedDocument(args.documentUri)
        val bytes = Base64.decode(args.data, Base64.DEFAULT)
        require(bytes.size <= 512 * 1024) { "Android 下载分块过大。" }
        activity.contentResolver.openOutputStream(uri, if (args.truncate) "wt" else "wa")?.use {
            it.write(bytes)
            it.flush()
        } ?: error("Android 下载目录不可写，授权可能已失效。")
        mapOf("written" to bytes.size)
    }

    @Command fun readLocalChunk(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(ReadArgs::class.java)
        require(args.offset >= 0 && args.length in 1..(512 * 1024)) { "Android 本地媒体读取范围无效。" }
        val root = requireReadableTree(args.rootUri)
        val document = resolveDocument(root, args.path)
        val total = documentSize(document)
        val buffer = ByteArray(args.length)
        val count = activity.contentResolver.openFileDescriptor(document, "r")?.use { descriptor ->
            java.io.FileInputStream(descriptor.fileDescriptor).use { input ->
                input.channel.position(args.offset)
                input.read(buffer).coerceAtLeast(0)
            }
        } ?: error("Android 本地媒体授权已失效。")
        mapOf(
            "data" to Base64.encodeToString(buffer.copyOf(count), Base64.NO_WRAP),
            "bytesRead" to count,
            "totalBytes" to total,
            "entityHash" to documentEntityHash(document, total),
        )
    }

    @Command fun finalizeDocument(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(FinalizeArgs::class.java)
        val partial = requireOwnedDocument(args.partialUri)
        val targetName = safeName(args.destinationName)
        val root = treeForDocument(partial)
        require(findChild(root, targetName) == null) { "Android 下载目标文件已存在。" }
        DocumentsContract.renameDocument(activity.contentResolver, partial, targetName)
            ?: error("Android 下载文件完成命名失败。")
        mapOf("completed" to true)
    }

    @Command fun resolveCompletedDocument(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(OwnedDocumentArgs::class.java)
        val root = requireReadableTree(args.directoryUri)
        val document = findChild(root, safeName(args.destinationName))
        val size = document?.let { documentSize(it) }
        mapOf(
            "uri" to document?.toString(),
            "size" to size,
            "entityHash" to document?.let { documentEntityHash(it, size ?: 0) },
        )
    }

    @Command fun deleteCompletedDocument(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(OwnedDocumentArgs::class.java)
        val root = requireWritableTree(args.directoryUri)
        val document = findChild(root, safeName(args.destinationName))
        if (document != null && !DocumentsContract.deleteDocument(activity.contentResolver, document))
            error("Android 离线文件删除失败。")
        mapOf("deleted" to (document != null))
    }

    @Command fun deletePartialDocument(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(OwnedDocumentArgs::class.java)
        val root = requireWritableTree(args.directoryUri)
        val name = ".${safeName(args.destinationName)}.ohmycine-part"
        val document = findChild(root, name)
        if (document != null && !DocumentsContract.deleteDocument(activity.contentResolver, document))
            error("Android 下载临时文件删除失败。")
        mapOf("deleted" to (document != null))
    }

    @Command fun updateForeground(invoke: Invoke) = progress(invoke, false)
    @Command fun finishForeground(invoke: Invoke) = progress(invoke, true)

    private fun progress(invoke: Invoke, finished: Boolean) = resolve(invoke) {
        val args = invoke.parseArgs(ProgressArgs::class.java)
        require(args.taskId.matches(Regex("[0-9a-f]{32}"))) { "Android 下载任务标识无效。" }
        if (finished) DownloadService.finish(activity, args.taskId, args.title, args.state)
        else DownloadService.update(activity, args.taskId, args.title, args.downloaded, args.total)
        mapOf("notified" to true)
    }

    private fun resolvePicker(invoke: Invoke, result: ActivityResult) {
        try {
            if (result.resultCode == Activity.RESULT_CANCELED) return invoke.resolveObject(mapOf("cancelled" to true))
            require(result.resultCode == Activity.RESULT_OK) { "Android 下载目录选择失败。" }
            val data = result.data ?: error("Android 下载目录选择器没有返回结果。")
            val uri = data.data ?: error("Android 下载目录选择器没有返回 URI。")
            val flags = data.flags and (Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
            require(flags and Intent.FLAG_GRANT_READ_URI_PERMISSION != 0 && flags and Intent.FLAG_GRANT_WRITE_URI_PERMISSION != 0) {
                "所选 Android 目录没有授予完整读写权限。"
            }
            activity.contentResolver.takePersistableUriPermission(uri, flags)
            requireWritableTree(uri.toString())
            invoke.resolveObject(mapOf("cancelled" to false, "uri" to uri.toString(), "name" to treeName(uri)))
        } catch (error: Exception) {
            invoke.reject(error.message ?: "Android 下载目录授权失败。")
        }
    }

    private fun requireWritableTree(value: String): Uri {
        val root = Uri.parse(value.trim())
        require(root.scheme == "content" && DocumentsContract.isTreeUri(root)) { "Android 下载目录授权无效，请重新选择目录。" }
        val grant = activity.contentResolver.persistedUriPermissions.find { it.uri == root }
        require(grant?.isReadPermission == true && grant.isWritePermission) { "Android 下载目录授权已失效，请重新选择目录。" }
        val document = rootDocument(root)
        require(queryFlags(document) and DocumentsContract.Document.FLAG_DIR_SUPPORTS_CREATE.toLong() != 0L) { "所选 Android 目录不可写，请重新选择。" }
        return root
    }

    private fun requireReadableTree(value: String): Uri {
        val root = Uri.parse(value.trim())
        require(root.scheme == "content" && DocumentsContract.isTreeUri(root)) { "Android 本地媒体目录无效。" }
        require(activity.contentResolver.persistedUriPermissions.any { it.uri == root && it.isReadPermission }) { "Android 本地媒体目录授权已失效。" }
        return root
    }

    private fun requireOwnedDocument(value: String): Uri {
        val uri = Uri.parse(value.trim())
        require(uri.scheme == "content" && DocumentsContract.isDocumentUri(activity, uri)) { "Android 下载临时文件无效。" }
        require(activity.contentResolver.persistedUriPermissions.any { grant ->
            grant.isWritePermission
                && uri.authority == grant.uri.authority
                && runCatching {
                    DocumentsContract.getDocumentId(uri).startsWith(DocumentsContract.getTreeDocumentId(grant.uri))
                }.getOrDefault(false)
        }) { "Android 下载目录授权已失效，请重新选择目录。" }
        return uri
    }

    private fun rootDocument(root: Uri): Uri = DocumentsContract.buildDocumentUriUsingTree(root, DocumentsContract.getTreeDocumentId(root))
    private fun treeForDocument(document: Uri): Uri = activity.contentResolver.persistedUriPermissions
        .firstOrNull { grant ->
            grant.isWritePermission
                && document.authority == grant.uri.authority
                && runCatching { DocumentsContract.getDocumentId(document).startsWith(DocumentsContract.getTreeDocumentId(grant.uri)) }.getOrDefault(false)
        }?.uri ?: error("Android 下载目录授权已失效，请重新选择目录。")

    private fun findChild(root: Uri, name: String): Uri? {
        val parent = rootDocument(root)
        val children = DocumentsContract.buildChildDocumentsUriUsingTree(root, DocumentsContract.getDocumentId(parent))
        activity.contentResolver.query(children, arrayOf(DocumentsContract.Document.COLUMN_DOCUMENT_ID, DocumentsContract.Document.COLUMN_DISPLAY_NAME), null, null, null)?.use { cursor ->
            while (cursor.moveToNext()) if (cursor.getString(1) == name)
                return DocumentsContract.buildDocumentUriUsingTree(root, cursor.getString(0))
        }
        return null
    }

    private fun resolveDocument(root: Uri, path: String): Uri {
        val segments = path.replace('\\', '/').split('/').filter { it.isNotBlank() }
        require(segments.none { it == "." || it == ".." || Uri.decode(it) == ".." }) { "Android 本地媒体路径无效。" }
        var current = rootDocument(root)
        for (segment in segments) {
            val children = DocumentsContract.buildChildDocumentsUriUsingTree(root, DocumentsContract.getDocumentId(current))
            current = activity.contentResolver.query(children, arrayOf(DocumentsContract.Document.COLUMN_DOCUMENT_ID, DocumentsContract.Document.COLUMN_DISPLAY_NAME), null, null, null)?.use { cursor ->
                while (cursor.moveToNext()) if (cursor.getString(1) == segment)
                    return@use DocumentsContract.buildDocumentUriUsingTree(root, cursor.getString(0))
                null
            } ?: error("Android 本地媒体文件不可用。")
        }
        return current
    }

    private fun availableName(root: Uri, requested: String): String {
        val dot = requested.lastIndexOf('.')
        val stem = if (dot > 0) requested.substring(0, dot) else requested
        val ext = if (dot > 0) requested.substring(dot) else ""
        for (suffix in 0..9999) {
            val name = if (suffix == 0) requested else "$stem ($suffix)$ext"
            if (findChild(root, name) == null && findChild(root, ".$name.ohmycine-part") == null) return name
        }
        error("Android 下载目录没有可用文件名。")
    }

    private fun safeName(value: String): String {
        val name = value.trim()
        require(name.isNotBlank() && name.length <= 240 && name != "." && name != ".." && !name.contains('/') && !name.contains('\\') && !name.contains('\u0000')) {
            "Android 下载文件名无效。"
        }
        return name
    }

    private fun documentSize(uri: Uri): Long = queryLong(uri, DocumentsContract.Document.COLUMN_SIZE) ?: 0
    private fun documentEntityHash(uri: Uri, size: Long): String {
        val documentId = DocumentsContract.getDocumentId(uri)
        val modified = queryLong(uri, DocumentsContract.Document.COLUMN_LAST_MODIFIED) ?: -1
        val digest = MessageDigest.getInstance("SHA-256")
            .digest("$documentId\u0000$size\u0000$modified".toByteArray(Charsets.UTF_8))
        return digest.joinToString("") { byte -> "%02x".format(byte) }
    }
    private fun queryFlags(uri: Uri): Long = queryLong(uri, DocumentsContract.Document.COLUMN_FLAGS) ?: 0
    private fun queryLong(uri: Uri, column: String): Long? = activity.contentResolver.query(uri, arrayOf(column), null, null, null)?.use { cursor ->
        if (cursor.moveToFirst() && !cursor.isNull(0)) cursor.getLong(0) else null
    }
    private fun treeName(root: Uri): String = activity.contentResolver.query(rootDocument(root), arrayOf(DocumentsContract.Document.COLUMN_DISPLAY_NAME), null, null, null)?.use { cursor ->
        if (cursor.moveToFirst()) cursor.getString(0) else null
    } ?: "已授权目录"

    private fun resolve(invoke: Invoke, action: () -> Any) {
        try { invoke.resolveObject(action()) }
        catch (error: FileNotFoundException) { invoke.reject("Android 下载目录授权已失效，请重新选择目录。") }
        catch (error: SecurityException) { invoke.reject("Android 下载目录授权已失效，请重新选择目录。") }
        catch (error: Exception) { invoke.reject(error.message ?: "Android 下载存储操作失败。") }
    }
}
