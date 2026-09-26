package com.vpsmanager.patchengine

import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * Instrumented: needs the real `libhpatchz.so` on a device or emulator. Uses
 * the SMALL committed fixtures (`src/androidTest/assets/hdiff`), so it runs on
 * a clean checkout.
 *
 * Covers the FAILURE paths that `ApkPatcherRealApkTest` does not. In
 * [wrongBase_nativePassesButHashFails] and
 * [patchCorruptedInMiddle_nativePassesButHashFails], `hpatchz` returns success
 * with a wrong file, so reaching [PatchResult.IntegrityMismatch] proves the
 * SHA-256 check is what protects the installer.
 */
class ApkPatcherSmokeTest {

    // Printed by tools/make-smoke-fixtures.sh; update it when regenerating the fixtures.
    private val expectedNewSha256 = "4cacda8517f06e47973f6b7450e27212728da54f880d34523948c8346ffbd2ae"
    private val expectedNewSize = 270_336L
    private val expected get() = ExpectedApk(expectedNewSha256, expectedNewSize)

    private lateinit var dir: File
    private lateinit var oldFile: File
    private lateinit var patchFile: File
    private lateinit var out: File

    private val patcher = ApkPatcher()

    @Before
    fun setUp() {
        dir = File(
            InstrumentationRegistry.getInstrumentation().targetContext.cacheDir,
            "smoke-${System.nanoTime()}",
        ).apply { mkdirs() }
        oldFile = PatchFixtures.copyOut("hdiff/smoke-old.bin", dir)
        patchFile = PatchFixtures.copyOut("hdiff/smoke.hdiff", dir)
        out = File(dir, "out.bin")
    }

    @After
    fun tearDown() {
        dir.deleteRecursively()
    }

    @Test
    fun appliesPatchAndChecksHash() {
        val result = patcher.apply(oldFile, patchFile, out, expected)

        assertTrue("expected Applied, got $result", result is PatchResult.Applied)
        result as PatchResult.Applied
        assertEquals(expectedNewSha256, result.sha256)
        assertEquals(expectedNewSize, out.length())
    }

    @Test
    fun wrongBase_nativePassesButHashFails() {
        // Same SIZE as the correct base, different content: the diff header
        // still checks out, so hpatchz cannot notice.
        val wrongBase = File(dir, "wrong-old.bin").apply {
            val bytes = oldFile.readBytes()
            for (i in bytes.indices step 4096) bytes[i] = (bytes[i] + 1).toByte()
            writeBytes(bytes)
        }

        val result = patcher.apply(wrongBase, patchFile, out, expected)

        assertTrue(
            "expected IntegrityMismatch (native would have returned 0 here), got $result",
            result is PatchResult.IntegrityMismatch,
        )
        result as PatchResult.IntegrityMismatch
        assertEquals(expectedNewSha256, result.expectedSha256)
        assertFalse("a hash mismatch and a file on disk cannot coexist", out.exists())
    }

    @Test
    fun patchCorruptedInMiddle_nativePassesButHashFails() {
        val corrupt = PatchFixtures.corruptedCopy(patchFile, dir, "corrupt.hdiff", offset = 5_000)

        val result = patcher.apply(oldFile, corrupt, out, expected)

        assertTrue(
            "expected IntegrityMismatch, since one swapped byte inside the zstd data does not make hpatchz fail; got $result",
            result is PatchResult.IntegrityMismatch,
        )
        assertFalse(out.exists())
    }

    @Test
    fun truncatedPatch_failsInNative() {
        val truncated = PatchFixtures.truncatedCopy(patchFile, dir, "trunc.hdiff", keepBytes = 4_000)

        val result = patcher.apply(oldFile, truncated, out, expected)

        assertTrue("expected NativeFailure, got $result", result is PatchResult.NativeFailure)
        assertEquals(HPatchCode.HPATCH_ERROR, (result as PatchResult.NativeFailure).code)
        assertFalse(out.exists())
    }

    @Test
    fun destroyedHeader_failsAsHdiffInfoError() {
        val broken = File(dir, "hdr.hdiff").apply {
            val bytes = patchFile.readBytes()
            for (i in 2 until 10) bytes[i] = 'Z'.code.toByte()
            writeBytes(bytes)
        }

        val result = patcher.apply(oldFile, broken, out, expected)

        assertTrue("expected NativeFailure, got $result", result is PatchResult.NativeFailure)
        assertEquals(HPatchCode.HDIFF_INFO_ERROR, (result as PatchResult.NativeFailure).code)
    }

    @Test
    fun wrongExpectedHash_failsEvenWithGoodPatch() {
        // Guards against the opposite bug: the module must not "fix" a wrong
        // manifest by accepting whatever came out.
        val result = patcher.apply(
            oldFile,
            patchFile,
            out,
            ExpectedApk("0".repeat(64), expectedNewSize),
        )

        assertTrue(result is PatchResult.IntegrityMismatch)
        assertEquals(expectedNewSha256, (result as PatchResult.IntegrityMismatch).actualSha256)
    }

    @Test
    fun missingPatch_neverReachesNative() {
        val result = patcher.apply(oldFile, File(dir, "missing.hdiff"), out, expected)

        assertTrue(result is PatchResult.InputMissing)
        assertEquals(PatchResult.InputRole.PATCH, (result as PatchResult.InputMissing).role)
    }
}
