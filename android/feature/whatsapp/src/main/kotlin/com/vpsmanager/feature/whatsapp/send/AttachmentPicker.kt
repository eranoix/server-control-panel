package com.vpsmanager.feature.whatsapp.send

import android.Manifest
import android.content.ContentResolver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.database.Cursor
import android.media.MediaRecorder
import android.net.Uri
import android.os.Build
import android.provider.OpenableColumns
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Row
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import com.vpsmanager.feature.whatsapp.media.MediaCache
import java.io.File
import java.util.UUID
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * An attachment ready for [MediaUploadWorker]: a local [file] (the generated client's multipart
 * upload only accepts a [java.io.File], not a `content://` Uri) plus its metadata.
 */
data class PickedAttachment(
    val file: File,
    val filename: String,
    val mimeType: String?,
    val sizeBytes: Long,
    val msgType: String,
)

/**
 * Filename, mime type and size of an attachment, plus the `msg_type` classification the server
 * expects. The [OpenableColumns] cursor may be null or lack columns; the mime type comes from
 * [ContentResolver.getType]. Kept framework-light so it is unit-testable with a fake [Cursor].
 */
internal data class AttachmentMetadata(
    val filename: String,
    val mimeType: String?,
    val sizeBytes: Long?,
    val msgType: String,
)

internal fun resolveAttachmentMetadata(cursor: Cursor?, mimeType: String?, fallbackName: String): AttachmentMetadata {
    var filename = fallbackName
    var sizeBytes: Long? = null
    if (cursor != null && cursor.moveToFirst()) {
        val nameIndex = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
        if (nameIndex >= 0) {
            cursor.getString(nameIndex)?.takeIf { it.isNotBlank() }?.let { filename = it }
        }
        val sizeIndex = cursor.getColumnIndex(OpenableColumns.SIZE)
        if (sizeIndex >= 0 && !cursor.isNull(sizeIndex)) {
            sizeBytes = cursor.getLong(sizeIndex)
        }
    }
    return AttachmentMetadata(filename = filename, mimeType = mimeType, sizeBytes = sizeBytes, msgType = msgTypeFor(mimeType))
}

/** Maps the mime type to image, video or audio; anything else, including null, is `document`. */
internal fun msgTypeFor(mimeType: String?): String = when {
    mimeType == null -> "document"
    mimeType.startsWith("image/") -> "image"
    mimeType.startsWith("video/") -> "video"
    mimeType.startsWith("audio/") -> "audio"
    else -> "document"
}

private fun queryMetadata(contentResolver: ContentResolver, uri: Uri): AttachmentMetadata {
    val fallback = uri.lastPathSegment?.substringAfterLast('/') ?: "arquivo"
    val mimeType = contentResolver.getType(uri)
    val cursor = contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)
    return cursor.use { resolveAttachmentMetadata(it, mimeType, fallback) }
}

/**
 * Copies [uri]'s bytes into [MediaCache.directory] under a unique name, since the upload needs
 * a [java.io.File]. Retries in [MediaUploadWorker] read the same bytes back from there.
 */
private fun copyToLocalFile(context: Context, uri: Uri, filename: String): File {
    val dest = File(MediaCache.directory(context), "${UUID.randomUUID()}-$filename")
    val opened = context.contentResolver.openInputStream(uri) ?: error("Could not read the selected file.")
    opened.use { input -> dest.outputStream().use { output -> input.copyTo(output) } }
    return dest
}

/**
 * The composer's attach buttons:
 * - Photo Picker for photo/video: no storage permission needed, but its grant is not
 *   persistable (persisting throws), so the bytes are copied out immediately.
 * - OpenDocument for any file: the grant is persisted synchronously in the result callback,
 *   before any background hop, or it may expire before the copy finishes.
 * - A [MediaRecorder] voice note recorded to the app's private cache.
 *
 * All are disabled while [enabled] is false, keeping one send in flight at a time.
 */
@Composable
fun AttachmentBar(
    enabled: Boolean,
    onAttachmentReady: (PickedAttachment) -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    val photoVideoLauncher = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        if (uri == null) return@rememberLauncherForActivityResult
        scope.launch {
            val attachment = withContext(Dispatchers.IO) {
                val meta = queryMetadata(context.contentResolver, uri)
                val file = copyToLocalFile(context, uri, meta.filename)
                PickedAttachment(
                    file = file,
                    filename = meta.filename,
                    mimeType = meta.mimeType,
                    sizeBytes = meta.sizeBytes ?: file.length(),
                    msgType = meta.msgType,
                )
            }
            onAttachmentReady(attachment)
        }
    }

    val documentLauncher = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri == null) return@rememberLauncherForActivityResult
        context.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
        scope.launch {
            val attachment = withContext(Dispatchers.IO) {
                val meta = queryMetadata(context.contentResolver, uri)
                val file = copyToLocalFile(context, uri, meta.filename)
                PickedAttachment(
                    file = file,
                    filename = meta.filename,
                    mimeType = meta.mimeType,
                    sizeBytes = meta.sizeBytes ?: file.length(),
                    msgType = "document",
                )
            }
            onAttachmentReady(attachment)
        }
    }

    var recording by remember { mutableStateOf<AudioRecorderSession?>(null) }
    val recordPermissionLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) recording = AudioRecorderSession.start(context)
    }

    // The buttons show only emoji, so each IconButton (the tap target) needs a
    // content description or screen readers announce the emoji name.
    Row {
        IconButton(
            enabled = enabled,
            onClick = { documentLauncher.launch(arrayOf("*/*")) },
            modifier = Modifier.semantics { contentDescription = "Attach file" },
        ) {
            Text("📎")
        }
        IconButton(
            enabled = enabled,
            onClick = {
                photoVideoLauncher.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo))
            },
            modifier = Modifier.semantics { contentDescription = "Attach photo or video" },
        ) {
            Text("🖼")
        }
        IconButton(
            enabled = enabled,
            onClick = {
                val active = recording
                if (active == null) {
                    if (ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
                        recording = AudioRecorderSession.start(context)
                    } else {
                        recordPermissionLauncher.launch(Manifest.permission.RECORD_AUDIO)
                    }
                } else {
                    val file = active.stop()
                    recording = null
                    onAttachmentReady(
                        PickedAttachment(
                            file = file,
                            filename = file.name,
                            mimeType = "audio/mp4",
                            sizeBytes = file.length(),
                            msgType = "audio",
                        ),
                    )
                }
            },
            // The same button records and stops, so the description must follow the state.
            modifier = Modifier.semantics {
                contentDescription =
                    if (recording == null) "Record audio" else "Stop recording and attach"
            },
        ) {
            Text(if (recording == null) "🎤" else "⏹")
        }
    }
}

/**
 * Records a voice note to the app's private cache so no other app can read or alter it.
 * No foreground service: recording only runs while the conversation screen is in front.
 */
internal class AudioRecorderSession private constructor(private val recorder: MediaRecorder, private val file: File) {
    fun stop(): File {
        recorder.stop()
        recorder.release()
        return file
    }

    companion object {
        fun start(context: Context): AudioRecorderSession {
            val file = File(MediaCache.directory(context), "voice-${UUID.randomUUID()}.m4a")
            @Suppress("DEPRECATION")
            val recorder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) MediaRecorder(context) else MediaRecorder()
            recorder.apply {
                setAudioSource(MediaRecorder.AudioSource.MIC)
                setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
                setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
                setOutputFile(file.absolutePath)
                prepare()
                start()
            }
            return AudioRecorderSession(recorder, file)
        }
    }
}
