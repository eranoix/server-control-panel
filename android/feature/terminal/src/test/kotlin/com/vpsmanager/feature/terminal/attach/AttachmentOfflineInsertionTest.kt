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
 * The guard that stops an attachment from vanishing when the session is down.
 *
 * `TerminalSocketClient.send` is literally `socket?.sendBytes(bytes)`: with no
 * socket, the bytes are DISCARDED silently, with no exception and no return
 * value. The first version of this screen inserted anyway and removed the row
 * from the bar — the path evaporated: nothing on the command line, nothing in
 * the bar, nothing explaining it. Reproduced on the emulator right after
 * reinstalling the APK, with the session still reconnecting.
 *
 * The test exercises the [AttachmentBar] pair plus the insertion decision
 * through the SAME path the screen uses, with the connection in each of the
 * two states.
 */
@RunWith(RobolectricTestRunner::class)
class AttachmentOfflineInsertionTest {

    @get:Rule
    val composeRule = createComposeRule()

    /**
     * Reproduces the rule of [TerminalAttachment] without the ViewModel (which
     * requires WorkManager): given the finished text, insert only when the
     * terminal is up, and only then discard.
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
        name = "foto.jpg",
        state = AttachmentState.Ready("/srv/inbox/foto.jpg"),
    )

    @Test
    fun `com a sessao no ar o caminho e inserido e a linha sai da barra`() {
        val attachment = readyAttachment()
        var inserted: String? = null
        var discarded: java.util.UUID? = null
        build(terminalReady = true, attachments = listOf(attachment), onInsertText = { inserted = it }, onDiscard = { discarded = it })

        composeRule.onNodeWithText(INSERT_LABEL).performClick()

        assertEquals("/srv/inbox/foto.jpg ", inserted)
        assertEquals(attachment.id, discarded)
    }

    @Test
    fun `com a sessao fora do ar NADA e inserido e o anexo NAO e descartado`() {
        val attachment = readyAttachment()
        var inserted: String? = null
        var discarded: java.util.UUID? = null
        build(terminalReady = false, attachments = listOf(attachment), onInsertText = { inserted = it }, onDiscard = { discarded = it })

        composeRule.onNodeWithText(INSERT_LABEL).performClick()

        // The point of the test: the attachment stays in the bar. Discarding
        // it here would lose the path forever, because the send never reached
        // the PTY.
        assertNull(inserted)
        assertNull(discarded)
        composeRule.onNodeWithText("/srv/inbox/foto.jpg").assertExists()
    }
}
