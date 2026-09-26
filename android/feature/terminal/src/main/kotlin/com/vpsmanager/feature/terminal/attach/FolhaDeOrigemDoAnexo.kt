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
const val FOLHA_ANEXO_TAG = "folha-origem-anexo"

/** Label of the item that opens the attachment sheet, in the terminal's options sheet. */
const val ANEXAR_LABEL = "Attach file or image"

/** Test tag for the attachment item inside the options sheet. */
const val ANEXAR_TAG = "botao-anexar-terminal"

internal const val ESCOLHER_ARQUIVO_LABEL = "Choose file"
internal const val ESCOLHER_IMAGEM_LABEL = "Choose image from gallery"
internal const val TIRAR_FOTO_LABEL = "Take a photo now"

/** The cache subfolder a freshly taken photo lands in before it uploads. */
private const val PASTA_DE_FOTOS = "anexos-terminal"

/**
 * The three ways of handing a reference to the assistant on the other side of
 * the session, in the order they are actually used on a phone:
 *
 * - **Pick a file** (`OpenMultipleDocuments`): the system picker, which
 *   reaches any installed provider (Drive, Files, a third-party manager). It
 *   is the only path that accepts *any* type — a `.log`, a `.tar.gz` — and its
 *   grant is persistable, so it is claimed immediately (see
 *   [reivindicarLeituraPersistente]).
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
fun FolhaDeOrigemDoAnexo(
    aoEscolher: (List<AnexoLocal>) -> Unit,
    aoFechar: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = aoFechar,
        sheetState = sheetState,
        modifier = Modifier.testTag(FOLHA_ANEXO_TAG),
    ) {
        ConteudoDaFolhaDeOrigem(aoEscolher = aoEscolher, aoFechar = aoFechar)
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
internal fun ConteudoDaFolhaDeOrigem(
    aoEscolher: (List<AnexoLocal>) -> Unit,
    aoFechar: () -> Unit,
) {
    val context = LocalContext.current
    val destinoDaFoto = remember { mutableStateOf<File?>(null) }

    val escolherArquivos = rememberLauncherForActivityResult(
        ActivityResultContracts.OpenMultipleDocuments(),
    ) { uris ->
        if (uris.isNotEmpty()) {
            // Claimed HERE, inside the callback, before any hop to the
            // background: the grant on a picked document is transient, and the
            // AnexoUploadWorker may run well after this screen (and the app
            // that supplied the file) have ceased to exist.
            uris.forEach { reivindicarLeituraPersistente(context.contentResolver, it) }
            aoEscolher(uris.map { resolverAnexo(context, it) })
            aoFechar()
        }
    }

    val escolherImagens = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(),
    ) { uris ->
        if (uris.isNotEmpty()) {
            // The Photo Picker's grant is NOT persistable (trying to persist
            // it throws) — it lasts as long as this process does. That is why
            // the copy into the app's cache happens now, rather than being
            // left to the worker.
            aoEscolher(uris.map { copiarParaCache(context, it) })
            aoFechar()
        }
    }

    val tirarFoto = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { deuCerto ->
        val arquivo = destinoDaFoto.value
        destinoDaFoto.value = null
        if (deuCerto && arquivo != null && arquivo.length() > 0) {
            aoEscolher(
                listOf(
                    AnexoLocal(
                        uri = Uri.fromFile(arquivo).toString(),
                        nomeExibido = arquivo.name,
                        tamanhoBytes = arquivo.length(),
                    ),
                ),
            )
            aoFechar()
        } else {
            // A cancelled camera (or one that wrote nothing): the empty file
            // already created for it must not sit there taking up cache.
            arquivo?.delete()
        }
    }

    val dispararCamera: () -> Unit = {
        val arquivo = novoArquivoDeFoto(context)
        destinoDaFoto.value = arquivo
        tirarFoto.launch(uriDeConteudoPara(context, arquivo))
    }

    val pedirCamera = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { concedida ->
        if (concedida) dispararCamera() else aoFechar()
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
            onClick = { escolherArquivos.launch(arrayOf("*/*")) },
            modifier = Modifier.fillMaxWidth().testTag(ESCOLHER_ARQUIVO_LABEL),
        ) { Text(text = ESCOLHER_ARQUIVO_LABEL) }
        OutlinedButton(
            onClick = {
                escolherImagens.launch(
                    PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
                )
            },
            modifier = Modifier.fillMaxWidth().testTag(ESCOLHER_IMAGEM_LABEL),
        ) { Text(text = ESCOLHER_IMAGEM_LABEL) }
        OutlinedButton(
            onClick = {
                if (temPermissaoDeCamera(context)) dispararCamera() else pedirCamera.launch(Manifest.permission.CAMERA)
            },
            modifier = Modifier.fillMaxWidth().testTag(TIRAR_FOTO_LABEL),
        ) { Text(text = TIRAR_FOTO_LABEL) }
    }
}

