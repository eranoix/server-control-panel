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
class BarraDeAnexosScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun anexo(estado: EstadoDoAnexo, nome: String = "foto.jpg") =
        AnexoNaTela(id = UUID.randomUUID(), nome = nome, estado = estado)

    private fun montar(
        anexos: List<AnexoNaTela>,
        aoInserir: (List<UUID>) -> Unit = {},
        aoCancelar: (UUID) -> Unit = {},
        aoDescartar: (UUID) -> Unit = {},
    ) {
        composeRule.setContent {
            Column(modifier = Modifier.fillMaxSize()) {
                BarraDeAnexos(
                    anexos = anexos,
                    aoInserir = aoInserir,
                    aoCancelar = aoCancelar,
                    aoDescartar = aoDescartar,
                )
            }
        }
    }

    @Test
    fun `sem anexo a barra nao existe na composicao`() {
        montar(emptyList())
        composeRule.onNodeWithTag(BARRA_ANEXOS_TAG).assertDoesNotExist()
    }

    @Test
    fun `envio em andamento mostra o percentual e oferece cancelar`() {
        montar(listOf(anexo(EstadoDoAnexo.Enviando(40))))

        composeRule.onNodeWithText("foto.jpg").assertExists()
        composeRule.onNodeWithText("Uploading 40%").assertExists()
        composeRule.onNodeWithText("Cancel").assertExists()
    }

    @Test
    fun `anexo pronto mostra o caminho no servidor e o botao de inserir`() {
        montar(listOf(anexo(EstadoDoAnexo.Pronto("/opt/panel/data/mobile-inbox/foto.jpg"))))

        composeRule.onNodeWithText("/opt/panel/data/mobile-inbox/foto.jpg").assertExists()
        composeRule.onNodeWithText(INSERIR_LABEL).assertExists()
    }

    @Test
    fun `tocar em inserir devolve o id daquele anexo`() {
        val pronto = anexo(EstadoDoAnexo.Pronto("/srv/inbox/a.png"))
        var inseridos: List<UUID>? = null
        montar(listOf(pronto), aoInserir = { inseridos = it })

        composeRule.onNodeWithText(INSERIR_LABEL).performClick()

        assertEquals(listOf(pronto.id), inseridos)
    }

    @Test
    fun `erro mostra o motivo por extenso, nao um falhou generico`() {
        montar(listOf(anexo(EstadoDoAnexo.Falhou("O servidor está sem espaço em disco. Libere espaço e envie de novo."))))

        composeRule.onNodeWithText(
            "O servidor está sem espaço em disco. Libere espaço e envie de novo.",
        ).assertExists()
    }

    @Test
    fun `com dois prontos aparece inserir todos`() {
        val a = anexo(EstadoDoAnexo.Pronto("/srv/inbox/a.png"), nome = "a.png")
        val b = anexo(EstadoDoAnexo.Pronto("/srv/inbox/b.png"), nome = "b.png")
        var inseridos: List<UUID>? = null
        montar(listOf(a, b), aoInserir = { inseridos = it })

        composeRule.onNodeWithText("$INSERIR_TODOS_LABEL (2)").performClick()

        assertEquals(listOf(a.id, b.id), inseridos)
    }

    @Test
    fun `com um unico pronto NAO aparece inserir todos`() {
        montar(listOf(anexo(EstadoDoAnexo.Pronto("/srv/inbox/a.png"))))
        composeRule.onNodeWithText("$INSERIR_TODOS_LABEL (1)").assertDoesNotExist()
    }

    @Test
    fun `cancelar um envio devolve o id daquele anexo`() {
        val enviando = anexo(EstadoDoAnexo.Enviando(10))
        var cancelado: UUID? = null
        montar(listOf(enviando), aoCancelar = { cancelado = it })

        composeRule.onNodeWithText("Cancel").performClick()

        assertEquals(enviando.id, cancelado)
    }

    @Test
    fun `a folha de origem oferece as tres origens`() {
        composeRule.setContent {
            ConteudoDaFolhaDeOrigem(aoEscolher = {}, aoFechar = {})
        }

        composeRule.onNodeWithText(ESCOLHER_ARQUIVO_LABEL).assertExists()
        composeRule.onNodeWithText(ESCOLHER_IMAGEM_LABEL).assertExists()
        composeRule.onNodeWithText(TIRAR_FOTO_LABEL).assertExists()
    }
}
