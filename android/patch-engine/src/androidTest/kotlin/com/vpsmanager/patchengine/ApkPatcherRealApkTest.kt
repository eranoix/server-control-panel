package com.vpsmanager.patchengine

import android.os.Debug
import android.util.Log
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.util.Properties
import java.util.concurrent.TimeUnit
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * The real cycle on a device or emulator: apply the 0.1.5 -> 0.1.6 patch
 * (built by the same `hdiffz` the server runs) to the signed 0.1.5 APK and
 * assert the result's SHA-256 is identical to the signed 0.1.6 APK.
 *
 * The fixtures are not committed (about 66 MB) and this test FAILS without
 * them, deliberately, rather than skipping and leaving the suite green without
 * this proof. Generate them with:
 *
 * ```
 * android/patch-engine/tools/make-patch-fixtures.sh
 * ```
 */
class ApkPatcherRealApkTest {

    private companion object {
        const val TAG = "PatchEngineRealApk"
        const val BASE_ASSET = "patch-fixtures/base.apk"
        const val PATCH_ASSET = "patch-fixtures/update.hdiff"
        const val EXPECTED_ASSET = "patch-fixtures/expected.properties"

        /**
         * Ceiling on the PEAK native heap during the patch. Guards against
         * raising `DEFAULT_CACHE_MEMORY_BYTES` past the old APK size, which
         * with `_IS_NEED_CACHE_OLD_ALL=1` loads all of it into memory.
         *
         * Must be a sampled peak, since `hpatchz` frees everything before
         * returning. 16 MiB leaves headroom over the measured 8.8 MB and still
         * catches a jump to 33 MB.
         */
        const val NATIVE_HEAP_PEAK_CEILING_BYTES = 16L * 1024 * 1024

        /** Peak sampling interval; the whole patch takes about 100 ms on the emulator. */
        const val HEAP_SAMPLE_INTERVAL_MS = 2L
    }

    private lateinit var dir: File

    @Before
    fun setUp() {
        // Create the directory BEFORE the fixture check, so a failed check is
        // reported as-is instead of being masked by an error in `tearDown`.
        dir = File(
            InstrumentationRegistry.getInstrumentation().targetContext.cacheDir,
            "realapk-${System.nanoTime()}",
        ).apply { mkdirs() }

        assertTrue(
            "fixtures missing: run android/patch-engine/tools/make-patch-fixtures.sh " +
                "(they are not committed: about 66 MB, regenerated in about 4 min; see this class's KDoc)",
            PatchFixtures.exists(BASE_ASSET) &&
                PatchFixtures.exists(PATCH_ASSET) &&
                PatchFixtures.exists(EXPECTED_ASSET),
        )
    }

    @After
    fun tearDown() {
        if (::dir.isInitialized) dir.deleteRecursively()
    }

    @Test
    fun patch015To016_rebuildsSignedApkByteForByte() {
        val expectedProps = Properties().apply {
            InstrumentationRegistry.getInstrumentation().context.assets.open(EXPECTED_ASSET).use { load(it) }
        }
        val expectedSha = expectedProps.getProperty("newSha256")
        val expectedSize = expectedProps.getProperty("newSizeBytes").toLong()

        val base = PatchFixtures.copyOut(BASE_ASSET, dir, "base.apk")
        val patch = PatchFixtures.copyOut(PATCH_ASSET, dir, "update.hdiff")
        val out = File(dir, "vpsmanager-0.1.6.apk")

        // Sanity check on the fixture itself, so a corrupted asset is not
        // blamed on the patcher.
        assertEquals(
            "base.apk came out of the asset different from what went in",
            expectedProps.getProperty("oldSha256"),
            ApkPatcher.sha256Of(base),
        )

        val heapBefore = Debug.getNativeHeapAllocatedSize()
        val peakSampler = HeapPeakSampler(heapBefore).also { it.start() }
        val startedAt = System.nanoTime()

        val result = ApkPatcher().apply(base, patch, out, ExpectedApk(expectedSha, expectedSize))

        val elapsedMs = (System.nanoTime() - startedAt) / 1_000_000
        val heapPeakGrowth = peakSampler.stopAndPeakGrowth()

        Log.i(
            TAG,
            "base=${base.length()}B patch=${patch.length()}B " +
                "(${"%.2f".format(patch.length() * 100.0 / base.length())}% of the APK) " +
                "time=${elapsedMs}ms nativeHeapPeak+=${heapPeakGrowth}B " +
                "samples=${peakSampler.sampleCount} result=$result",
        )

        assertTrue("expected Applied, got $result", result is PatchResult.Applied)
        result as PatchResult.Applied
        assertEquals("the rebuilt APK must match the signed 0.1.6 byte for byte", expectedSha, result.sha256)
        assertEquals(expectedSize, out.length())

        assertTrue(
            "native heap peak at ${heapPeakGrowth}B (${peakSampler.sampleCount} samples). " +
                "Was cacheMemory raised? With _IS_NEED_CACHE_OLD_ALL=1 that loads " +
                "the whole old APK into RAM",
            heapPeakGrowth < NATIVE_HEAP_PEAK_CEILING_BYTES,
        )
    }

    /**
     * Samples `Debug.getNativeHeapAllocatedSize()` on a parallel thread to
     * estimate the PEAK, since `hpatchz` frees everything before returning.
     */
    private class HeapPeakSampler(private val baseline: Long) : Thread() {
        @Volatile private var running = true

        @Volatile private var peak = baseline

        @Volatile var sampleCount = 0
            private set

        override fun run() {
            while (running) {
                val now = Debug.getNativeHeapAllocatedSize()
                if (now > peak) peak = now
                sampleCount++
                try {
                    sleep(HEAP_SAMPLE_INTERVAL_MS)
                } catch (_: InterruptedException) {
                    return
                }
            }
        }

        fun stopAndPeakGrowth(): Long {
            running = false
            join(TimeUnit.SECONDS.toMillis(2))
            return peak - baseline
        }
    }

    @Test
    fun realApkPatch_onWrongBase_deliversNoFile() {
        val expectedProps = Properties().apply {
            InstrumentationRegistry.getInstrumentation().context.assets.open(EXPECTED_ASSET).use { load(it) }
        }
        val base = PatchFixtures.copyOut(BASE_ASSET, dir, "base.apk")
        val patch = PatchFixtures.copyOut(PATCH_ASSET, dir, "update.hdiff")

        // An "almost right" base (same size, a few bytes swapped): the most
        // dangerous case, because the diff header still checks out.
        val wrongBase = File(dir, "wrong-base.apk").apply {
            val bytes = base.readBytes()
            for (i in bytes.indices step 1_000_000) bytes[i] = (bytes[i] + 1).toByte()
            writeBytes(bytes)
        }
        val out = File(dir, "out.apk")

        val result = ApkPatcher().apply(
            wrongBase,
            patch,
            out,
            ExpectedApk(
                expectedProps.getProperty("newSha256"),
                expectedProps.getProperty("newSizeBytes").toLong(),
            ),
        )

        assertTrue("a wrong APK must never become Applied; got $result", result !is PatchResult.Applied)
        assertTrue("the rejected file must not stay on disk", !out.exists())
    }
}
