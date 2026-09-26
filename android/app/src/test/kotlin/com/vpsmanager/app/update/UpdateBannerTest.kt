package com.vpsmanager.app.update

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.vpsmanager.data.update.UpdateRecovery
import com.vpsmanager.data.update.UpdateState
import com.vpsmanager.data.update.formatDownloadSize
import com.vpsmanager.designsystem.VpsManagerTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The update banner.
 *
 * The test that matters most here is the SIZE one: on a bad connection,
 * "1.4 MB" is the difference between tapping now and putting it off, and
 * showing the size of the rebuilt APK (31 MB) instead of what travels
 * would be lying in exactly the direction that makes the owner never
 * update.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class, qualifiers = "w411dp-h891dp-xxhdpi")
class UpdateBannerTest {

    @get:Rule
    val composeRule = createComposeRule()

    // ------------------------------------------------------------------
    // The number
    // ------------------------------------------------------------------

    /**
     * The three numbers are the ones MEASURED in this project
     * (`docs/android-atualizacao-incremental.md`): the 0.1.5→0.1.6 patch, the
     * full one, and the raw APK. The banner has to say exactly what the
     * documentation says — in MiB the patch would become "1.3 MB" and the
     * owner would see a number different from the one the device shows for
     * the same file.
     */
    @Test
    fun `o tamanho aparece em MB com uma casa e virgula`() {
        assertEquals("1,4 MB", formatDownloadSize(1_400_329))
        assertEquals("10,0 MB", formatDownloadSize(10_029_237))
        assertEquals("31,1 MB", formatDownloadSize(31_135_416))
    }

    @Test
    fun `abaixo de um mega o numero vira KB, porque zero virgula um MB nao diz nada`() {
        assertEquals("819 KB", formatDownloadSize(819_200))
        assertEquals("512 B", formatDownloadSize(512))
    }

    @Test
    fun `o banner anuncia a versao e o tamanho do que vai trafegar`() {
        composeRule.setContent {
            VpsManagerTheme {
                UpdateBanner(
                    state = UpdateState.Available(versionName = "0.1.7", downloadBytes = 1_400_329, incremental = true),
                    onUpdateClick = {},
                    onCancelClick = {},
                    onRecoveryClick = {},
                )
            }
        }

        composeRule.onNodeWithText("Version 0.1.7 available — 1,4 MB").assertIsDisplayed()
        composeRule.onNodeWithText("Update").assertIsDisplayed()
    }

    // ------------------------------------------------------------------
    // Each rung of the ladder has its own banner
    // ------------------------------------------------------------------

    @Test
    fun `nada a fazer nao desenha faixa nenhuma`() {
        assertNull(bannerContentFor(UpdateState.Idle))
        assertNull(
            "verificar é rotina de fundo; piscar 'verificando' a cada abertura vira ruído",
            bannerContentFor(UpdateState.Checking),
        )
    }

    @Test
    fun `baixando mostra progresso e deixa cancelar`() {
        val content = bannerContentFor(
            UpdateState.Downloading(versionName = "0.1.7", downloadedBytes = 700_000, totalBytes = 1_400_329),
        )

        checkNotNull(content)
        assertEquals(0.5f, content.progress!!, 0.01f)
        assertEquals(listOf(UpdateBannerAction.Cancel), content.actions)
        assertTrue(content.text, content.text.contains("700 KB of 1,4 MB"))
    }

    @Test
    fun `aplicar e instalar mostram giro e nenhum botao — nao ha o que cancelar ali`() {
        val aplicando = checkNotNull(bannerContentFor(UpdateState.Applying("0.1.7")))
        assertTrue(aplicando.spinner)
        assertTrue(aplicando.actions.isEmpty())

        val instalando = checkNotNull(bannerContentFor(UpdateState.Installing("0.1.7")))
        assertTrue(instalando.spinner)
        assertTrue(instalando.actions.isEmpty())
    }

    @Test
    fun `falta de espaco oferece liberar espaco E tentar de novo`() {
        val content = checkNotNull(
            bannerContentFor(
                UpdateState.Failed(
                    "Falta espaço: libere 5,5 MB e tente de novo.",
                    canRetry = true,
                    recovery = UpdateRecovery.FREE_SPACE,
                ),
            ),
        )

        assertEquals(
            listOf(UpdateBannerAction.Recover("Free up space", UpdateRecovery.FREE_SPACE), UpdateBannerAction.Retry),
            content.actions,
        )
        assertTrue(content.text.contains("5,5 MB"))
    }