internal fun temPermissaoDeCamera(context: Context): Boolean =
    ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

private fun novoArquivoDeFoto(context: Context): File {
    val pasta = File(context.cacheDir, PASTA_DE_FOTOS).apply { mkdirs() }
    return File(pasta, "foto-${System.currentTimeMillis()}.jpg")
}

/**
 * The camera is ANOTHER app: it needs a `content://` with write permission,
 * never a `file://` (which would throw `FileUriExposedException`). The
 * `FileProvider` declared in this module's manifest is scoped to the
 * [PASTA_DE_FOTOS] cache subfolder alone.
 */
private fun uriDeConteudoPara(context: Context, arquivo: File): Uri =
    FileProvider.getUriForFile(context, "${context.packageName}.terminalattach.fileprovider", arquivo)

// The authority above matches the one declared in this module's
// AndroidManifest, and the provider behind it is [AnexoFileProvider] — see
// that class's doc for why it cannot be androidx's FileProvider directly.

private fun reivindicarLeituraPersistente(contentResolver: ContentResolver, uri: Uri) {
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
private fun copiarParaCache(context: Context, uri: Uri): AnexoLocal {
    val metadados = resolverAnexo(context, uri)
    val pasta = File(context.cacheDir, PASTA_DE_FOTOS).apply { mkdirs() }
    val destino = File(pasta, "${System.currentTimeMillis()}-${metadados.nomeExibido}")
    return try {
        context.contentResolver.openInputStream(uri)?.use { entrada ->
            destino.outputStream().use { saida -> entrada.copyTo(saida) }
        } ?: return metadados
        AnexoLocal(
            uri = Uri.fromFile(destino).toString(),
            nomeExibido = metadados.nomeExibido,
            tamanhoBytes = destino.length(),
        )
    } catch (e: java.io.IOException) {
        // Without the copy, the upload may still succeed through the original
        // Uri if it survives — better to try than to discard the operator's
        // choice.
        metadados
    } catch (e: SecurityException) {
        metadados
    }
}

/**
 * Name and size as the provider reports them. Both can be missing (no provider
 * is obliged to answer those columns): the name falls back to the `Uri`'s last
 * segment and the size to `0`, which the attachment bar shows as
 * indeterminate progress rather than inventing a percentage.
 */
internal fun resolverAnexo(context: Context, uri: Uri): AnexoLocal {
    var nome: String? = null
    var tamanho = 0L
    try {
        context.contentResolver.query(uri, null, null, null, null)?.use { cursor ->
            val indiceNome = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
            val indiceTamanho = cursor.getColumnIndex(OpenableColumns.SIZE)
            if (cursor.moveToFirst()) {
                if (indiceNome >= 0) nome = cursor.getString(indiceNome)
                if (indiceTamanho >= 0 && !cursor.isNull(indiceTamanho)) tamanho = cursor.getLong(indiceTamanho)
            }
        }
    } catch (e: SecurityException) {
        // The grant is already revoked: carry on with the fallback values.
    }
    return AnexoLocal(
        uri = uri.toString(),
        nomeExibido = nome ?: uri.lastPathSegment?.substringAfterLast('/') ?: "file",
        tamanhoBytes = tamanho,
    )
}
