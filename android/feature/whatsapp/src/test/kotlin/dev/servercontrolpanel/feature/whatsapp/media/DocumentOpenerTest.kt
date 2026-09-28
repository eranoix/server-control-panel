package dev.servercontrolpanel.feature.whatsapp.media

import android.content.Intent
import android.net.Uri
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.robolectric.RobolectricTestRunner
import org.junit.runner.RunWith

@RunWith(RobolectricTestRunner::class)
class DocumentOpenerTest {

    @Test
    fun `built intent targets a content uri, never file`() {
        val contentUri = Uri.parse("content://dev.servercontrolpanel.app.whatsappmedia.fileprovider/whatsapp_media/report.pdf")

        val intent = DocumentOpener.buildViewIntent(contentUri, "application/pdf")

        assertEquals(Intent.ACTION_VIEW, intent.action)
        assertEquals("content", intent.data?.scheme)
        assertNotEquals("file", intent.data?.scheme)
        assertEquals("application/pdf", intent.type)
    }

    @Test
    fun `built intent grants a revocable read permission`() {
        val contentUri = Uri.parse("content://dev.servercontrolpanel.app.whatsappmedia.fileprovider/whatsapp_media/report.pdf")

        val intent = DocumentOpener.buildViewIntent(contentUri, "application/pdf")

        assertTrue((intent.flags and Intent.FLAG_GRANT_READ_URI_PERMISSION) != 0)
    }

    @Test
    fun `a null mime type falls back to a generic binary type instead of crashing`() {
        val contentUri = Uri.parse("content://dev.servercontrolpanel.app.whatsappmedia.fileprovider/whatsapp_media/file")

        val intent = DocumentOpener.buildViewIntent(contentUri, mimeType = null)

        assertEquals("application/octet-stream", intent.type)
    }
}
