package dev.servercontrolpanel.sdui

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performScrollToIndex
import dev.servercontrolpanel.core.sdui.SduiEnvelope
import dev.servercontrolpanel.core.sdui.SduiJson
import dev.servercontrolpanel.core.sdui.parseScreen
import dev.servercontrolpanel.sdui.actionrunner.ActionInvoker
import dev.servercontrolpanel.sdui.actionrunner.ActionRunner
import dev.servercontrolpanel.sdui.actionrunner.ComponentDataFetcher
import dev.servercontrolpanel.sdui.actionrunner.ScreenRefetcher
import dev.servercontrolpanel.sdui.actionrunner.ScreenState
import dev.servercontrolpanel.sdui.registry.LocalActionRunner
import dev.servercontrolpanel.sdui.registry.LocalScreenState
import dev.servercontrolpanel.sdui.registry.SduiFixtures
import dev.servercontrolpanel.data.sdui.SduiActionHttpResult
import dev.servercontrolpanel.data.sdui.SduiDataResult
import kotlinx.serialization.json.JsonObject
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [SduiScreen] under Robolectric against the real SDUI fixtures. Data and
 * actions go through inert fakes provided via [LocalScreenState]/[LocalActionRunner],
 * the same seam `PayloadPreviewScreen` uses to stay off the network.
 */
@RunWith(RobolectricTestRunner::class)
class SduiScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun renderInertly(envelope: SduiEnvelope) = render(envelope, ComponentDataFetcher { SduiDataResult.Empty })

    private fun render(envelope: SduiEnvelope, fetcher: ComponentDataFetcher) {
        composeRule.setContent {
            val screenState = remember(envelope) {
                ScreenState(
                    initialEnvelope = envelope,
                    componentDataFetcher = fetcher,
                    screenRefetcher = ScreenRefetcher { envelope },
                )
            }
            val actionRunner = remember(screenState) {
                ActionRunner(
                    invoker = ActionInvoker { _, _ -> SduiActionHttpResult.Success(JsonObject(emptyMap())) },
                    screenState = screenState,
                )
            }
            CompositionLocalProvider(
                LocalActionRunner provides actionRunner,
                LocalScreenState provides screenState,
            ) {
                SduiScreen(envelope)
            }
        }
        composeRule.waitForIdle()
    }

    @Test
    fun `all-components fixture renders every one of its 7 components without crashing`() {
        val envelope = parseScreen(SduiFixtures.read("all-components.json"))

        renderInertly(envelope)

        // LazyColumn only composes visible items, so scroll each one into view first.
        composeRule.onNodeWithText("Nenhum container em execução").assertExists() // table (index 0)

        composeRule.onNode(hasScrollAction()).performScrollToIndex(1)
        composeRule.onNodeWithText("Nome da regra").assertExists() // form (index 1)

        composeRule.onNode(hasScrollAction()).performScrollToIndex(4)
        composeRule.onNodeWithText("Deploy").assertExists() // action (index 4)

        composeRule.onNode(hasScrollAction()).performScrollToIndex(6)
        composeRule.onRoot().assertExists() // chart + confirm_destructive (indices 5-6) render without crashing
    }

    /**
     * A table with rows must render inside [SduiScreen]'s `LazyColumn`: a nested
     * lazy list would crash with an infinite-height `IllegalStateException`. Real
     * rows are needed, since an empty fetcher never reaches the row branch.
     */
    @Test
    fun `a table with rows renders without a nested vertical scroll`() {
        val envelope = parseScreen(
            """
            {"sdui_version":1,"screen":{"id":"scheduler.jobs","title":"Scheduler","components":[
              {"type":"table","id":"jobs-table",
               "columns":[{"key":"name","label":"Name","kind":"text"},
                          {"key":"schedule","label":"Schedule","kind":"text"}],
               "rows_source":{"endpoint":"/api/mobile/v1/scheduler/jobs"},
               "row_actions":[{"action_id":"scheduler.job.run_now","label":"Run now"}],
               "empty_state":{"text":"No scheduled jobs yet."}}]}}
            """.trimIndent(),
        )
        val rows = SduiJson.parseToJsonElement(
            """{"rows":[{"id":"1","name":"daily-backup","schedule":"0 3 * * *"},
                       {"id":"2","name":"log-cleanup","schedule":"*/15 * * * *"}]}""",
        )

        render(envelope, ComponentDataFetcher { SduiDataResult.Success(rows) })

        composeRule.onNodeWithText("daily-backup").assertExists()
        composeRule.onNodeWithText("log-cleanup").assertExists()
    }

    @Test
    fun `a critical unknown component surfaces the needs-update card instead of crashing`() {
        val envelope = parseScreen(SduiFixtures.read("unknown-critical.json"))

        renderInertly(envelope)

        composeRule.onNodeWithText("This section needs a newer version of the app").assertExists()
        composeRule.onNodeWithText("Unrecognized type: gantt").assertExists()
    }

    @Test
    fun `a non-critical unknown component is silently skipped, the rest of the screen still renders`() {
        val envelope = parseScreen(SduiFixtures.read("unknown-noncritical.json"))

        renderInertly(envelope)

        composeRule.onRoot().assertExists()
    }
}
