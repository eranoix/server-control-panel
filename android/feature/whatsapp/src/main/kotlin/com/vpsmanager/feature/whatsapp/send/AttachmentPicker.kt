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
 * One attachment ready to hand to [MediaUploadWorker] -- a local, already
 * readable [file] (never a `content://` `Uri`: the generated OpenAPI
 * client's multipart operation only accepts a [java.io.File]) plus the
 * metadata [MediaUploadWorker]/the composer need before/while sending.
 */
data class PickedAttachment(
    val file: File,
    val filename: String,
    val mimeType: String?,
    val sizeBytes: Long,
    val msgType: String,
)

/**
 * Filename/mime/size resolved for one attachment, plus the `image|video|
 * audio|document` classification the BFF's `msg_type` field expects. [cursor]
 * is an [OpenableColumns] query result (may be `null`/empty -- content
 * providers are not required to support either column); [mimeType] comes
 * from [ContentResolver.getType], not the cursor, matching how Android
 * itself splits that lookup. Pure and Android-framework-light on purpose so
 * it is unit-testable against a fake [Cursor] for every picker source.
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

/** `image|video|audio` from the mime type's own type segment; anything else (including a null mime) is `document`. */
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
 * Copies [uri]'s bytes into [MediaCache.directory] under a collision-proof
 * name. Required because the generated client's multipart upload operation
 * takes a [java.io.File], never a `content://` `Uri` -- and reusing the
 * on-demand media cache directory means an about-to-be-sent file doesn't
 * need a second storage location (it is also where [MediaUploadWorker]'s
 * retry reads the same bytes back from, keyed off the message's own
 * `media.url`).
 */
private fun copyToLocalFile(context: Context, uri: Uri, filename: String): File {
    val dest = File(MediaCache.directory(context), "${UUID.randomUUID()}-$filename")
    val opened = context.contentResolver.openInputStream(uri) ?: error("Could not read the selected file.")
    opened.use { input -> dest.outputStream().use { output -> input.copyTo(output) } }
    return dest
}

/**
 * The composer's three attach entry points:
 * - [ActivityResultContracts.PickVisualMedia] for photo/video -- the system
 *   Photo Picker needs no `READ_EXTERNAL_STORAGE` runtime prompt on a modern
 *   target SDK (matches the project's "typing/permission friction is last
 *   resort" preference), and its returned `content://` grant is *not*
 *   persistable (attempting to persist it throws) -- valid for this
 *   process's lifetime, which this function respects by copying the bytes
 *   out immediately rather than deferring.
 * - [ActivityResultContracts.OpenDocument] for arbitrary files -- this grant
 *   *is* persistable, and [android.content.ContentResolver.takePersistableUriPermission]
 *   is called synchronously inside the activity-result callback, before any
 *   suspend/background hop, because a picked document's grant is otherwise
 *   transient and the copy below may not finish before the granting
 *   activity is gone (the same precedent the file-transfer work already ran
 *   into).
 * - An in-app [MediaRecorder] voice-note toggle, recording straight to the
 *   app's private cache -- no picker, no permission concern beyond
 *   `RECORD_AUDIO` itself, since nothing is shared/external until upload.
 *
 * Every entry point is disabled while [enabled] is `false` -- the same
 * "only one send in flight" invariant [ConversationScreen]'s text composer
 * already relies on.
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

    // ACCESSIBILITY: the content of these buttons is nothing but an emoji.
    // With no description, the screen reader announces the EMOJI'S NAME
    // ("paperclip") or nothing at all, and not the action — three controls
    // nobody can tell the purpose of. The description goes on the IconButton
    // itself because that is what receives the tap.
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
            // The description follows the STATE: the same button records and
            // stops, and "Gravar audio" while recording is already under way
            // would send a blind user back to the start instead of finishing.
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
 * Records a voice note straight to the app's private cache directory --
 * never shared/external storage, so no other app can read or tamper with
 * the bytes before send. [Context.startForegroundService]/notifications are
 * deliberately out of scope: recording only ever runs while this composable
 * (and the conversation screen it lives in) is foreground.
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
