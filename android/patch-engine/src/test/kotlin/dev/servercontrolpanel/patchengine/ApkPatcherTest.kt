package dev.servercontrolpanel.patchengine

import java.io.File
import java.nio.file.Files
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ApkPatcherTest {

    private val tempDir: File = Files.createTempDirectory("patch-engine-test").toFile()

    @After
    fun tearDown() {
        tempDir.deleteRecursively()
    }

    private fun file(name: String, bytes: ByteArray = ByteArray(0)): File =
        File(tempDir, name).apply { writeBytes(bytes) }

    private fun fakeNative(returns: Int, produces: ByteArray? = null) = NativePatcher {
        _, _, outNewFileName, _, _, _ ->
        if (produces != null) File(outNewFileName).writeBytes(produces)
        returns
    }

    private val newBytes = "new content".toByteArray()
    private val newSha = ApkPatcher.sha256Of(File(tempDir, "seed").apply { writeBytes(newBytes) })
    private val expected get() = ExpectedApk(sha256 = newSha, sizeBytes = newBytes.size.toLong())

    @Test
    fun apply_whenEverythingMatches_returnsAppliedWithComputedHash() {
        val patcher = ApkPatcher(fakeNative(returns = 0, produces = newBytes))
        val out = File(tempDir, "out.apk")

        val result = patcher.apply(file("base.apk", byteArrayOf(1)), file("p.hdiff", byteArrayOf(2)), out, expected)

        assertTrue("expected Applied, got $result", result is PatchResult.Applied)
        result as PatchResult.Applied
        assertEquals(out, result.newFile)
        assertEquals(newSha, result.sha256)
        assertTrue(out.exists())
    }

    @Test
    fun apply_whenNativeSaysOkButBytesAreWrong_failsAndDeletesOutput() {
        val patcher = ApkPatcher(fakeNative(returns = 0, produces = "something else".toByteArray()))
        val out = File(tempDir, "out.apk")

        val result = patcher.apply(file("base.apk", byteArrayOf(1)), file("p.hdiff", byteArrayOf(2)), out, expected)

        assertTrue("expected IntegrityMismatch, got $result", result is PatchResult.IntegrityMismatch)
        result as PatchResult.IntegrityMismatch
        assertEquals(newSha, result.expectedSha256)
        assertFalse("the wrong file must not survive the result", out.exists())
    }

    @Test
    fun apply_uppercaseExpectedHash_stillMatches() {
        val patcher = ApkPatcher(fakeNative(returns = 0, produces = newBytes))
        val out = File(tempDir, "out.apk")

        val result = patcher.apply(
            file("base.apk", byteArrayOf(1)),
            file("p.hdiff", byteArrayOf(2)),
            out,
            ExpectedApk(sha256 = newSha.uppercase(), sizeBytes = newBytes.size.toLong()),
        )

        assertTrue("the hash is hex, case should not matter; got $result", result is PatchResult.Applied)
    }

    @Test
    fun apply_withoutBaseApk_doesNotCallNative() {
        var called = false
        val patcher = ApkPatcher(NativePatcher { _, _, _, _, _, _ -> called = true; 0 })

        val result = patcher.apply(
            File(tempDir, "missing.apk"),
            file("p.hdiff", byteArrayOf(2)),
            File(tempDir, "out.apk"),
            expected,
        )

        assertEquals(
            PatchResult.InputMissing(PatchResult.InputRole.BASE_APK, File(tempDir, "missing.apk").path),
            result,
        )
        assertFalse("no point entering JNI without the files", called)
    }

    @Test
    fun apply_withoutPatch_reportsWhichInputIsMissing() {
        val patcher = ApkPatcher(fakeNative(returns = 0, produces = newBytes))

        val result = patcher.apply(
            file("base.apk", byteArrayOf(1)),
            File(tempDir, "missing.hdiff"),
            File(tempDir, "out.apk"),
            expected,
        )

        assertTrue(result is PatchResult.InputMissing)
        assertEquals(PatchResult.InputRole.PATCH, (result as PatchResult.InputMissing).role)
    }

    @Test
    fun apply_commonNativeError_becomesTranslatedNativeFailure() {
        val patcher = ApkPatcher(fakeNative(returns = 9))
        val out = File(tempDir, "out.apk")

        val result = patcher.apply(file("base.apk", byteArrayOf(1)), file("p.hdiff", byteArrayOf(2)), out, expected)

        assertEquals(PatchResult.NativeFailure(HPatchCode.HDIFF_INFO_ERROR, 9), result)
        assertFalse(out.exists())
    }

    @Test
    fun apply_diskFullMidWrite_becomesInsufficientStorageNotNativeCode() {
        val patcher = ApkPatcher(fakeNative(returns = 24))

        val result = patcher.apply(
            file("base.apk", byteArrayOf(1)),
            file("p.hdiff", byteArrayOf(2)),
            File(tempDir, "out.apk"),
            expected,
        )

        assertTrue("expected InsufficientStorage, got $result", result is PatchResult.InsufficientStorage)
        assertEquals(newBytes.size.toLong(), (result as PatchResult.InsufficientStorage).requiredBytes)
    }

    @Test
    fun apply_withoutHpatchzLib_becomesEngineUnavailable() {
        val patcher = ApkPatcher(
            NativePatcher { _, _, _, _, _, _ -> throw UnsatisfiedLinkError("dlopen failed: libhpatchz.so") },
        )

        val result = patcher.apply(
            file("base.apk", byteArrayOf(1)),
            file("p.hdiff", byteArrayOf(2)),
            File(tempDir, "out.apk"),
            expected,
        )

        assertTrue("expected EngineUnavailable, got $result", result is PatchResult.EngineUnavailable)
    }

    @Test
    fun apply_deletesLeftoverFromPreviousRunBeforeCallingNative() {
        val out = File(tempDir, "out.apk")
        out.writeBytes("half-written apk from a failed attempt".toByteArray())

        var sawLeftover = true
        val patcher = ApkPatcher(
            NativePatcher { _, _, outNewFileName, _, _, _ ->
                sawLeftover = File(outNewFileName).exists()
                File(outNewFileName).writeBytes(newBytes)
                0
            },
        )

        val result = patcher.apply(file("base.apk", byteArrayOf(1)), file("p.hdiff", byteArrayOf(2)), out, expected)

        assertFalse("native code must not find leftovers from a previous attempt", sawLeftover)
        assertTrue(result is PatchResult.Applied)
    }

    @Test
    fun apply_passesDefaultFourMebibyteCacheMemory() {
        var seenCache = -1L
        var seenThreads = -1
        var seenChecksum = false
        val patcher = ApkPatcher(
            NativePatcher { _, _, outNewFileName, cacheMemory, threadNum, isChecksumNewData ->
                seenCache = cacheMemory
                seenThreads = threadNum
                seenChecksum = isChecksumNewData
                File(outNewFileName).writeBytes(newBytes)
                0
            },
        )

        patcher.apply(file("base.apk", byteArrayOf(1)), file("p.hdiff", byteArrayOf(2)), File(tempDir, "out.apk"), expected)

        assertEquals(4L * 1024 * 1024, seenCache)
        assertEquals(4L * 1024 * 1024, ApkPatcher.DEFAULT_CACHE_MEMORY_BYTES)
        assertEquals(1, seenThreads)
        assertTrue(seenChecksum)
    }

    @Test
    fun sha256Of_matchesKnownNistVector() {
        val f = File(tempDir, "abc.bin").apply { writeBytes("abc".toByteArray()) }
        assertEquals(
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
            ApkPatcher.sha256Of(f),
        )
    }

    @Test
    fun sha256Of_fileLargerThanReadBuffer() {
        val bytes = ByteArray(200_000) { (it % 251).toByte() }
        val f = File(tempDir, "large.bin").apply { writeBytes(bytes) }

        val digest = java.security.MessageDigest.getInstance("SHA-256").digest(bytes)
        val hex = digest.joinToString("") { "%02x".format(it) }

        assertEquals(hex, ApkPatcher.sha256Of(f))
    }

    @Test
    fun hPatchCode_translatesUpstreamEnumGaps() {
        assertEquals(HPatchCode.OPTIONS_ERROR, HPatchCode.fromRaw(1))
        assertEquals(HPatchCode.DECOMPRESSER_OPEN_ERROR, HPatchCode.fromRaw(20))
        assertEquals(HPatchCode.FILE_WRITE_NO_SPACE_ERROR, HPatchCode.fromRaw(24))
        assertEquals(HPatchCode.CHECKSUM_SET_ERROR, HPatchCode.fromRaw(103))
        assertEquals(HPatchCode.CHECKSUM_NEWDATA_ERROR, HPatchCode.fromRaw(106))
    }

    @Test
    fun hPatchCode_unknownCodeIsNotDisguisedAsKnown() {
        assertEquals(HPatchCode.UNKNOWN, HPatchCode.fromRaw(201))
        assertEquals(HPatchCode.UNKNOWN, HPatchCode.fromRaw(9999))

        val patcher = ApkPatcher(fakeNative(returns = 201))
        val result = patcher.apply(
            file("base.apk", byteArrayOf(1)),
            file("p.hdiff", byteArrayOf(2)),
            File(tempDir, "out.apk"),
            expected,
        )
        assertEquals(PatchResult.NativeFailure(HPatchCode.UNKNOWN, 201), result)
    }
}
