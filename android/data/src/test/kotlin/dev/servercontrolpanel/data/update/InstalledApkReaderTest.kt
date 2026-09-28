package dev.servercontrolpanel.data.update

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import dev.servercontrolpanel.patchengine.ApkPatcher
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class InstalledApkReaderTest {

    @get:Rule
    val temp = TemporaryFolder()

    private val context: Context = ApplicationProvider.getApplicationContext()

    private fun fakeApk(name: String, content: ByteArray): File =
        File(temp.newFolder(), name).apply {
            parentFile?.mkdirs()
            writeBytes(content)
        }

    @Test
    fun `hashes the APK the system currently points to`() {
        val content = ByteArray(2048) { (it % 31).toByte() }
        val apk = fakeApk("base.apk", content)
        context.applicationInfo.sourceDir = apk.path

        val result = InstalledApkReader(context).read()

        check(result is InstalledApkResult.Ok)
        assertEquals(ApkPatcher.sha256Of(apk), result.sha256)
        assertEquals(apk.path, result.file.path)
    }

    @Test
    fun `the hash is cached and the second read does not rehash`() {
        val apk = fakeApk("base.apk", ByteArray(1024) { 7 })
        context.applicationInfo.sourceDir = apk.path
        var times = 0
        val reader = InstalledApkReader(context) { file -> times++; ApkPatcher.sha256Of(file) }

        val first = reader.read()
        val second = reader.read()

        assertEquals(1, times)
        assertEquals((first as InstalledApkResult.Ok).sha256, (second as InstalledApkResult.Ok).sha256)
    }

    @Test
    fun `a new path invalidates the cache and never returns the previous APK hash`() {
        val old = fakeApk("base.apk", ByteArray(1024) { 1 })
        val next = fakeApk("base.apk", ByteArray(1024) { 2 })
        val reader = InstalledApkReader(context)

        context.applicationInfo.sourceDir = old.path
        val oldHash = (reader.read() as InstalledApkResult.Ok).sha256

        context.applicationInfo.sourceDir = next.path
        val newHash = (reader.read() as InstalledApkResult.Ok).sha256

        assertEquals(ApkPatcher.sha256Of(old), oldHash)
        assertEquals(ApkPatcher.sha256Of(next), newHash)
        assertTrue("the cache must not survive a reinstall", oldHash != newHash)
    }

    @Test
    fun `an unreadable APK degrades to unavailable instead of crashing`() {
        context.applicationInfo.sourceDir = File(temp.newFolder(), "does-not-exist.apk").path

        val result = InstalledApkReader(context).read()

        check(result is InstalledApkResult.Unavailable)
        assertTrue(result.reason.contains("does-not-exist.apk"))
    }
}
