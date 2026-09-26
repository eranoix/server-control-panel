package com.vpsmanager.feature.terminal.attach

import android.Manifest
import android.content.ContentResolver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.provider.OpenableColumns
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.core.content.FileProvider
import java.io.File

/** Test tag for the attachment source sheet. */
const val ATTACHMENT_SHEET_TAG = "folha-origem-anexo"

/** Label of the item that opens the attachment sheet, in the terminal's options sheet. */
const val ATTACH_LABEL = "Attach file or image"

/** Test tag for the attachment item inside the options sheet. */
const val ATTACH_TAG = "botao-anexar-terminal"

internal const val CHOOSE_FILE_LABEL = "Choose file"
internal const val CHOOSE_IMAGE_LABEL = "Choose image from gallery"
internal const val TAKE_PHOTO_LABEL = "Take a photo now"

/** The cache subfolder a freshly taken photo lands in before it uploads. */
private const val PHOTOS_DIR = "anexos-terminal"

/**
 * The three ways of handing a reference to the assistant on the other side of
 * the session, in the order they are actually used on a phone:
 *
 * - **Pick a file** (`OpenMultipleDocuments`): the system picker, which
 *   reaches any installed provider (Drive, Files, a third-party manager). It
 *   is the only path that accepts *any* type — a `.log`, a `.tar.gz` — and its
 *   grant is persistable, so it is claimed immediately (see
 *   [takePersistableRead]).
 * - **Pick an image** (`PickMultipleVisualMedia`): the system Photo Picker.
 *   Chosen over an `OpenDocument` filtered to image types because it requires
 *   NO storage permission at all — no dialog asking for access to every photo
 *   in order to send one.
 * - **Take a photo now** (`TakePicture`): the most useful of the three for
 *   "give you a reference to look at" — photographing a screen, an error on a
 *   monitor, a whiteboard. It needs a destination of its own (the camera
 *   writes INTO the file we point it at) and the runtime camera permission,
 *   because this app declares `CAMERA` in the manifest — declaring it makes
 *   the permission enforceable even for `ACTION_IMAGE_CAPTURE`, which on its
 *   own would not need it.
 *
 * All three accept MULTIPLE items (the camera one photo at a time, but each
 * one goes off into the queue without waiting for the previous).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AttachmentSourceSheet(
    onChoose: (List<LocalAttachment>) -> Unit,
    onClose: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = onClose,
        sheetState = sheetState,
        modifier = Modifier.testTag(ATTACHMENT_SHEET_TAG),
    ) {
        SourceSheetContent(onChoose = onChoose, onClose = onClose)
    }
}

/**
 * The content kept apart from the `ModalBottomSheet` wrapper: the wrapper is a
 * system window and the content is what carries the rules (which sources
 * exist, what each one fires). Separated, the content is exercisable straight
 * from a JVM test without depending on a window's animation — the same reason
 * `TerminalOptionsContent` exists apart from `TerminalOptionsSheet`.
 */
@Composable
internal fun SourceSheetContent(
    onChoose: (List<LocalAttachment>) -> Unit,
    onClose: () -> Unit,
) {
    val context = LocalContext.current
    val photoDestination = remember { mutableStateOf<File?>(null) }

    val pickFiles = rememberLauncherForActivityResult(
        ActivityResultContracts.OpenMultipleDocuments(),
    ) { uris ->
        if (uris.isNotEmpty()) {
            // Claimed HERE, inside the callback, before any hop to the
            // background: the grant on a picked document is transient, and the
            // AttachmentUploadWorker may run well after this screen (and the app
            // that supplied the file) have ceased to exist.
            uris.forEach { takePersistableRead(context.contentResolver, it) }
            onChoose(uris.map { resolveAttachment(context, it) })
            onClose()
        }
    }

    val pickImages = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(),
    ) { uris ->
        if (uris.isNotEmpty()) {
            // The Photo Picker's grant is NOT persistable (trying to persist
            // it throws) — it lasts as long as this process does. That is why
            // the copy into the app's cache happens now, rather than being
            // left to the worker.
            onChoose(uris.map { copyToCache(context, it) })
            onClose()
        }
    }

    val takePhoto = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { succeeded ->
        val file = photoDestination.value
        photoDestination.value = null
        if (succeeded && file != null && file.length() > 0) {
            onChoose(
                listOf(
                    LocalAttachment(
                        uri = Uri.fromFile(file).toString(),
                        displayName = file.name,
                        sizeBytes = file.length(),
                    ),
                ),
            )
            onClose()
        } else {
            // A cancelled camera (or one that wrote nothing): the empty file
            // already created for it must not sit there taking up cache.
            file?.delete()
        }
    }

    val launchCamera: () -> Unit = {
        val file = newPhotoFile(context)
        photoDestination.value = file
        takePhoto.launch(contentUriFor(context, file))
    }

    val requestCamera = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) launchCamera() else onClose()
    }

    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 24.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(text = "Attach to session", style = MaterialTheme.typography.titleMedium)
        Text(
            text = "The file goes to the server's inbox folder and its path is ready " +
                "for you to insert on the command line.",
            style = MaterialTheme.typography.bodySmall,
        )
        OutlinedButton(
            onClick = { pickFiles.launch(arrayOf("*/*")) },
            modifier = Modifier.fillMaxWidth().testTag(CHOOSE_FILE_LABEL),
        ) { Text(text = CHOOSE_FILE_LABEL) }
        OutlinedButton(
            onClick = {
                pickImages.launch(
                    PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
                )
            },
            modifier = Modifier.fillMaxWidth().testTag(CHOOSE_IMAGE_LABEL),
        ) { Text(text = CHOOSE_IMAGE_LABEL) }
        OutlinedButton(
            onClick = {
                if (hasCameraPermission(context)) launchCamera() else requestCamera.launch(Manifest.permission.CAMERA)
            },
            modifier = Modifier.fillMaxWidth().testTag(TAKE_PHOTO_LABEL),
        ) { Text(text = TAKE_PHOTO_LABEL) }
    }
}

