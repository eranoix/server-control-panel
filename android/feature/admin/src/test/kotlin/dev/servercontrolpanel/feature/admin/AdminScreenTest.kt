package dev.servercontrolpanel.feature.admin

import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.test.hasProgressBarRangeInfo
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onNodeWithText
import dev.servercontrolpanel.core.sdui.parseScreen
import dev.servercontrolpanel.data.sdui.SduiActionHttpResult
import dev.servercontrolpanel.data.sdui.SduiDataResult
import dev.servercontrolpanel.data.sdui.SduiScreenPort
import dev.servercontrolpanel.data.sdui.SduiScreenResult
import dev.servercontrolpanel.sdui.actionrunner.ComponentDataFetcher
import kotlinx.coroutines.awaitCancellation
import kotlinx.serialization.json.JsonObject
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Hand-written fake of [SduiScreenPort] (the repo uses no mocking library).
 * [action] always fails: these tests cover rendering, not action dispatch.
 */
private class FakeScreenPort(private val outcome: suspend () -> SduiScreenResult) : SduiScreenPort {
    override suspend fun screen(sectionId: String): SduiScreenResult = outcome()

    override suspend fun action(actionId: String, requestBody: JsonObject): SduiActionHttpResult =
        SduiActionHttpResult.Error("no action is dispatched in these rendering tests")
}

/**
 * Renders the SDUI section host under Robolectric for every [AdminUiState].
 *
 * Fakes at the data boundary ([SduiScreenPort]) so no okhttp3 enters a feature
 * module; the HTTP to [SduiScreenResult] mapping is covered by `SduiRepositoryTest` in `:data`.
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
            // Only the section host, so these tests need no fake catalog.
            AdminSectionContent(sectionId = "scheduler.jobs", viewModel = viewModelFor(outcome))
        }
        composeRule.waitForIdle()
    }

    @Test
    fun `loading state shows while the fetch is still in flight`() {
        // The fetch never resolves, so the initial state stays visible.
        renderWith { awaitCancellation() }

        composeRule.onNode(hasProgressBarRangeInfo(ProgressBarRangeInfo.Indeterminate)).assertExists()
    }

    /**
     * A 404 means "not your section" (the server hides forbidden screens), so it
     * shows a calm notice and never the generic error's "Try again".
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
        renderWith { SduiScreenResult.Error("Connection failed. Check the network and try again.") }

        composeRule.onNodeWithText("Connection failed. Check the network and try again.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `a successful fetch renders the envelope through the real SduiScreen renderer`() {
        val envelopeJson = """
            {"sdui_version":1,"screen":{"id":"scheduler.jobs","title":"Scheduler","components":[
                {"type":"action","id":"refresh-jobs","label":"Refresh","action_id":"scheduler.jobs.refresh"}
            ]}}
        """.trimIndent()

        renderWith { SduiScreenResult.Success(parseScreen(envelopeJson)) }

        composeRule.onNodeWithText("Refresh").assertExists()
    }
}
