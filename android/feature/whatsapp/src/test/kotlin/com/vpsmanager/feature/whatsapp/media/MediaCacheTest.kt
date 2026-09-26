package com.vpsmanager.feature.whatsapp.media

import coil.annotation.ExperimentalCoilApi
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment

/**
 * Proves the cache-eviction requirement mechanically: the [coil.ImageLoader]
 * [MediaCache.imageLoader] builds has a disk cache with a real, finite
 * [coil.disk.DiskCache.getMaxSize] -- not `Long.MAX_VALUE` (Coil's
 * behavior when no cap is configured at all) -- so a chatty WhatsApp
 * history cannot grow the on-disk cache without bound.
 */
@RunWith(RobolectricTestRunner::class)
class MediaCacheTest {

    @OptIn(ExperimentalCoilApi::class)
    @Test
    fun `disk cache is capped at MediaCache MAX_CACHE_BYTES, not unbounded`() {
        val context = RuntimeEnvironment.getApplication()
        val imageLoader = MediaCache.imageLoader(context)

        val diskCache = imageLoader.diskCache
        assertTrue("expected a disk cache to be configured", diskCache != null)
        assertEquals(MediaCache.MAX_CACHE_BYTES, diskCache!!.maxSize)
        assertNotEquals(Long.MAX_VALUE, diskCache.maxSize)
    }

    @Test
    fun `cache directory is a subfolder, not the whole external files root`() {
        val context = RuntimeEnvironment.getApplication()
        val dir = MediaCache.directory(context)

        assertEquals(MediaCache.CACHE_SUBDIR, dir.name)
        assertTrue("cache directory should exist after MediaCache.directory()", dir.exists())
    }

    @Test
    fun `resolveUrl prefixes a relative BFF path with the server origin`() {
        val resolved = MediaCache.resolveUrl(
            serverBaseUrl = "https://vpsm.example.com",
            relativeUrl = "/api/mobile/v1/whatsapp/chats/123/media/456",
        )

        assertEquals("https://vpsm.example.com/api/mobile/v1/whatsapp/chats/123/media/456", resolved)
    }

    @Test
    fun `resolveUrl passes an already-absolute url through unchanged`() {
        val resolved = MediaCache.resolveUrl(
            serverBaseUrl = "https://vpsm.example.com",
            relativeUrl = "https://other-host.example.com/file.jpg",
        )

        assertEquals("https://other-host.example.com/file.jpg", resolved)
    }

    @Test
    fun `resolveUrl with a null serverBaseUrl yields a still-relative string`() {
        val resolved = MediaCache.resolveUrl(
            serverBaseUrl = null,
            relativeUrl = "/api/mobile/v1/whatsapp/chats/123/media/456",
        )

        assertEquals("/api/mobile/v1/whatsapp/chats/123/media/456", resolved)
    }
}
