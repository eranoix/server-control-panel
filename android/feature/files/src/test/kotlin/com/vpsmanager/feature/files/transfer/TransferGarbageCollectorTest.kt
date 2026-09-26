package com.vpsmanager.feature.files.transfer

import android.content.SharedPreferences
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * An in-memory [SharedPreferences] fake -- [TransferStateStore]'s internal
 * `SharedPreferences`-taking constructor exists exactly so tests can hand it
 * one of these instead of a real, `Context`-backed prefs file (this module
 * has no Robolectric; a real `getSharedPreferences` call would hit an
 * un-stubbed Android runtime method).
 */
private class FakeSharedPreferences : SharedPreferences {
    private val values = mutableMapOf<String, Any?>()

    override fun getAll(): MutableMap<String, *> = values.toMutableMap()
    override fun getString(key: String?, defValue: String?) = values[key] as? String ?: defValue
    override fun getStringSet(key: String?, defValues: MutableSet<String>?) =
        @Suppress("UNCHECKED_CAST") (values[key] as? MutableSet<String> ?: defValues)
    override fun getInt(key: String?, defValue: Int) = values[key] as? Int ?: defValue
    override fun getLong(key: String?, defValue: Long) = values[key] as? Long ?: defValue
    override fun getFloat(key: String?, defValue: Float) = values[key] as? Float ?: defValue
    override fun getBoolean(key: String?, defValue: Boolean) = values[key] as? Boolean ?: defValue
    override fun contains(key: String?) = values.containsKey(key)
    override fun edit(): SharedPreferences.Editor = FakeEditor()
    override fun registerOnSharedPreferenceChangeListener(listener: SharedPreferences.OnSharedPreferenceChangeListener?) = Unit
    override fun unregisterOnSharedPreferenceChangeListener(listener: SharedPreferences.OnSharedPreferenceChangeListener?) = Unit

    private inner class FakeEditor : SharedPreferences.Editor {
        private val pending = mutableMapOf<String, Any?>()
        private val removals = mutableListOf<String>()
        private var clearAll = false

        override fun putString(key: String?, value: String?) = apply { pending[key!!] = value }
        override fun putStringSet(key: String?, values: MutableSet<String>?) = apply { pending[key!!] = values }
        override fun putInt(key: String?, value: Int) = apply { pending[key!!] = value }
        override fun putLong(key: String?, value: Long) = apply { pending[key!!] = value }
        override fun putFloat(key: String?, value: Float) = apply { pending[key!!] = value }
        override fun putBoolean(key: String?, value: Boolean) = apply { pending[key!!] = value }
        override fun remove(key: String?) = apply { removals.add(key!!) }
        override fun clear() = apply { clearAll = true }

        override fun commit(): Boolean {
            apply()
            return true
        }

        override fun apply() {
            if (clearAll) values.clear()
            removals.forEach { values.remove(it) }
            pending.forEach { (k, v) -> values[k] = v }
        }
    }
}

class TransferGarbageCollectorTest {

    private fun store() = TransferStateStore(FakeSharedPreferences())

    @Test
    fun sweep_removesOrphanedDownload_whenWorkIsNotActive() {
        val store = store()
        store.saveDownloadUri("download:/a/b.bin", "content://media/1")
        val gc = TransferGarbageCollector(store)
        val deleted = mutableListOf<String>()

        val removed = gc.sweep(isWorkActive = { false }, deletePendingDownload = { deleted.add(it) })

        assertEquals(setOf("download:/a/b.bin"), removed)
        assertEquals(listOf("content://media/1"), deleted)
        assertNull(store.downloadState("download:/a/b.bin"))
    }

    @Test
    fun sweep_removesOrphanedUpload_whenWorkIsNotActive() {
        val store = store()
        store.saveUploadSession("upload:/dest/file.bin", "session-123")
        store.saveUploadProgress("upload:/dest/file.bin", 500L)
        val gc = TransferGarbageCollector(store)

        val removed = gc.sweep(isWorkActive = { false }, deletePendingDownload = { })

        assertEquals(setOf("upload:/dest/file.bin"), removed)
        assertNull(store.uploadState("upload:/dest/file.bin"))
    }

    @Test
    fun sweep_neverTouchesAnActiveTransfer_evenIfCalledRepeatedly() {
        val store = store()
        store.saveDownloadUri("download:/in-progress.bin", "content://media/2")
        val gc = TransferGarbageCollector(store)

        val removed = gc.sweep(isWorkActive = { true }, deletePendingDownload = { fail("must not delete an active transfer") })

        assertTrue(removed.isEmpty())
        assertEquals("content://media/2", store.downloadState("download:/in-progress.bin")?.mediaUri)
    }

    @Test
    fun sweep_isNoOp_whenNothingIsTracked() {
        val gc = TransferGarbageCollector(store())
        val removed = gc.sweep(isWorkActive = { fail("no work name should ever be queried") }, deletePendingDownload = { })
        assertTrue(removed.isEmpty())
    }

    @Test
    fun cleanupCancelled_deletesMediaStoreRow_andClearsDownloadState() {
        val store = store()
        store.saveDownloadUri("download:/cancelled.bin", "content://media/3")
        val gc = TransferGarbageCollector(store)
        val deleted = mutableListOf<String>()

        gc.cleanupCancelled("download:/cancelled.bin") { deleted.add(it) }

        assertEquals(listOf("content://media/3"), deleted)
        assertNull(store.downloadState("download:/cancelled.bin"))
    }

    @Test
    fun cleanupCancelled_clearsUploadState_withoutTouchingMediaStore() {
        val store = store()
        store.saveUploadSession("upload:/cancelled-upload.bin", "session-abc")
        val gc = TransferGarbageCollector(store)

        gc.cleanupCancelled("upload:/cancelled-upload.bin") { fail("an upload has no MediaStore row to delete") }

        assertNull(store.uploadState("upload:/cancelled-upload.bin"))
    }

    @Test
    fun cleanupCancelled_onUnknownWorkName_isSafeNoOp() {
        val gc = TransferGarbageCollector(store())
        gc.cleanupCancelled("download:/never-started.bin") { fail("nothing to delete for an unknown work name") }
    }

    private fun fail(message: String): Nothing = throw AssertionError(message)
}
