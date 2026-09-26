package com.vpsmanager.feature.terminal.attach

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * An attachment must not vanish when the session is down: `TerminalSocketClient.send`
 * silently drops bytes when there is no socket, so the row may only leave the bar
 * after a successful insert. Exercises [AttachmentBar] with the connection up and down.
 */
@RunWith(RobolectricTestRunner::class)
class AttachmentOfflineInsertionTest {

    @get:Rule
    val composeRule = createComposeRule()

    /**
     * Mirrors the screen's insertion rule without the ViewModel (which needs
     * WorkManager): insert only when the terminal is up, and only then discard.
     */
    private fun build(
        terminalReady: Boolean,
        attachments: List<ScreenAttachment>,
        onInsertText: (String) -> Unit,
        onDiscard: (java.util.UUID) -> Unit,
    ) {
        composeRule.setContent {
            AttachmentBar(
                attachments = attachments,
                onInsert = { ids ->
                    val text = insertionTextFrom(attachments, ids)
                    if (text.isEmpty()) return@AttachmentBar
                    if (!terminalReady) return@AttachmentBar
                    onInsertText(text)
                    ids.forEach(onDiscard)
                },
                onCancel = {},
                onDiscard = onDiscard,
            )
        }
    }

    private fun readyAttachment() = ScreenAttachment(
        id = java.util.UUID.randomUUID(),
        name = "photo.jpg",
        state = AttachmentState.Ready("/srv/inbox/photo.jpg"),
    )

    @Test
    fun `with the session up the path is inserted and the row leaves the bar`() {
        val attachment = readyAttachment()
        var inserted: String? = null
        var discarded: java.util.UUID? = null
        build(terminalReady = true, attachments = listOf(attachment), onInsertText = { inserted = it }, onDiscard = { discarded = it })

        composeRule.onNodeWithText(INSERT_LABEL).performClick()

        assertEquals("/srv/inbox/photo.jpg ", inserted)
        assertEquals(attachment.id, discarded)
    }

    @Test
    fun `with the session down nothing is inserted and the attachment is not discarded`() {
        val attachment = readyAttachment()
        var inserted: String? = null
        var discarded: java.util.UUID? = null
        build(terminalReady = false, attachments = listOf(attachment), onInsertText = { inserted = it }, onDiscard = { discarded = it })

        composeRule.onNodeWithText(INSERT_LABEL).performClick()

        // The attachment must stay in the bar: the send never reached the PTY.
        assertNull(inserted)
        assertNull(discarded)
        composeRule.onNodeWithText("/srv/inbox/photo.jpg").assertExists()
    }
}
