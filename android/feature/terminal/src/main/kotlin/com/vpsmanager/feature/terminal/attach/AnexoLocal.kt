package com.vpsmanager.feature.terminal.attach

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * A file from the device, already chosen and ready to become an attachment:
 * the [uri] the `ContentResolver` knows how to open, the name the picker
 * showed and the size (when the provider reports it — not all of them do).
 */
data class AnexoLocal(
    val uri: String,
    val nomeExibido: String,
    val tamanhoBytes: Long,
)

/** Name used when the content provider exposes no `DISPLAY_NAME` at all. */
private const val NOME_PADRAO = "anexo"

private val CARIMBO = DateTimeFormatter.ofPattern("yyyyMMdd-HHmmss")

/**
 * The name the file lands under in the server's inbound folder.
 *
 * **Why stamp the date instead of keeping the original name.** The server
 * finishes the upload with an `os.Rename` to `dest_dir/filename`, and `rename`
 * OVERWRITES silently. Two photos from the camera are called `IMG_0001.jpg`
 * with banal frequency; without the stamp, the second would replace the first
 * and the path the assistant received would start pointing at different
 * content — the worst kind of defect, because nothing fails, only the content
 * changes. With the stamp, an `ls` of the folder still comes out in
 * chronological order, which is the order in which one looks for "the image I
 * just sent".
 *
 * **What is sanitised, and what is NOT.** Slashes and `..` go because the
 * server rejects the whole upload if they appear (`InitUpload` validates
 * `filepath.Base`) — a name coming from the device may not choose a folder.
 * Spaces and accents STAY: they are legitimate file names, the server accepts
 * them, and what looks after them on the command line is the shell quoting at
 * the moment of insertion (`comAspasParaShell`), not a mutilation of the name
 * here.
 */
fun nomeDeDestinoPara(nomeOriginal: String, instante: Instant, zona: ZoneId = ZoneId.systemDefault()): String {
    val semCaminho = nomeOriginal
        .replace('\\', '/')
        .substringAfterLast('/')
        .replace("..", "")
        // A NUL in the middle of the name truncates the string on the file
        // system's side and would make the file land under a name other than
        // the one handed back to the operator.
        .replace("\u0000", "")
        .trim()
    val base = semCaminho.ifBlank { NOME_PADRAO }
    return "${CARIMBO.format(instante.atZone(zona))}-$base"
}
