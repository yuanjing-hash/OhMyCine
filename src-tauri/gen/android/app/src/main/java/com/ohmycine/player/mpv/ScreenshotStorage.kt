package com.ohmycine.player.mpv

import android.app.Activity
import android.content.ContentValues
import android.media.MediaScannerConnection
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.DocumentsContract
import android.provider.MediaStore
import java.io.File
import java.io.RandomAccessFile

internal object ScreenshotStorage {
    private const val FOLDER = "OhMyCine"

    @Synchronized
    fun capture(activity: Activity, args: ScreenshotArgs): Map<String, String> {
        require(args.format == "png" || args.format == "jpg") { "Unsupported screenshot format." }
        val stem = safeStem(args)
        val mime = if (args.format == "png") "image/png" else "image/jpeg"
        val temporary = File.createTempFile("ohmycine-frame-", "." + args.format, activity.cacheDir)
        try {
            MpvSurfaceHost.screenshotToFile(temporary.absolutePath)
            var ready = false
            for (attempt in 0 until 200) {
                if (fileComplete(temporary, args.format)) {
                    ready = true
                    break
                }
                Thread.sleep(50)
            }
            check(ready) { "Screenshot write timed out." }
            return if (args.directoryUri.isNullOrBlank()) {
                saveDefault(activity, temporary, stem, args.format, mime)
            } else {
                saveToTree(activity, temporary, stem, args.format, mime, args.directoryUri!!)
            }
        } finally {
            temporary.delete()
        }
    }

    private fun fileComplete(file: File, format: String): Boolean = runCatching {
        val marker = if (format == "png")
            byteArrayOf(73, 69, 78, 68, 0xae.toByte(), 66, 96, 0x82.toByte())
        else
            byteArrayOf(0xff.toByte(), 0xd9.toByte())
        RandomAccessFile(file, "r").use { input ->
            if (input.length() < marker.size) return@runCatching false
            input.seek(input.length() - marker.size)
            val tail = ByteArray(marker.size)
            input.readFully(tail)
            tail.contentEquals(marker)
        }
    }.getOrDefault(false)

    private fun safeStem(args: ScreenshotArgs): String {
        val forbidden = "<>:\"/\\|?*"
        val clean = args.title.asSequence().take(80)
            .map { if (Character.isISOControl(it) || forbidden.contains(it)) ' ' else it }
            .joinToString("").trim().trim('.').trim().ifBlank { "Video" }
        val episode = when {
            args.seasonNumber != null && args.episodeNumber != null
                && args.seasonNumber!! >= 0 && args.episodeNumber!! >= 0 ->
                "-S" + args.seasonNumber.toString().padStart(2, '0') +
                    "E" + args.episodeNumber.toString().padStart(2, '0')
            args.episodeNumber != null && args.episodeNumber!! >= 0 ->
                "-E" + args.episodeNumber.toString().padStart(2, '0')
            else -> ""
        }
        return clean + episode + "-\u622a\u56fe"
    }

    private fun nextName(stem: String, format: String, occupied: Set<String>): String {
        for (number in 1..9999) {
            val name = stem + number.toString().padStart(4, '0') + "." + format
            if (name !in occupied)
                return name
        }
        error("Screenshot numbers are exhausted.")
    }

    private fun saveDefault(activity: Activity, source: File, stem: String, format: String, mime: String): Map<String, String> {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            @Suppress("DEPRECATION")
            val directory = File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_PICTURES), FOLDER)
            check(directory.isDirectory || directory.mkdirs()) { "Cannot create screenshot directory." }
            val name = nextName(stem, format, directory.list()?.toSet().orEmpty())
            val target = File(directory, name)
            source.copyTo(target)
            MediaScannerConnection.scanFile(activity, arrayOf(target.absolutePath), arrayOf(mime), null)
            return mapOf("name" to name, "path" to target.absolutePath)
        }

        val resolver = activity.contentResolver
        val collection = MediaStore.Images.Media.EXTERNAL_CONTENT_URI
        val relative = Environment.DIRECTORY_PICTURES + "/" + FOLDER + "/"
        val occupied = mutableSetOf<String>()
        resolver.query(collection, arrayOf(MediaStore.Images.Media.DISPLAY_NAME),
            MediaStore.Images.Media.RELATIVE_PATH + " = ?", arrayOf(relative), null)?.use { cursor ->
            while (cursor.moveToNext())
                occupied.add(cursor.getString(0))
        }
        val name = nextName(stem, format, occupied)
        val values = ContentValues().apply {
            put(MediaStore.Images.Media.DISPLAY_NAME, name)
            put(MediaStore.Images.Media.MIME_TYPE, mime)
            put(MediaStore.Images.Media.RELATIVE_PATH, relative)
            put(MediaStore.Images.Media.IS_PENDING, 1)
        }
        val target = resolver.insert(collection, values) ?: error("Cannot create screenshot.")
        try {
            resolver.openOutputStream(target, "w")?.use { output ->
                source.inputStream().use { input -> input.copyTo(output) }
            } ?: error("Cannot write screenshot.")
            resolver.update(target, ContentValues().apply {
                put(MediaStore.Images.Media.IS_PENDING, 0)
            }, null, null)
            return mapOf("name" to name, "path" to target.toString())
        } catch (error: Exception) {
            resolver.delete(target, null, null)
            throw error
        }
    }

    private fun saveToTree(activity: Activity, source: File, stem: String, format: String, mime: String, directoryUri: String): Map<String, String> {
        val root = Uri.parse(directoryUri.trim())
        require(root.scheme == "content" && DocumentsContract.isTreeUri(root)) { "Screenshot directory grant is invalid." }
        val resolver = activity.contentResolver
        require(resolver.persistedUriPermissions.any { it.uri == root && it.isReadPermission && it.isWritePermission }) {
            "Screenshot directory grant expired."
        }
        val documentId = DocumentsContract.getTreeDocumentId(root)
        val document = DocumentsContract.buildDocumentUriUsingTree(root, documentId)
        val children = DocumentsContract.buildChildDocumentsUriUsingTree(root, documentId)
        val occupied = mutableSetOf<String>()
        resolver.query(children, arrayOf(DocumentsContract.Document.COLUMN_DISPLAY_NAME), null, null, null)?.use { cursor ->
            while (cursor.moveToNext())
                occupied.add(cursor.getString(0))
        }
        val name = nextName(stem, format, occupied)
        val target = DocumentsContract.createDocument(resolver, document, mime, name)
            ?: error("Cannot create screenshot in selected directory.")
        try {
            resolver.openOutputStream(target, "w")?.use { output ->
                source.inputStream().use { input -> input.copyTo(output) }
            } ?: error("Cannot write screenshot in selected directory.")
            return mapOf("name" to name, "path" to target.toString())
        } catch (error: Exception) {
            DocumentsContract.deleteDocument(resolver, target)
            throw error
        }
    }
}