    @Test
    fun `fontes desconhecidas negada oferece o atalho para o interruptor`() {
        val content = checkNotNull(
            bannerContentFor(UpdateState.Failed("permissão", canRetry = true, recovery = UpdateRecovery.ALLOW_UNKNOWN_SOURCES)),
        )

        assertTrue(content.actions.first() is UpdateBannerAction.Recover)
        assertEquals("Allow", content.actions.first().label)
    }

    @Test
    fun `sideload bloqueado nao oferece tentar de novo — seria empurrar para o mesmo muro`() {
        val content = checkNotNull(
            bannerContentFor(UpdateState.Failed("bloqueado", canRetry = false, recovery = UpdateRecovery.USE_BROWSER)),
        )

        assertEquals(listOf(UpdateBannerAction.Recover("How to install", UpdateRecovery.USE_BROWSER)), content.actions)
    }

    @Test
    fun `instalacao falhada leva ao diagnostico, onde a mensagem do sistema cabe inteira`() {
        val content = checkNotNull(
            bannerContentFor(
                UpdateState.Failed("A instalação falhou: ...", canRetry = true, recovery = UpdateRecovery.SHOW_DIAGNOSTICS),
            ),
        )

        assertEquals(
            listOf(UpdateBannerAction.Recover("Diagnostics", UpdateRecovery.SHOW_DIAGNOSTICS), UpdateBannerAction.Retry),
            content.actions,
        )
    }

    // ------------------------------------------------------------------
    // The taps land where they should
    // ------------------------------------------------------------------

    @Test
    fun `tocar em Atualizar dispara o download e nao o cancelamento`() {
        var atualizou = 0
        var cancelou = 0
        composeRule.setContent {
            VpsManagerTheme {
                UpdateBanner(
                    state = UpdateState.Available("0.1.7", 1_400_329, incremental = true),
                    onUpdateClick = { atualizou++ },
                    onCancelClick = { cancelou++ },
                    onRecoveryClick = {},
                )
            }
        }

        composeRule.onNodeWithText("Update").performClick()

        assertEquals(1, atualizou)
        assertEquals(0, cancelou)
    }

    @Test
    fun `tocar em Cancelar durante o download cancela`() {
        var cancelou = 0
        composeRule.setContent {
            VpsManagerTheme {
                UpdateBanner(
                    state = UpdateState.Downloading("0.1.7", 700_000, 1_400_329),
                    onUpdateClick = {},
                    onCancelClick = { cancelou++ },
                    onRecoveryClick = {},
                )
            }
        }

        composeRule.onNodeWithText("Cancel").performClick()

        assertEquals(1, cancelou)
    }

    @Test
    fun `tocar na saida de uma falha entrega QUAL saida foi pedida`() {
        var pedida: UpdateRecovery? = null
        composeRule.setContent {
            VpsManagerTheme {
                UpdateBanner(
                    state = UpdateState.Failed("sem permissão", canRetry = true, recovery = UpdateRecovery.ALLOW_UNKNOWN_SOURCES),
                    onUpdateClick = {},
                    onCancelClick = {},
                    onRecoveryClick = { pedida = it },
                )
            }
        }

        composeRule.onNodeWithText("Allow").performClick()

        assertEquals(UpdateRecovery.ALLOW_UNKNOWN_SOURCES, pedida)
    }

    /** "Try again" is the same path as "Update" — the coordinator resumes the partial download. */
    @Test
    fun `tentar de novo reusa o caminho de atualizar`() {
        var atualizou = 0
        composeRule.setContent {
            VpsManagerTheme {
                UpdateBanner(
                    state = UpdateState.Failed("Falha de conexão.", canRetry = true),
                    onUpdateClick = { atualizou++ },
                    onCancelClick = {},
                    onRecoveryClick = {},
                )
            }
        }

        composeRule.onNodeWithText("Try again").performClick()

        assertEquals(1, atualizou)
    }
}
