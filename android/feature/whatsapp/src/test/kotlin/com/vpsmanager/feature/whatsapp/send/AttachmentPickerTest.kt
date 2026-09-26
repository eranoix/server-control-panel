package com.vpsmanager.feature.whatsapp.send

import android.database.MatrixCursor
import android.provider.OpenableColumns
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class AttachmentPickerTest {

    private fun cursorWith(name: String?, size: Long?): MatrixCursor {
        val cursor = MatrixCursor(arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE))
        cursor.addRow(arrayOf<Any?>(name, size))
        return cursor
    }

    @Test
    fun `photo metadata reports image msgType from mime type`() {
        val result = resolveAttachmentMetadata(cursorWith("praia.jpg", 204_800L), "image/jpeg", "fallback")

        assertEquals("praia.jpg", result.filename)
        assertEquals("image/jpeg", result.mimeType)
        assertEquals(204_800L, result.sizeBytes)
        assertEquals("image", result.msgType)
    }

    @Test
    fun `video metadata reports video msgType from mime type`() {
        val result = resolveAttachmentMetadata(cursorWith("clipe.mp4", 5_000_000L), "video/mp4", "fallback")

        assertEquals("clipe.mp4", result.filename)
        assertEquals("video", result.msgType)
    }

    @Test
    fun `document metadata reports document msgType for a non-media mime type`() {
        val result = resolveAttachmentMetadata(cursorWith("relatorio.pdf", 10_240L), "application/pdf", "fallback")

        assertEquals("relatorio.pdf", result.filename)
        assertEquals("document", result.msgType)
    }

    @Test
    fun `a null mime type resolves to document`() {
        val result = resolveAttachmentMetadata(cursorWith("arquivo", 1L), null, "fallback")

        assertEquals("document", result.msgType)
    }

    @Test
    fun `a null cursor falls back to the caller-provided name with no size`() {
        val result = resolveAttachmentMetadata(null, "image/png", "fallback.png")

        assertEquals("fallback.png", result.filename)
        assertNull(result.sizeBytes)
        assertEquals("image", result.msgType)
    }

    @Test
    fun `a blank display name column falls back to the caller-provided name`() {
        val result = resolveAttachmentMetadata(cursorWith("", 512L), "audio/mp4", "fallback.m4a")

        assertEquals("fallback.m4a", result.filename)
        assertEquals(512L, result.sizeBytes)
        assertEquals("audio", result.msgType)
    }

    @Test
    fun `a missing size column leaves sizeBytes null`() {
        val cursor = MatrixCursor(arrayOf(OpenableColumns.DISPLAY_NAME))
        cursor.addRow(arrayOf("nota.txt"))

        val result = resolveAttachmentMetadata(cursor, "text/plain", "fallback")

        assertEquals("nota.txt", result.filename)
        assertNull(result.sizeBytes)
        assertEquals("document", result.msgType)
    }
}
