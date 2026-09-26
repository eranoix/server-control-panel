package com.vpsmanager.patchengine

import java.io.File
import java.io.IOException
import java.security.MessageDigest

/**
 * Rebuilds a new APK from the already-installed APK plus an HDiffPatch binary
 * patch, and only hands back the file after checking its SHA-256.
 *
 * Deliberately narrow: it downloads and installs nothing (networking belongs
 * to :data, installation to `PackageInstaller`), so it can be tested in isolation.
 *
 * `hpatchz` can report success and still produce wrong bytes (e.g. a base
 * that differs from the one the diff was built against), so [apply] requires
 * the expected SHA-256, treats a mismatch as [PatchResult.IntegrityMismatch]
 * and deletes the file. A wrong APK never reaches the installer.
 *
 * [apply] BLOCKS (file I/O plus decompression, seconds for a 30 MB APK); call
 * it from an I/O dispatcher.
 */
class ApkPatcher internal constructor(private val native: NativePatcher) {

    constructor() : this(SystemNativePatcher)

    /**
     * Applies [patch] over [baseApk] and writes the result to [outputApk].
     *
     * @param expected size and SHA-256 the release manifest declares for the
     *   new APK; the size allows an early refusal for lack of space, the hash
     *   is the final authority over the content.
     * @param cacheMemoryBytes native I/O buffer. Read the note on
     *   [DEFAULT_CACHE_MEMORY_BYTES] before raising it.
     */
    fun apply(
        baseApk: File,
        patch: File,
        outputApk: File,
        expected: ExpectedApk,
        cacheMemoryBytes: Long = DEFAULT_CACHE_MEMORY_BYTES,
    ): PatchResult {
        if (!baseApk.isFile || !baseApk.canRead()) {
            return PatchResult.InputMissing(PatchResult.InputRole.BASE_APK, baseApk.path)
        }
        if (!patch.isFile || !patch.canRead()) {
            return PatchResult.InputMissing(PatchResult.InputRole.PATCH, patch.path)
        }

        val parent = outputApk.absoluteFile.parentFile
            ?: return PatchResult.OutputUnwritable(outputApk.path, "no parent directory")
        if (!parent.isDirectory && !parent.mkdirs()) {
            return PatchResult.OutputUnwritable(outputApk.path, "could not create ${parent.path}")
        }

        // Early space check, using `usableSpace` (what this process can write,
        // minus the system reserve). Zero means "unknown", not "full": carry on
        // and let the native side report HPATCH_FILEWRITE_NO_SPACE_ERROR.
        val usable = parent.usableSpace
        if (usable in 1 until expected.sizeBytes) {
            return PatchResult.InsufficientStorage(expected.sizeBytes, usable)
        }

        // A partial file from a failed earlier attempt must never be mistaken
        // for output of this one.
        if (outputApk.exists() && !outputApk.delete()) {
            return PatchResult.OutputUnwritable(outputApk.path, "leftover from a previous run could not be deleted")
        }

        val rawCode = try {
            native.patch(
                oldFileName = baseApk.absolutePath,
                diffFileName = patch.absolutePath,
                outNewFileName = outputApk.absolutePath,
                cacheMemory = cacheMemoryBytes,
                threadNum = SINGLE_THREAD,
                isChecksumNewData = true,
            )
        } catch (e: UnsatisfiedLinkError) {
            return PatchResult.EngineUnavailable(e.message ?: e.toString())
        }

        if (rawCode != HPATCH_SUCCESS) {
            outputApk.delete()
            val code = HPatchCode.fromRaw(rawCode)
            // A full disk is an environment problem the caller handles
            // differently, so it gets its own result type.
            return if (code == HPatchCode.FILE_WRITE_NO_SPACE_ERROR) {
                PatchResult.InsufficientStorage(expected.sizeBytes, parent.usableSpace)
            } else {
                PatchResult.NativeFailure(code, rawCode)
            }
        }

        val actualSha256 = try {
            sha256Of(outputApk)
        } catch (e: IOException) {
            outputApk.delete()
            return PatchResult.OutputUnwritable(outputApk.path, "failed to re-read for verification: ${e.message}")
        }

        if (!actualSha256.equals(expected.sha256, ignoreCase = true)) {
            outputApk.delete()
            return PatchResult.IntegrityMismatch(expected.sha256.lowercase(), actualSha256)
        }

        return PatchResult.Applied(outputApk, actualSha256)
    }

    companion object {
        /**
         * I/O buffer handed to `hpatchz`: 4 MiB, upstream's own
         * `_kPatchCacheSize_def` (measured RAM peak 8.8 MB).
         *
         * Do not raise it: the `.so` is built with `-D_IS_NEED_CACHE_OLD_ALL=1`
         * (see `src/main/cpp/Android.mk`), so a `cacheMemory` >= the old APK
         * size loads the ENTIRE old APK into native memory. Upstream clamps
         * values below 256 KiB, so going smaller saves little.
         */
        const val DEFAULT_CACHE_MEMORY_BYTES: Long = 4L * 1024 * 1024

        /**
         * `threadNum = 1`: multithreading only helps `-SD`/`-WD` diffs and
         * multiplies I/O buffers per thread. The MT code stays linked (MT=1 in
         * Android.mk) so raising this is a one-line change.
         */
        private const val SINGLE_THREAD = 1

        private const val HPATCH_SUCCESS = 0
        private const val HASH_BUFFER_BYTES = 64 * 1024

        /**
         * SHA-256 in lowercase hex, streamed in 64 KiB blocks since the file
         * is tens of MB.
         */
        fun sha256Of(file: File): String {
            val digest = MessageDigest.getInstance("SHA-256")
            val buffer = ByteArray(HASH_BUFFER_BYTES)
            file.inputStream().use { input ->
                while (true) {
                    val read = input.read(buffer)
                    if (read <= 0) break
                    digest.update(buffer, 0, read)
                }
            }
            return digest.digest().joinToString(separator = "") { byte ->
                val v = byte.toInt() and 0xff
                HEX[v ushr 4].toString() + HEX[v and 0x0f]
            }
        }

        private const val HEX = "0123456789abcdef"
    }
}

/**
 * What the server declares about the APK the patch should produce. Both fields
 * come from the release manifest, never from the patch itself, or verifying
 * would prove nothing.
 */
data class ExpectedApk(val sha256: String, val sizeBytes: Long)

/**
 * The module's single native call, behind an interface so the logic around it
 * is testable on the host JVM without an emulator.
 */
internal fun interface NativePatcher {
    fun patch(
        oldFileName: String,
        diffFileName: String,
        outNewFileName: String,
        cacheMemory: Long,
        threadNum: Int,
        isChecksumNewData: Boolean,
    ): Int
}

/**
 * Bridge to `com.github.sisong.HPatch`, the official vendored binding
 * (`vendor/HDiffPatch/builds/android_ndk_jni_mk/java`). Its static initializer
 * calls `System.loadLibrary`, so it is referenced only here, keeping it
 * unloaded on a host JVM (unit tests).
 */
internal object SystemNativePatcher : NativePatcher {
    override fun patch(
        oldFileName: String,
        diffFileName: String,
        outNewFileName: String,
        cacheMemory: Long,
        threadNum: Int,
        isChecksumNewData: Boolean,
    ): Int = com.github.sisong.HPatch.patch(
        oldFileName,
        diffFileName,
        outNewFileName,
        cacheMemory,
        threadNum,
        isChecksumNewData,
    )
}
