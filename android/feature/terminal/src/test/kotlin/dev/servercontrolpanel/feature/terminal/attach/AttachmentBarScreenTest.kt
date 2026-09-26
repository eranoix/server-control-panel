package dev.servercontrolpanel.feature.terminal.attach

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import java.util.UUID
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * The attachment bar rendered under Robolectric. It sits between the grid and the
 * key bar, so with no attachments it must take zero height.
 */
@RunWith(RobolectricTestRunner::class)
class AttachmentBarScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun attachment(state: AttachmentState, name: String = "photo.jpg") =
        ScreenAttachment(id = UUID.randomUUID(), name = name, state = state)

    private fun build(
        attachments: List<ScreenAttachment>,
        onInsert: (List<UUID>) -> Unit = {},
        onCancel: (UUID) -> Unit = {},
        onDiscard: (UUID) -> Unit = {},
    ) {
        composeRule.setContent {
            Column(modifier = Modifier.fillMaxSize()) {
                AttachmentBar(
                    attachments = attachments,
                    onInsert = onInsert,
                    onCancel = onCancel,
                    onDiscard = onDiscard,
                )
            }
        }
    }

    @Test
    fun `with no attachment the bar is not in the composition`() {
        build(emptyList())
        composeRule.onNodeWithTag(ATTACHMENT_BAR_TAG).assertDoesNotExist()
    }

    @Test
    fun `an upload in progress shows the percentage and offers cancel`() {
        build(listOf(attachment(AttachmentState.Uploading(40))))

        composeRule.onNodeWithText("photo.jpg").assertExists()
        composeRule.onNodeWithText("Uploading 40%").assertExists()
        composeRule.onNodeWithText("Cancel").assertExists()
    }

    @Test
    fun `a ready attachment shows the server path and the insert button`() {
        build(listOf(attachment(AttachmentState.Ready("/opt/panel/data/mobile-inbox/photo.jpg"))))

        composeRule.onNodeWithText("/opt/panel/data/mobile-inbox/photo.jpg").assertExists()
        composeRule.onNodeWithText(INSERT_LABEL).assertExists()
    }

    @Test
    fun `tapping insert returns that attachment's id`() {
        val ready = attachment(AttachmentState.Ready("/srv/inbox/a.png"))
        var inserted: List<UUID>? = null
        build(listOf(ready), onInsert = { inserted = it })

        composeRule.onNodeWithText(INSERT_LABEL).performClick()

        assertEquals(listOf(ready.id), inserted)
    }

    @Test
    fun `an error shows the full reason, not a generic failure`() {
        build(listOf(attachment(AttachmentState.Failed("The server is out of disk space. Free some space and upload again."))))

        composeRule.onNodeWithText(
            "The server is out of disk space. Free some space and upload again.",
        ).assertExists()
    }

    @Test
    fun `with two ready attachments insert all appears`() {
        val a = attachment(AttachmentState.Ready("/srv/inbox/a.png"), name = "a.png")
        val b = attachment(AttachmentState.Ready("/srv/inbox/b.png"), name = "b.png")
        var inserted: List<UUID>? = null
        build(listOf(a, b), onInsert = { inserted = it })

        composeRule.onNodeWithText("$INSERT_ALL_LABEL (2)").performClick()

        assertEquals(listOf(a.id, b.id), inserted)
    }

    @Test
    fun `with a single ready attachment insert all does not appear`() {
        build(listOf(attachment(AttachmentState.Ready("/srv/inbox/a.png"))))
        composeRule.onNodeWithText("$INSERT_ALL_LABEL (1)").assertDoesNotExist()
    }

    @Test
    fun `cancelling an upload returns that attachment's id`() {
        val sending = attachment(AttachmentState.Uploading(10))
        var cancelled: UUID? = null
        build(listOf(sending), onCancel = { cancelled = it })

        composeRule.onNodeWithText("Cancel").performClick()

        assertEquals(sending.id, cancelled)
    }

    @Test
    fun `the source sheet offers the three sources`() {
        composeRule.setContent {
            SourceSheetContent(onChoose = {}, onClose = {})
        }

        composeRule.onNodeWithText(CHOOSE_FILE_LABEL).assertExists()
        composeRule.onNodeWithText(CHOOSE_IMAGE_LABEL).assertExists()
        composeRule.onNodeWithText(TAKE_PHOTO_LABEL).assertExists()
    }
}
