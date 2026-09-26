package com.vpsmanager.feature.terminal.attach

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
 * The attachment bar rendered for real under Robolectric — with no emulator
 * and no device.
 *
 * The ZERO HEIGHT assertion is first class, not cosmetic: this bar sits
 * between the grid and the key bar, and the terminal screen has already paid
 * the price of permanent chrome once (160.8 dp of episodic controls that were
 * moved into the options sheet). If it costs height when there is no
 * attachment at all, it is a regression, however pretty it may look.
 */
@RunWith(RobolectricTestRunner::class)
class AttachmentBarScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun attachment(state: AttachmentState, name: String = "foto.jpg") =
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
    fun `sem anexo a barra nao existe na composicao`() {
        build(emptyList())
        composeRule.onNodeWithTag(ATTACHMENT_BAR_TAG).assertDoesNotExist()
    }

    @Test
    fun `envio em andamento mostra o percentual e oferece cancelar`() {
        build(listOf(attachment(AttachmentState.Uploading(40))))

        composeRule.onNodeWithText("foto.jpg").assertExists()
        composeRule.onNodeWithText("Uploading 40%").assertExists()
        composeRule.onNodeWithText("Cancel").assertExists()
    }

    @Test
    fun `anexo pronto mostra o caminho no servidor e o botao de inserir`() {
        build(listOf(attachment(AttachmentState.Ready("/opt/panel/data/mobile-inbox/foto.jpg"))))

        composeRule.onNodeWithText("/opt/panel/data/mobile-inbox/foto.jpg").assertExists()
        composeRule.onNodeWithText(INSERT_LABEL).assertExists()
    }

    @Test
    fun `tocar em inserir devolve o id daquele anexo`() {
        val ready = attachment(AttachmentState.Ready("/srv/inbox/a.png"))
        var inserted: List<UUID>? = null
        build(listOf(ready), onInsert = { inserted = it })

        composeRule.onNodeWithText(INSERT_LABEL).performClick()

        assertEquals(listOf(ready.id), inserted)
    }

    @Test
    fun `erro mostra o motivo por extenso, nao um falhou generico`() {
        build(listOf(attachment(AttachmentState.Failed("O servidor está sem espaço em disco. Libere espaço e envie de novo."))))

        composeRule.onNodeWithText(
            "O servidor está sem espaço em disco. Libere espaço e envie de novo.",
        ).assertExists()
    }

    @Test
    fun `com dois prontos aparece inserir todos`() {
        val a = attachment(AttachmentState.Ready("/srv/inbox/a.png"), name = "a.png")
        val b = attachment(AttachmentState.Ready("/srv/inbox/b.png"), name = "b.png")
        var inserted: List<UUID>? = null
        build(listOf(a, b), onInsert = { inserted = it })

        composeRule.onNodeWithText("$INSERT_ALL_LABEL (2)").performClick()

        assertEquals(listOf(a.id, b.id), inserted)
    }

    @Test
    fun `com um unico pronto NAO aparece inserir todos`() {
        build(listOf(attachment(AttachmentState.Ready("/srv/inbox/a.png"))))
        composeRule.onNodeWithText("$INSERT_ALL_LABEL (1)").assertDoesNotExist()
    }

    @Test
    fun `cancelar um envio devolve o id daquele anexo`() {
        val sending = attachment(AttachmentState.Uploading(10))
        var cancelled: UUID? = null
        build(listOf(sending), onCancel = { cancelled = it })

        composeRule.onNodeWithText("Cancel").performClick()

        assertEquals(sending.id, cancelled)
    }

    @Test
    fun `a folha de origem oferece as tres origens`() {
        composeRule.setContent {
            SourceSheetContent(onChoose = {}, onClose = {})
        }

        composeRule.onNodeWithText(CHOOSE_FILE_LABEL).assertExists()
        composeRule.onNodeWithText(CHOOSE_IMAGE_LABEL).assertExists()
        composeRule.onNodeWithText(TAKE_PHOTO_LABEL).assertExists()
    }
}
