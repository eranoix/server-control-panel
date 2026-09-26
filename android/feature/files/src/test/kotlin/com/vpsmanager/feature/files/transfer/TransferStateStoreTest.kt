package com.vpsmanager.feature.files.transfer

import android.content.SharedPreferences
import org.junit.Assert.assertEquals
import org.junit.Test

class TransferStateStoreTest {

    // Minimal fake -- exercises only the get/edit/apply surface TransferStateStore
    // actually calls, unlike TransferGarbageCollectorTest's fuller fake.
    private class MinimalFakeSharedPreferences : SharedPreferences {
        val values = mutableMapOf<String, Any?>()
        override fun getAll(): MutableMap<String, *> = values.toMutableMap()
        override fun getString(key: String?, defValue: String?) = values[key] as? String ?: defValue
        override fun getStringSet(key: String?, defValues: MutableSet<String>?) = defValues
        override fun getInt(key: String?, defValue: Int) = values[key] as? Int ?: defValue
        override fun getLong(key: String?, defValue: Long) = values[key] as? Long ?: defValue
        override fun getFloat(key: String?, defValue: Float) = values[key] as? Float ?: defValue
        override fun getBoolean(key: String?, defValue: Boolean) = values[key] as? Boolean ?: defValue
        override fun contains(key: String?) = values.containsKey(key)
        override fun registerOnSharedPreferenceChangeListener(listener: SharedPreferences.OnSharedPreferenceChangeListener?) = Unit
        override fun unregisterOnSharedPreferenceChangeListener(listener: SharedPreferences.OnSharedPreferenceChangeListener?) = Unit

        override fun edit(): SharedPreferences.Editor = object : SharedPreferences.Editor {
            override fun putString(key: String?, value: String?) = apply { values[key!!] = value }
            override fun putStringSet(key: String?, values: MutableSet<String>?) = apply { this@MinimalFakeSharedPreferences.values[key!!] = values }
            override fun putInt(key: String?, value: Int) = apply { values[key!!] = value }
            override fun putLong(key: String?, value: Long) = apply { values[key!!] = value }
            override fun putFloat(key: String?, value: Float) = apply { values[key!!] = value }
            override fun putBoolean(key: String?, value: Boolean) = apply { values[key!!] = value }
            override fun remove(key: String?) = apply { values.remove(key) }
            override fun clear() = apply { values.clear() }
            override fun commit(): Boolean = true
            override fun apply() = Unit
        }
    }

    @Test
    fun allTrackedWorkNames_recoversWorkNamesThatThemselvesContainColons() {
        val store = TransferStateStore(MinimalFakeSharedPreferences())
        // Download work names embed a server path, which may contain colons
        // (e.g. a Windows-style drive-letter path relayed through a mixed
        // deployment) -- and upload work names embed "destDir/filename" with
        // no colon at all. Both must round-trip through allTrackedWorkNames.
        store.saveDownloadUri("download:/srv/a:b.bin", "content://media/1")
        store.saveUploadSession("upload:/dest/file.bin", "session-1")

        val names = store.allTrackedWorkNames()

        assertEquals(setOf("download:/srv/a:b.bin", "upload:/dest/file.bin"), names)
    }

    @Test
    fun allTrackedWorkNames_isEmpty_whenNothingPersisted() {
        val store = TransferStateStore(MinimalFakeSharedPreferences())
        assertEquals(emptySet<String>(), store.allTrackedWorkNames())
    }

    @Test
    fun allTrackedWorkNames_oneEntryPerWorkName_evenWithMultipleKeys() {
        val store = TransferStateStore(MinimalFakeSharedPreferences())
        // saveUploadSession writes both KEY_SESSION_ID and KEY_BYTES_UPLOADED
        // for the same work name -- must not be double-counted.
        store.saveUploadSession("upload:/dest/file.bin", "session-1")
        store.saveUploadProgress("upload:/dest/file.bin", 200L)

        assertEquals(setOf("upload:/dest/file.bin"), store.allTrackedWorkNames())
    }
}
