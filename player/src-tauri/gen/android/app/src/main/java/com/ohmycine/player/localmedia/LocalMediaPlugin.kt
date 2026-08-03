package com.ohmycine.player.localmedia

import android.app.Activity
import android.content.Intent
import android.database.Cursor
import android.net.Uri
import android.provider.DocumentsContract
import android.provider.OpenableColumns
import androidx.activity.result.ActivityResult
import app.tauri.annotation.ActivityCallback
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.Plugin

@InvokeArg
class LocalEntryArgs {
    lateinit var rootPath: String
    var path: String? = null
}

@TauriPlugin
class LocalMediaPlugin(private val activity: Activity) : Plugin(activity) {
    @Command
    fun pickVideo(invoke: Invoke) {
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
            addCategory(Intent.CATEGORY_OPENABLE)
            type = "*/*"
            putExtra(Intent.EXTRA_MIME_TYPES, arrayOf(
                "video/*",
                "application/octet-stream",
                "application/x-iso9660-image",
            ))
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            addFlags(Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION)
        }
        startActivityForResult(invoke, intent, "pickVideoResult")
    }

    @ActivityCallback
    fun pickVideoResult(invoke: Invoke, result: ActivityResult) {
        resolvePickerResult(invoke, result, false)
    }

    @Command
    fun pickDirectory(invoke: Invoke) {
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            addFlags(Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION)
            addFlags(Intent.FLAG_GRANT_PREFIX_URI_PERMISSION)
        }
        startActivityForResult(invoke, intent, "pickDirectoryResult")
    }

    @ActivityCallback
    fun pickDirectoryResult(invoke: Invoke, result: ActivityResult) {
        resolvePickerResult(invoke, result, true)
    }

    @Command
    fun list(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(LocalEntryArgs::class.java)
        val root = requireReadableTree(args.rootPath)
        val directory = resolveDocument(root, args.path)
        require(isDirectory(directory)) { "本地文件目录不可用。" }
        queryChildren(root, directory, normalizeProviderPath(args.path)).map { it.toMap() }
    }

    @Command
    fun metadata(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(LocalEntryArgs::class.java)
        val root = requireReadableTree(args.rootPath)
        val document = resolveDocument(root, args.path)
        queryDocument(document, normalizeProviderPath(args.path)).toMap()
    }

    @Command
    fun streamPath(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(LocalEntryArgs::class.java)
        val root = requireReadableTree(args.rootPath)
        val document = resolveDocument(root, args.path)
        require(!isDirectory(document)) { "本地文件夹不能直接播放。" }
        document.toString()
    }

    private fun resolvePickerResult(invoke: Invoke, result: ActivityResult, directory: Boolean) {
        try {
            if (result.resultCode == Activity.RESULT_CANCELED) {
                invoke.resolveObject(mapOf("cancelled" to true))
                return
            }
            require(result.resultCode == Activity.RESULT_OK) { "Android 文件选择失败。" }
            val data = result.data ?: error("Android 文件选择未返回结果。")
            val uri = data.data ?: error("Android 文件选择未返回 URI。")
            persistReadPermission(uri, data.flags)
            val document = if (directory) treeDocumentUri(uri) else uri
            val entry = queryDocument(document, "/")
            invoke.resolveObject(mapOf(
                "cancelled" to false,
                "uri" to uri.toString(),
                "name" to entry.name,
                "size" to entry.size,
                "modifiedMs" to entry.modifiedMs,
            ))
        } catch (error: Exception) {
            invoke.reject(error.message ?: "Android 文件选择失败。")
        }
    }

    private fun persistReadPermission(uri: Uri, resultFlags: Int) {
        val flags = resultFlags and Intent.FLAG_GRANT_READ_URI_PERMISSION
        require(flags != 0) { "Android 未授予媒体读取权限。" }
        activity.contentResolver.takePersistableUriPermission(uri, flags)
    }

    private fun requireReadableTree(value: String): Uri {
        val root = Uri.parse(value.trim())
        require(root.scheme == "content" && DocumentsContract.isTreeUri(root)) {
            "Android 本地媒体目录无效。"
        }
        val readable = activity.contentResolver.persistedUriPermissions.any {
            it.uri == root && it.isReadPermission
        }
        require(readable) { "Android 本地媒体目录授权已失效，请重新选择目录。" }
        return root
    }

    private fun treeDocumentUri(root: Uri): Uri = DocumentsContract.buildDocumentUriUsingTree(
        root,
        DocumentsContract.getTreeDocumentId(root),
    )

    private fun resolveDocument(root: Uri, path: String?): Uri {
        val segments = normalizeProviderPath(path)
            .split('/')
            .filter { it.isNotEmpty() }
        var current = treeDocumentUri(root)
        for (segment in segments) {
            current = findChild(root, current, segment)
                ?: error("本地文件路径不可用。")
        }
        return current
    }

    private fun findChild(root: Uri, parent: Uri, name: String): Uri? {
        val parentId = DocumentsContract.getDocumentId(parent)
        val childrenUri = DocumentsContract.buildChildDocumentsUriUsingTree(root, parentId)
        return activity.contentResolver.query(
            childrenUri,
            arrayOf(
                DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                DocumentsContract.Document.COLUMN_DISPLAY_NAME,
            ),
            null,
            null,
            null,
        )?.use { cursor ->
            val idIndex = cursor.getColumnIndexOrThrow(DocumentsContract.Document.COLUMN_DOCUMENT_ID)
            val nameIndex = cursor.getColumnIndexOrThrow(DocumentsContract.Document.COLUMN_DISPLAY_NAME)
            while (cursor.moveToNext()) {
                if (cursor.getString(nameIndex) == name)
                    return@use DocumentsContract.buildDocumentUriUsingTree(root, cursor.getString(idIndex))
            }
            null
        }
    }

    private fun queryChildren(root: Uri, parent: Uri, parentPath: String): List<LocalDocumentEntry> {
        val childrenUri = DocumentsContract.buildChildDocumentsUriUsingTree(
            root,
            DocumentsContract.getDocumentId(parent),
        )
        val entries = mutableListOf<LocalDocumentEntry>()
        activity.contentResolver.query(childrenUri, DOCUMENT_PROJECTION, null, null, null)?.use { cursor ->
            while (cursor.moveToNext()) {
                val entry = entryFromCursor(cursor, parentPath, true)
                if (isSafeDisplayName(entry.name))
                    entries.add(entry)
            }
        } ?: error("本地文件目录读取失败。")
        return entries.sortedWith(compareByDescending<LocalDocumentEntry> { it.isDir }.thenBy { it.name.lowercase() })
    }

    private fun queryDocument(uri: Uri, providerPath: String): LocalDocumentEntry {
        return activity.contentResolver.query(uri, DOCUMENT_PROJECTION, null, null, null)?.use { cursor ->
            require(cursor.moveToFirst()) { "本地文件条目不可用。" }
            entryFromCursor(cursor, normalizeProviderPath(providerPath), false)
        } ?: error("本地文件条目不可用。")
    }

    private fun entryFromCursor(cursor: Cursor, providerPath: String, appendName: Boolean): LocalDocumentEntry {
        val name = cursor.stringValue(DocumentsContract.Document.COLUMN_DISPLAY_NAME)
            ?: cursor.stringValue(OpenableColumns.DISPLAY_NAME)
            ?: ""
        val mimeType = cursor.stringValue(DocumentsContract.Document.COLUMN_MIME_TYPE)
        val isDir = mimeType == DocumentsContract.Document.MIME_TYPE_DIR
        val path = if (!appendName) providerPath
        else if (providerPath == "/") "/$name"
        else "$providerPath/$name"
        return LocalDocumentEntry(
            name = name,
            path = if (name.isBlank()) providerPath else normalizeProviderPath(path),
            isDir = isDir,
            size = if (isDir) null else cursor.longValue(OpenableColumns.SIZE),
            modifiedMs = cursor.longValue(DocumentsContract.Document.COLUMN_LAST_MODIFIED),
        )
    }

    private fun isDirectory(uri: Uri): Boolean {
        return activity.contentResolver.query(
            uri,
            arrayOf(DocumentsContract.Document.COLUMN_MIME_TYPE),
            null,
            null,
            null,
        )?.use { cursor ->
            cursor.moveToFirst()
                && cursor.getString(0) == DocumentsContract.Document.MIME_TYPE_DIR
        } ?: false
    }

    private fun normalizeProviderPath(value: String?): String {
        val trimmed = value?.trim().orEmpty()
        if (trimmed.isEmpty() || trimmed == "/")
            return "/"
        require(!trimmed.contains('\u0000')) { "本地文件路径无效。" }
        val segments = trimmed.replace('\\', '/').split('/').filter { it.isNotEmpty() }
        require(segments.none { isUnsafeSegment(it) }) { "本地文件路径无效。" }
        return "/${segments.joinToString("/")}"
    }

    private fun isUnsafeSegment(segment: String): Boolean {
        var current = segment
        repeat(2) {
            if (current == "." || current == ".." || current.contains('/') || current.contains('\\'))
                return true
            val decoded = Uri.decode(current)
            if (decoded == current)
                return false
            current = decoded
        }
        return current == "." || current == ".." || current.contains('/') || current.contains('\\')
    }

    private fun isSafeDisplayName(name: String): Boolean {
        return name.isNotBlank()
            && !name.contains('\u0000')
            && !name.contains('/')
            && !name.contains('\\')
            && name != "."
            && name != ".."
    }

    private fun Cursor.stringValue(column: String): String? {
        val index = getColumnIndex(column)
        return if (index >= 0 && !isNull(index)) getString(index) else null
    }

    private fun Cursor.longValue(column: String): Long? {
        val index = getColumnIndex(column)
        return if (index >= 0 && !isNull(index)) getLong(index) else null
    }

    private fun resolve(invoke: Invoke, action: () -> Any) {
        try {
            invoke.resolveObject(action())
        } catch (error: Exception) {
            invoke.reject(error.message ?: "Android 本地媒体命令执行失败。")
        }
    }

    companion object {
        private val DOCUMENT_PROJECTION = arrayOf(
            DocumentsContract.Document.COLUMN_DOCUMENT_ID,
            DocumentsContract.Document.COLUMN_DISPLAY_NAME,
            DocumentsContract.Document.COLUMN_MIME_TYPE,
            DocumentsContract.Document.COLUMN_SIZE,
            DocumentsContract.Document.COLUMN_LAST_MODIFIED,
        )
    }
}

private data class LocalDocumentEntry(
    val name: String,
    val path: String,
    val isDir: Boolean,
    val size: Long?,
    val modifiedMs: Long?,
) {
    fun toMap(): Map<String, Any?> = mapOf(
        "name" to name,
        "path" to path,
        "isDir" to isDir,
        "size" to size,
        "modifiedMs" to modifiedMs,
    )
}
