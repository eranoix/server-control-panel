package com.vpsmanager.feature.terminal.attach

import java.time.Instant
import java.time.ZoneId
import java.util.UUID
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure attachment logic: the file name on the server and the exact text inserted on
 * the command line. Mistakes here cause silent damage (overwriting an earlier
 * attachment, breaking the shell argument).
 */
class AttachmentNameAndTextTest {

    private val instant = Instant.parse("2026-09-06T19:30:45Z")
    private val utc = ZoneId.of("UTC")

    @Test
    fun `the destination name is timestamped so it never overwrites an earlier attachment`() {
        assertEquals("20260906-193045-IMG_0001.jpg", destinationNameFor("IMG_0001.jpg", instant, utc))
    }

    @Test
    fun `two photos with the same name get different destinations`() {
        val first = destinationNameFor("IMG_0001.jpg", instant, utc)
        val second = destinationNameFor("IMG_0001.jpg", instant.plusSeconds(1), utc)
        // The server's `rename` overwrites silently, so a path already handed out
        // would start pointing at different content.
        assertTrue(first != second)
    }

    @Test
    fun `spaces in the name are preserved`() {
        // Spaces are handled by shell quoting at insertion time; the file keeps the
        // name the user saw in the picker.
        val name = destinationNameFor("Captura de tela.png", instant, utc)
        assertTrue(name.endsWith("-Captura de tela.png"))
    }

    @Test
    fun `path separators and dot-dot are stripped from the name`() {
        // InitUpload rejects the whole upload if the name tries to pick a folder.
        assertEquals("20260906-193045-passwd", destinationNameFor("../../etc/passwd", instant, utc))
        assertEquals("20260906-193045-note.txt", destinationNameFor("C:\\Users\\a\\note.txt", instant, utc))
    }

    @Test
    fun `an empty name gets a default name instead of just the timestamp`() {
        assertEquals("20260906-193045-anexo", destinationNameFor("   ", instant, utc))
    }

    private fun attachment(state: AttachmentState, name: String = "x") =
        ScreenAttachment(id = UUID.randomUUID(), name = name, state = state)

    @Test
    fun `only ready attachments go into the inserted text`() {
        val ready = attachment(AttachmentState.Ready("/srv/inbox/a.png"))
        val sending = attachment(AttachmentState.Uploading(40))
        val failed = attachment(AttachmentState.Failed("no space left"))

        val text = insertionTextFrom(listOf(ready, sending, failed), listOf(ready.id, sending.id, failed.id))

        assertEquals("/srv/inbox/a.png ", text)
    }

    @Test
    fun `a path with a space reaches the command line quoted`() {
        val ready = attachment(AttachmentState.Ready("/srv/inbox/20260906-193045-Screen capture.png"))

        val text = insertionTextFrom(listOf(ready), listOf(ready.id))

        assertEquals("'/srv/inbox/20260906-193045-Screen capture.png' ", text)
    }

    @Test
    fun `several ready attachments are inserted in the order they were attached`() {
        val a = attachment(AttachmentState.Ready("/srv/inbox/a.png"))
        val b = attachment(AttachmentState.Ready("/srv/inbox/b b.png"))

        val text = insertionTextFrom(listOf(a, b), listOf(a.id, b.id))

        assertEquals("/srv/inbox/a.png '/srv/inbox/b b.png' ", text)
    }

    @Test
    fun `nothing ready inserts nothing`() {
        val sending = attachment(AttachmentState.Uploading(10))
        assertEquals("", insertionTextFrom(listOf(sending), listOf(sending.id)))
    }

    @Test
    fun `the percentage is unknown when the provider did not report the size`() {
        assertEquals(UNKNOWN_PERCENT, percentOf(sent = 100, total = 0))
        assertEquals(50, percentOf(sent = 50, total = 100))
        assertEquals(100, percentOf(sent = 100, total = 100))
    }

    @Test
    fun `the state becomes a sentence saying what happened, never a generic failure`() {
        assertEquals("Uploading 40%", stateDescription(AttachmentState.Uploading(40)))
        assertEquals("Uploading…", stateDescription(AttachmentState.Uploading(UNKNOWN_PERCENT)))
        assertEquals("/srv/inbox/a.png", stateDescription(AttachmentState.Ready("/srv/inbox/a.png")))
        assertEquals(
            "The server is out of disk space. Free some space and upload again.",
            stateDescription(AttachmentState.Failed("The server is out of disk space. Free some space and upload again.")),
        )
        assertEquals("Upload canceled.", stateDescription(AttachmentState.Cancelled))
    }
}
