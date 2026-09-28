package dev.servercontrolpanel.feature.terminal.attach

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

data class LocalAttachment(
    val uri: String,
    val displayName: String,
    val sizeBytes: Long,
)

private const val DEFAULT_NAME = "attachment"

private val STAMP = DateTimeFormatter.ofPattern("yyyyMMdd-HHmmss")

fun destinationNameFor(originalName: String, instant: Instant, zone: ZoneId = ZoneId.systemDefault()): String {
    val withoutPath = originalName
        .replace('\\', '/')
        .substringAfterLast('/')
        .replace("..", "")
        .replace("\u0000", "")
        .trim()
    val base = withoutPath.ifBlank { DEFAULT_NAME }
    return "${STAMP.format(instant.atZone(zone))}-$base"
}
