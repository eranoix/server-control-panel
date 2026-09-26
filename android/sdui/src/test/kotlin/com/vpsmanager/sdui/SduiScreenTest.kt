package com.vpsmanager.sdui

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performScrollToIndex
import com.vpsmanager.core.sdui.SduiEnvelope
import com.vpsmanager.core.sdui.SduiJson
import com.vpsmanager.core.sdui.parseScreen
import com.vpsmanager.sdui.actionrunner.ActionInvoker
import com.vpsmanager.sdui.actionrunner.ActionRunner
import com.vpsmanager.sdui.actionrunner.ComponentDataFetcher
import com.vpsmanager.sdui.actionrunner.ScreenRefetcher
import com.vpsmanager.sdui.actionrunner.ScreenState
import com.vpsmanager.sdui.registry.LocalActionRunner
import com.vpsmanager.sdui.registry.LocalScreenState
import com.vpsmanager.sdui.registry.SduiFixtures
import com.vpsmanager.data.sdui.SduiActionHttpResult
import com.vpsmanager.data.sdui.SduiDataResult
import kotlinx.serialization.json.JsonObject
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [SduiScreen] under Robolectric against the real fixture corpus in
 * `contracts/sdui/fixtures` -- never composed end to end before this. Every
 * read/write component reached here resolves through an inert
 * [ComponentDataFetcher]/[ActionInvoker] pair provided via
 * [LocalScreenState]/[LocalActionRunner], the exact seam the shipped
 * `PayloadPreviewScreen` already uses to keep a pasted payload from ever
 * hitting the real network -- see [ComponentDataSource.rememberComponentDataState],
 * which prefers `LocalScreenState.current?.componentDataFetcher` over a real,
 * network-hitting `SduiDataRepository()` whenever a [ScreenState] is provided.
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

        // The screen's LazyColumn only composes what is on/near screen --
        // each assertion below scrolls its component's index into view first, rather
        // than assuming everything the JSON declares composes in a single pass.
        composeRule.onNodeWithText("Nenhum container em execução").assertExists() // table (index 0)

        composeRule.onNode(hasScrollAction()).performScrollToIndex(1)
        composeRule.onNodeWithText("Nome da regra").assertExists() // form (index 1)

        composeRule.onNode(hasScrollAction()).performScrollToIndex(4)
        composeRule.onNodeWithText("Deploy").assertExists() // action (index 4)

        composeRule.onNode(hasScrollAction()).performScrollToIndex(6)
        composeRule.onRoot().assertExists() // chart + confirm_destructive (indices 5-6) render without crashing
    }

    /**
     * Regression: the Admin screen killed the app as soon as the table had ROWS.
     *
     * [TableComponent] and [com.vpsmanager.sdui.list.ListComponent] built the
     * rows in a `LazyColumn` — inside an item of [SduiScreen]'s `LazyColumn`,
     * which gives the child an infinite maximum height. Compose forbids two
     * nested vertical scrolls and kills the process with
     * `IllegalStateException("Vertically scrollable component was measured
     * with an infinity maximum height constraints")`.
     *
     * WHY THE SUITE DID NOT CATCH IT. Every existing render test uses a
     * [ComponentDataFetcher] that returns [SduiDataResult.Empty]: the table
     * stopped at the `EmptyBlock` and the branch containing the inner
     * `LazyColumn` was never composed. Only with real rows — the user's case —
     * does the defect show up; that is why this test feeds real rows.
     */
    @Test
    fun `uma tabela COM linhas renderiza — nao pode haver scroll vertical aninhado`() {
        val envelope = parseScreen(
            """
            {"sdui_version":1,"screen":{"id":"scheduler.jobs","title":"Scheduler","components":[
              {"type":"table","id":"jobs-table",
               "columns":[{"key":"name","label":"Nome","kind":"text"},
                          {"key":"schedule","label":"Agenda","kind":"text"}],
               "rows_source":{"endpoint":"/api/mobile/v1/scheduler/jobs"},
               "row_actions":[{"action_id":"scheduler.job.run_now","label":"Executar agora"}],
               "empty_state":{"text":"Nenhum job agendado ainda."}}]}}
            """.trimIndent(),
        )
        val rows = SduiJson.parseToJsonElement(
            """{"rows":[{"id":"1","name":"backup-diario","schedule":"0 3 * * *"},
                       {"id":"2","name":"limpeza-logs","schedule":"*/15 * * * *"}]}""",
        )

        render(envelope, ComponentDataFetcher { SduiDataResult.Success(rows) })

        composeRule.onNodeWithText("backup-diario").assertExists()
        composeRule.onNodeWithText("limpeza-logs").assertExists()
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
