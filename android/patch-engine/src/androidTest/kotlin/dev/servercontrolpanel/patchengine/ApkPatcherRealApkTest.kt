package dev.servercontrolpanel.patchengine

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

class ApkPatcherRealApkTest {

    private companion object {
        const val TAG = "PatchEngineRealApk"
        const val BASE_ASSET = "patch-fixtures/base.apk"
        const val PATCH_ASSET = "patch-fixtures/update.hdiff"
        const val EXPECTED_ASSET = "patch-fixtures/expected.properties"

        const val NATIVE_HEAP_PEAK_CEILING_BYTES = 16L * 1024 * 1024

        const val HEAP_SAMPLE_INTERVAL_MS = 2L
    }

    private lateinit var dir: File

    @Before
    fun setUp() {
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
        val out = File(dir, "servercontrolpanel-0.1.6.apk")

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
