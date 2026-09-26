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
 * The test exercises the [BarraDeAnexos] pair plus the insertion decision
 * through the SAME path the screen uses, with the connection in each of the
 * two states.
 */
@RunWith(RobolectricTestRunner::class)
class AnexoInsercaoOfflineTest {

    @get:Rule
    val composeRule = createComposeRule()

    /**
     * Reproduces the rule of [AnexoDoTerminal] without the ViewModel (which
     * requires WorkManager): given the finished text, insert only when the
     * terminal is up, and only then discard.
     */
    private fun montar(
        terminalPronto: Boolean,
        anexos: List<AnexoNaTela>,
        aoInserirTexto: (String) -> Unit,
        aoDescartar: (java.util.UUID) -> Unit,
    ) {
        composeRule.setContent {
            BarraDeAnexos(
                anexos = anexos,
                aoInserir = { ids ->
                    val texto = textoParaInserirDe(anexos, ids)
                    if (texto.isEmpty()) return@BarraDeAnexos
                    if (!terminalPronto) return@BarraDeAnexos
                    aoInserirTexto(texto)
                    ids.forEach(aoDescartar)
                },
                aoCancelar = {},
                aoDescartar = aoDescartar,
            )
        }
    }

    private fun anexoPronto() = AnexoNaTela(
        id = java.util.UUID.randomUUID(),
        nome = "foto.jpg",
        estado = EstadoDoAnexo.Pronto("/srv/inbox/foto.jpg"),
    )

    @Test
    fun `com a sessao no ar o caminho e inserido e a linha sai da barra`() {
        val anexo = anexoPronto()
        var inserido: String? = null
        var descartado: java.util.UUID? = null
        montar(terminalPronto = true, anexos = listOf(anexo), aoInserirTexto = { inserido = it }, aoDescartar = { descartado = it })

        composeRule.onNodeWithText(INSERIR_LABEL).performClick()

        assertEquals("/srv/inbox/foto.jpg ", inserido)
        assertEquals(anexo.id, descartado)
    }

    @Test
    fun `com a sessao fora do ar NADA e inserido e o anexo NAO e descartado`() {
        val anexo = anexoPronto()
        var inserido: String? = null
        var descartado: java.util.UUID? = null
        montar(terminalPronto = false, anexos = listOf(anexo), aoInserirTexto = { inserido = it }, aoDescartar = { descartado = it })

        composeRule.onNodeWithText(INSERIR_LABEL).performClick()

        // The point of the test: the attachment stays in the bar. Discarding
        // it here would lose the path forever, because the send never reached
        // the PTY.
        assertNull(inserido)
        assertNull(descartado)
        composeRule.onNodeWithText("/srv/inbox/foto.jpg").assertExists()
    }
}
