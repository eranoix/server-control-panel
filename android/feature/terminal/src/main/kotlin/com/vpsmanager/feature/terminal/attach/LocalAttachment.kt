package com.vpsmanager.feature.terminal.attach

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * A device file chosen to become an attachment: the [uri] the `ContentResolver`
 * can open, the name the picker showed and the size (when the provider reports it).
 */
data class LocalAttachment(
    val uri: String,
    val displayName: String,
    val sizeBytes: Long,
)

/** Name used when the content provider exposes no `DISPLAY_NAME` at all. */
private const val DEFAULT_NAME = "anexo"

private val STAMP = DateTimeFormatter.ofPattern("yyyyMMdd-HHmmss")

/**
 * The name the file lands under in the server's inbound folder.
 *
 * Prefixed with a timestamp because the server finishes with `os.Rename`, which
 * silently overwrites: two camera photos named `IMG_0001.jpg` would otherwise
 * swap content under a path already handed out. The stamp also keeps `ls` in
 * chronological order.
 *
 * Slashes and `..` are removed (the server rejects them); spaces and accents stay,
 * since shell quoting (`shellQuoted`) handles them at insertion time.
 */
fun destinationNameFor(originalName: String, instant: Instant, zone: ZoneId = ZoneId.systemDefault()): String {
    val withoutPath = originalName
        .replace('\\', '/')
        .substringAfterLast('/')
        .replace("..", "")
        // A NUL truncates the name on the filesystem side, so the file would land
        // under a different name than the one returned.
        .replace("\u0000", "")
        .trim()
    val base = withoutPath.ifBlank { DEFAULT_NAME }
    return "${STAMP.format(instant.atZone(zone))}-$base"
}