internal fun hasCameraPermission(context: Context): Boolean =
    ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

private fun newPhotoFile(context: Context): File {
    val dir = File(context.cacheDir, PHOTOS_DIR).apply { mkdirs() }
    return File(dir, "foto-${System.currentTimeMillis()}.jpg")
}

/**
 * The camera is ANOTHER app: it needs a `content://` with write permission,
 * never a `file://` (which would throw `FileUriExposedException`). The
 * `FileProvider` declared in this module's manifest is scoped to the
 * [PHOTOS_DIR] cache subfolder alone.
 */
private fun contentUriFor(context: Context, file: File): Uri =
    FileProvider.getUriForFile(context, "${context.packageName}.terminalattach.fileprovider", file)

// The authority above matches the one declared in this module's
// AndroidManifest, and the provider behind it is [AttachmentFileProvider] — see
// that class's doc for why it cannot be androidx's FileProvider directly.

private fun takePersistableRead(contentResolver: ContentResolver, uri: Uri) {
    try {
        contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
    } catch (e: SecurityException) {
        // Not every provider grants a persistable permission. Carry on with
        // the transient one: if it does not survive until the worker runs, the
        // upload fails VISIBLY (the attachment bar shows the reason) instead of
        // disappearing in silence.
    }
}

/**
 * Copies the bytes into the app's cache. Only necessary for the Photo Picker,
 * whose grant is not persistable: without the copy, an upload that WorkManager
 * deferred (no network) would find an already-revoked `Uri` when it came to
 * read.
 */
private fun copyToCache(context: Context, uri: Uri): LocalAttachment {
    val metadata = resolveAttachment(context, uri)
    val dir = File(context.cacheDir, PHOTOS_DIR).apply { mkdirs() }
    val destination = File(dir, "${System.currentTimeMillis()}-${metadata.displayName}")
    return try {
        context.contentResolver.openInputStream(uri)?.use { entry ->
            destination.outputStream().use { output -> entry.copyTo(output) }
        } ?: return metadata
        LocalAttachment(
            uri = Uri.fromFile(destination).toString(),
            displayName = metadata.displayName,
            sizeBytes = destination.length(),
        )
    } catch (e: java.io.IOException) {
        // Without the copy, the upload may still succeed through the original
        // Uri if it survives — better to try than to discard the operator's
        // choice.
        metadata
    } catch (e: SecurityException) {
        metadata
    }
}

/**
 * Name and size as the provider reports them. Both can be missing (no provider
 * is obliged to answer those columns): the name falls back to the `Uri`'s last
 * segment and the size to `0`, which the attachment bar shows as
 * indeterminate progress rather than inventing a percentage.
 */
internal fun resolveAttachment(context: Context, uri: Uri): LocalAttachment {
    var name: String? = null
    var size = 0L
    try {
        context.contentResolver.query(uri, null, null, null, null)?.use { cursor ->
            val nameIndex = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
            val sizeIndex = cursor.getColumnIndex(OpenableColumns.SIZE)
            if (cursor.moveToFirst()) {
                if (nameIndex >= 0) name = cursor.getString(nameIndex)
                if (sizeIndex >= 0 && !cursor.isNull(sizeIndex)) size = cursor.getLong(sizeIndex)
            }
        }
    } catch (e: SecurityException) {
        // The grant is already revoked: carry on with the fallback values.
    }
    return LocalAttachment(
        uri = uri.toString(),
        displayName = name ?: uri.lastPathSegment?.substringAfterLast('/') ?: "file",
        sizeBytes = size,
    )
}
