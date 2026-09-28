package dev.servercontrolpanel.patchengine

import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.io.FileNotFoundException

internal object PatchFixtures {

    private val assets get() = InstrumentationRegistry.getInstrumentation().context.assets

    fun exists(assetPath: String): Boolean =
        try {
            assets.open(assetPath).close()
            true
        } catch (_: FileNotFoundException) {
            false
        }

    fun copyOut(assetPath: String, dir: File, name: String = assetPath.substringAfterLast('/')): File {
        val dest = File(dir, name)
        assets.open(assetPath).use { input ->
            dest.outputStream().use { output -> input.copyTo(output, DEFAULT_BUFFER_BYTES) }
        }
        return dest
    }

    fun corruptedCopy(source: File, dir: File, name: String, offset: Int): File {
        val bytes = source.readBytes()
        bytes[offset] = (bytes[offset] + 1).toByte()
        return File(dir, name).apply { writeBytes(bytes) }
    }

    fun truncatedCopy(source: File, dir: File, name: String, keepBytes: Int): File =
        File(dir, name).apply { writeBytes(source.readBytes().copyOf(keepBytes)) }

    private const val DEFAULT_BUFFER_BYTES = 64 * 1024
}
