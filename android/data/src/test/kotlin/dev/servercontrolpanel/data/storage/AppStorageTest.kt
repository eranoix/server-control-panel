package dev.servercontrolpanel.data.storage

import dev.servercontrolpanel.data.storage.AppStorage.Kind
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/** Pins the boundaries of the storage sweep: deleting the wrong file is worse than no sweep. */
class AppStorageTest {

    @get:Rule
    val tempDir = TemporaryFolder()

    private val now = 1_700_000_000_000L
    private val oneDay = 24L * 60 * 60 * 1000

    private fun file(dir: File, name: String, bytes: Int, ageInDays: Long): File {
        dir.mkdirs()
        val f = File(dir, name)
        f.writeBytes(ByteArray(bytes))
        f.setLastModified(now - ageInDays * oneDay)
        return f
    }

    @Test
    fun `a self-bounded store is never touched by the sweep`() {
        // The HTTP cache and media have their own limits; deleting them only costs network later.
        val cache = tempDir.newFolder("bff-http")
        val old = file(cache, "response", 1_000, ageInDays = 90)

        val r = AppStorage.maintenance(
            listOf(AppStorage.StorageArea("Cache", "", cache, Kind.AUTO_BOUNDED)),
            nowMs = now,
        )

        assertTrue("the cache prunes itself, the sweep leaves it alone", old.exists())
        assertEquals(0L, r.freedBytes)
    }

    @Test
    fun `an old temporary file is removed and a recent one stays`() {
        val tmp = tempDir.newFolder("attachments")
        val stale = file(tmp, "abandoned-upload", 4_000, ageInDays = 10)
        val recent = file(tmp, "uploading-now", 2_000, ageInDays = 1)

        val r = AppStorage.maintenance(
            listOf(AppStorage.StorageArea("Temporary", "", tmp, Kind.TEMPORARY)),
            nowMs = now,
        )

        assertFalse("an upload stalled for ten days will not finish", stale.exists())
        assertTrue("an upload from yesterday may just be waiting for network", recent.exists())
        assertEquals(4_000L, r.freedBytes)
        assertEquals(1, r.filesRemoved)
    }

    @Test
    fun `an update in use is never deleted, even when newest`() {
        // Deleting a download in flight wastes the network already spent on it.
        val staging = tempDir.newFolder("updates")
        val downloading = file(staging, "new.hdiff", 9_000, ageInDays = 0)
        val fromPreviousVersion = file(staging, "old.apk", 50_000, ageInDays = 0)

        val r = AppStorage.maintenance(
            listOf(AppStorage.StorageArea("Updates", "", staging, Kind.IN_TRANSIT)),
            nowMs = now,
            inUse = setOf(downloading),
        )

        assertTrue("the download in progress must survive", downloading.exists())
        assertFalse("the artifact of the installed version is garbage", fromPreviousVersion.exists())
        assertEquals(50_000L, r.freedBytes)
    }

    @Test
    fun `in-transit files are removed when unused, regardless of age`() {
        // A rebuilt APK is tens of MB, so it is not kept once installed.
        val staging = tempDir.newFolder("updates")
        val installedToday = file(staging, "already-installed.apk", 60_000, ageInDays = 0)

        val r = AppStorage.maintenance(
            listOf(AppStorage.StorageArea("Updates", "", staging, Kind.IN_TRANSIT)),
            nowMs = now,
        )

        assertFalse(installedToday.exists())
        assertEquals(60_000L, r.freedBytes)
    }

    @Test
    fun `a file without a valid date is not treated as old`() {
        // `lastModified` returns 0 when unknown; that must not read as "1970, therefore old".
        val tmp = tempDir.newFolder("attachments")
        val noDate = file(tmp, "no-date", 1_500, ageInDays = 0)
        noDate.setLastModified(0)

        AppStorage.maintenance(
            listOf(AppStorage.StorageArea("Temporary", "", tmp, Kind.TEMPORARY)),
            nowMs = now,
        )

        assertTrue("with no known date, nothing is deleted", noDate.exists())
    }

    @Test
    fun `measuring sums recursively and survives a missing directory`() {
        val dir = tempDir.newFolder("media")
        file(File(dir, "sub"), "a", 700, ageInDays = 0)
        file(dir, "b", 300, ageInDays = 0)

        val usages = AppStorage.measure(
            listOf(
                AppStorage.StorageArea("Media", "", dir, Kind.AUTO_BOUNDED),
                AppStorage.StorageArea("Never created", "", File(dir, "missing"), Kind.TEMPORARY),
            ),
        )

        assertEquals(1_000L, usages[0].bytes)
        assertEquals("a directory that never existed counts as zero, not an error", 0L, usages[1].bytes)
    }

    @Test
    fun `formatting uses the same base as the Android screens`() {
        assertEquals("512 B", AppStorage.formatBytes(512))
        assertEquals("1,5 kB", AppStorage.formatBytes(1_500).replace('.', ','))
        assertEquals("24,0 MB", AppStorage.formatBytes(24_000_000).replace('.', ','))
    }
}
