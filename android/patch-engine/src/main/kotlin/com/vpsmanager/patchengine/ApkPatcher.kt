package com.vpsmanager.patchengine

import java.io.File
import java.io.IOException
import java.security.MessageDigest

/**
 * Rebuilds a new APK from the already-installed APK plus an HDiffPatch binary
 * patch, and only hands back the file after checking its SHA-256.
 *
 * This module is deliberately narrow: it **downloads nothing and installs
 * nothing**. Networking belongs to :data, installation to `PackageInstaller`.
 * A file goes in and a file comes out — that is what makes it possible to
 * prove the piece in isolation, in an instrumented test, before an update
 * banner even exists.
 *
 * ### Why a patch instead of downloading the whole APK
 * This project's release APK is over 30 MB. Measured across the two signed
 * APKs the instrumented test uses, the 0.1.5 -> 0.1.6 diff comes to 262 KB —
 * less than 1% of the download. For someone updating the app on a phone over
 * a bad connection, that is the difference between updating and putting it
 * off.
 *
 * ### Verification is part of the job, not an extra
 * `hpatchz` can return success and still produce the wrong bytes — the classic
 * case is applying the patch over a base that is not exactly the one the
 * server used to generate the diff. That is why [apply] demands the expected
 * SHA-256 and treats a divergence as FAILURE
 * ([PatchResult.IntegrityMismatch]), deleting the file before returning. A
 * wrong APK never reaches the installer.
 *
 * ### Threading
 * [apply] BLOCKS: it is file I/O plus decompression, on the order of seconds
 * for a 30 MB APK. Call it from an I/O dispatcher. The module deliberately
 * does not depend on coroutines — :data is what owns that context.
 */
class ApkPatcher internal constructor(private val native: NativePatcher) {

    constructor() : this(SystemNativePatcher)

    /**
     * Applies [patch] over [baseApk] and writes the result to [outputApk].
     *
     * @param expected size and SHA-256 that the server declares for the new
     *   APK. Both come from the same release manifest that announces the
     *   patch; the size lets us refuse early for lack of space, the hash is
     *   the final authority over the content.
     * @param cacheMemoryBytes I/O buffer on the native side. The default is
     *   [DEFAULT_CACHE_MEMORY_BYTES] — read the comment there before raising it.
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

        // Cheap, early check: without it the user pays for the whole
        // decompression only to find out on the last byte that the disk
        // filled up. `usableSpace` (and not `freeSpace`) because that is what
        // this process can actually write, already minus the system reserve.
        //
        // Zero means "I don't know" (nonexistent path, or the OS refused the
        // query), not "full" — in that case we carry on and let the native
        // side fail with HPATCH_FILEWRITE_NO_SPACE_ERROR if that is the case.
        // Refusing over a missing reading would block a perfectly good update.
        val usable = parent.usableSpace
        if (usable in 1 until expected.sizeBytes) {
            return PatchResult.InsufficientStorage(expected.sizeBytes, usable)
        }

        // Leftovers from a previous attempt can never become input to this
        // one: the native side truncates the output file, but if it fails
        // halfway the partial file stays on disk, and that is exactly the file
        // an unsuspecting installer would try to open.
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
            // A full disk is about the environment, not about the patch: the
            // caller reacts to it differently (asking to free up space), so it
            // should not need to know the native error code for that.
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
         * I/O buffer handed to `hpatchz` — 4 MiB.
         *
         * Not a round number picked at random; it is upstream's own
         * `_kPatchCacheSize_def` (`builds/android_ndk_jni_mk/hpatch.c`), and it
         * is the value under which the 8.8 MB RAM peak was measured in the
         * evaluation that chose HDiffPatch.
         *
         * **Why not raise it "to make things faster":** this `.so` is compiled
         * with `-D_IS_NEED_CACHE_OLD_ALL=1` (inherited from upstream's
         * Android.mk — see `src/main/cpp/Android.mk`). With that flag, if
         * `cacheMemory` is greater than or equal to the size of the old APK,
         * the patcher loads the ENTIRE OLD APK into memory. Passing 64 MiB
         * "for headroom" on a 33 MB APK does not buy a faster patch for free:
         * it trades 4 MiB of native heap for 33 MiB, inside an app process
         * that Android kills without ceremony. The limit is explicit here
         * precisely so that trap does not stay implicit.
         *
         * Upstream treats a `cacheMemory` below 256 KiB as 256 KiB, and a
         * negative one as this same 4 MiB default — that is, smaller values
         * save little and cost speed.
         */
        const val DEFAULT_CACHE_MEMORY_BYTES: Long = 4L * 1024 * 1024

        /**
         * `threadNum = 1`. Multi-threading in `hpatchz` only helps on diffs
         * generated with `-SD`/`-WD`, and it multiplies the I/O buffers per
         * thread. The gain does not pay for the memory peak on a phone, and
         * this project's patch is a single file. The multi-threaded code stays
         * LINKED into the `.so` (MT=1 in Android.mk) so that raising this
         * number one day is a one-line change, not a re-vendoring.
         */
        private const val SINGLE_THREAD = 1

        private const val HPATCH_SUCCESS = 0
        private const val HASH_BUFFER_BYTES = 64 * 1024

        /**
         * SHA-256 in lowercase hexadecimal, read in 64 KiB blocks — the file
         * is tens of MB and does not fit (nor needs to fit) in memory.
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
 * What the server declares about the APK the patch is meant to produce. Both
 * fields come from the release manifest, never from the patch itself — if they
 * came from inside the downloaded file, verifying would prove nothing.
 */
data class ExpectedApk(val sha256: String, val sizeBytes: Long)

/**
 * The single native call in this module, isolated behind an interface so that
 * the logic around it (missing inputs, disk space, error-code translation,
 * hash checking) is testable on the host JVM, with no emulator. The real path
 * is still proven by `ApkPatcherInstrumentedTest`.
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
 * (`vendor/HDiffPatch/builds/android_ndk_jni_mk/java`). `System.loadLibrary`
 * lives in that class's static initializer, so touching it is what triggers
 * the load — and that is why it is referenced only in here, and not in any
 * `init` block: on a host JVM (unit tests) the class is never even loaded.
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
