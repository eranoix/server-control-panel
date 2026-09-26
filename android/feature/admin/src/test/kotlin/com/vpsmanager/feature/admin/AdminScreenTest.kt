package com.vpsmanager.feature.admin

import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.test.hasProgressBarRangeInfo
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onNodeWithText
import com.vpsmanager.core.sdui.parseScreen
import com.vpsmanager.data.sdui.SduiActionHttpResult
import com.vpsmanager.data.sdui.SduiDataResult
import com.vpsmanager.data.sdui.SduiScreenPort
import com.vpsmanager.data.sdui.SduiScreenResult
import com.vpsmanager.sdui.actionrunner.ComponentDataFetcher
import kotlinx.coroutines.awaitCancellation
import kotlinx.serialization.json.JsonObject
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Hand-written fake of the [SduiScreenPort] seam — there is no mocking
 * library in this repository, and there should not be: the pattern here is
 * the same one used by `FakeTrackControl` (`WebRtcSessionManagerTest`) and by
 * the `TelecomAccountPort` fake in `PhoneAccountRegistrarTest`.
 *
 * [action] returns a fixed error on purpose: these tests exercise what the
 * screen RENDERS for each [AdminUiState], never the action dispatch — whose
 * state machine is already covered by `ActionRunnerTest` (`:sdui`) and whose
 * HTTP mapping is covered by `SduiActionRepositoryTest` (`:data`).
 */
private class FakeScreenPort(private val outcome: suspend () -> SduiScreenResult) : SduiScreenPort {
    override suspend fun screen(sectionId: String): SduiScreenResult = outcome()

    override suspend fun action(actionId: String, requestBody: JsonObject): SduiActionHttpResult =
        SduiActionHttpResult.Error("nenhuma ação é despachada nestes testes de renderização")
}

/**
 * Renders [AdminScreen], the SDUI host, under Robolectric in EVERY
 * [AdminUiState].
 *
 * The faking happens at the DATA boundary ([SduiScreenPort]), not at the HTTP
 * boundary. The previous version of this test pointed a real
 * `SduiRepository`/`SduiDataRepository` pair at a local `MockWebServer`
 * because `SduiRepository` was a final class with no seam — and that dragged
 * `okhttp3` into a feature module, exactly the violation that the gate
 * (`scripts/check-mobile-bff-only.sh`) exists to prevent.
 *
 * WHAT IS LOST by raising the boundary: the HTTP -> [SduiScreenResult]
 * translation (404 -> `NotFound`, 403 -> `Forbidden`, body -> `parseScreen`)
 * is no longer re-exercised here. It is not left uncovered — it is precisely
 * the subject of `SduiRepositoryTest` in `:data`, where `MockWebServer` is
 * legitimate and the gate allows it. What this test covers, and only it
 * covers, is unchanged: `AdminViewModel` -> [AdminUiState] -> real `:sdui`
 * rendering.
 */
@RunWith(RobolectricTestRunner::class)
class AdminScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun viewModelFor(outcome: suspend () -> SduiScreenResult): AdminViewModel =
        AdminViewModel(
            sectionId = "scheduler.jobs",
            screenPort = FakeScreenPort(outcome),
            componentDataFetcher = ComponentDataFetcher { SduiDataResult.Empty },
        )

    private fun renderWith(outcome: suspend () -> SduiScreenResult) {
        composeRule.setContent {
            // Renders the SECTION HOST, not the whole AdminScreen: these
            // tests are about AdminUiState -> pixels, and assembling a fake
            // catalogue as well just to get here would couple each of them to
            // a second boundary they do not exercise.
            AdminSectionContent(sectionId = "scheduler.jobs", viewModel = viewModelFor(outcome))
        }
        composeRule.waitForIdle()
    }

    @Test
    fun `loading state shows while the fetch is still in flight`() {
        // The fetch never resolves, so the observed state is the initial one
        // — and the assertion is now about the progress indicator itself, not
        // merely about the existence of the root.
        renderWith { awaitCancellation() }

        composeRule.onNode(hasProgressBarRangeInfo(ProgressBarRangeInfo.Indeterminate)).assertExists()
    }

    /**
     * A 404 from `/screens/{id}` is NOT an error: the server answers 404
     * rather than 403 so as not to confirm the existence of a screen to
     * someone who may not see it, so this case is "the section is not yours",
     * which the screen says calmly and without a red button. The test pins
     * both halves: the calm text appears AND the generic error text ("Tentar
     * novamente") does NOT.
     */
    @Test
    fun `a not-found section reads as unavailable, never as a scary error`() {
        renderWith { SduiScreenResult.NotFound }

        composeRule.onNodeWithText("Section unavailable").assertExists()
        composeRule
            .onNodeWithText("This section is not available to your account. Pick another one in the selector above.")
            .assertExists()
        composeRule.onNodeWithText("Reload").assertExists()
        composeRule.onNodeWithText("Try again").assertDoesNotExist()
    }

    @Test
    fun `a forbidden section also reads as unavailable, pointing at the picker`() {
        renderWith { SduiScreenResult.Forbidden }

        composeRule.onNodeWithText("Section unavailable").assertExists()
        composeRule
            .onNodeWithText("You do not have permission to view this section. Pick another one in the selector above.")
            .assertExists()
    }

    @Test
    fun `a generic failure surfaces the repository's own reason verbatim`() {
        renderWith { SduiScreenResult.Error("Falha de conexão. Verifique a rede e tente novamente.") }

        composeRule.onNodeWithText("Falha de conexão. Verifique a rede e tente novamente.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `a successful fetch renders the envelope through the real SduiScreen renderer`() {
        val envelopeJson = """
            {"sdui_version":1,"screen":{"id":"scheduler.jobs","title":"Agendador","components":[
                {"type":"action","id":"refresh-jobs","label":"Atualizar","action_id":"scheduler.jobs.refresh"}
            ]}}
        """.trimIndent()

        renderWith { SduiScreenResult.Success(parseScreen(envelopeJson)) }

        composeRule.onNodeWithText("Atualizar").assertExists()
    }
}
