package dev.servercontrolpanel.patchengine

import java.io.File
import java.io.IOException
import java.security.MessageDigest

class ApkPatcher internal constructor(private val native: NativePatcher) {

    constructor() : this(SystemNativePatcher)

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

        val usable = parent.usableSpace
        if (usable in 1 until expected.sizeBytes) {
            return PatchResult.InsufficientStorage(expected.sizeBytes, usable)
        }

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
        const val DEFAULT_CACHE_MEMORY_BYTES: Long = 4L * 1024 * 1024

        private const val SINGLE_THREAD = 1

        private const val HPATCH_SUCCESS = 0
        private const val HASH_BUFFER_BYTES = 64 * 1024

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

data class ExpectedApk(val sha256: String, val sizeBytes: Long)

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
