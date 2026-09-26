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
 * The three ways to hand a reference to the agent on the other side of the session:
 *
 * - Pick a file (`OpenMultipleDocuments`): any provider and any type; its grant is
 *   persistable, so it is claimed right away (see [takePersistableRead]).
 * - Pick an image (`PickMultipleVisualMedia`): the Photo Picker, which needs no
 *   storage permission.
 * - Take a photo now (`TakePicture`): the camera writes into a file we provide.
 *   Needs the runtime CAMERA permission, because declaring `CAMERA` in the
 *   manifest makes it enforced even for `ACTION_IMAGE_CAPTURE`.
 *
 * All accept multiple items (the camera one photo at a time, each queued at once).
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
 * The content kept apart from the `ModalBottomSheet` wrapper so its rules can be
 * tested on the JVM without window animations (as with `TerminalOptionsContent`).
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
            // Claim here, inside the callback: the document grant is transient and
            // the upload worker may run after this screen and the source app are gone.
            uris.forEach { takePersistableRead(context.contentResolver, it) }
            onChoose(uris.map { resolveAttachment(context, it) })
            onClose()
        }
    }

    val pickImages = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(),
    ) { uris ->
        if (uris.isNotEmpty()) {
            // Photo Picker grants cannot be persisted (it throws) and last only
            // for this process, so copy into the cache now.
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
            // Camera cancelled or wrote nothing: delete the empty file.
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
 * The camera is another app, so it needs a writable `content://` URI, never a
 * `file://` (`FileUriExposedException`). The module's `FileProvider` is scoped to
 * the [PHOTOS_DIR] cache subfolder.
 */
private fun contentUriFor(context: Context, file: File): Uri =
    FileProvider.getUriForFile(context, "${context.packageName}.terminalattach.fileprovider", file)

// The authority matches this module's AndroidManifest; the provider is
// [AttachmentFileProvider] (see its doc for why it is a subclass).

private fun takePersistableRead(contentResolver: ContentResolver, uri: Uri) {
    try {
        contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
    } catch (e: SecurityException) {
        // Not every provider grants persistable access. Carry on with the
        // transient grant; if it expires the upload fails visibly with a reason.
    }
}

/**
 * Copies the bytes into the app cache. Only needed for the Photo Picker, whose
 * grant is not persistable, so a deferred upload would find the `Uri` revoked.
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
        // Fall back to the original Uri, which may still work.
        metadata
    } catch (e: SecurityException) {
        metadata
    }
}

/**
 * Name and size as the provider reports them. Either may be missing: the name
 * falls back to the `Uri`'s last segment and the size to `0`, shown as
 * indeterminate progress.
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
        // Grant already revoked: use the fallback values.
    }
    return LocalAttachment(
        uri = uri.toString(),
        displayName = name ?: uri.lastPathSegment?.substringAfterLast('/') ?: "file",
        sizeBytes = size,
    )
}
