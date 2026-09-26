package com.vpsmanager.patchengine

import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.io.FileNotFoundException

/**
 * Copies fixtures from the TEST APK's assets into real files.
 *
 * `hpatchz` works with file paths, not with streams — it is a C library that
 * calls `fopen`. An APK asset has no path on the filesystem, so materializing
 * is mandatory, not laziness.
 */
internal object PatchFixtures {

    private val assets get() = InstrumentationRegistry.getInstrumentation().context.assets

    fun exists(assetPath: String): Boolean =
        try {
            assets.open(assetPath).close()
            true
        } catch (_: FileNotFoundException) {
            false
        }

    /** Materializes [assetPath] inside [dir] and returns the file. */
    fun copyOut(assetPath: String, dir: File, name: String = assetPath.substringAfterLast('/')): File {
        val dest = File(dir, name)
        assets.open(assetPath).use { input ->
            dest.outputStream().use { output -> input.copyTo(output, DEFAULT_BUFFER_BYTES) }
        }
        return dest
    }

    /** Copy of [source] with the byte at [offset] altered — patch corrupted during download. */
    fun corruptedCopy(source: File, dir: File, name: String, offset: Int): File {
        val bytes = source.readBytes()
        bytes[offset] = (bytes[offset] + 1).toByte()
        return File(dir, name).apply { writeBytes(bytes) }
    }

    /** Copy of [source] cut at [keepBytes] — interrupted download. */
    fun truncatedCopy(source: File, dir: File, name: String, keepBytes: Int): File =
        File(dir, name).apply { writeBytes(source.readBytes().copyOf(keepBytes)) }

    private const val DEFAULT_BUFFER_BYTES = 64 * 1024
}
