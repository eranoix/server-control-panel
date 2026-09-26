package com.vpsmanager.sdui.registry

import com.vpsmanager.core.sdui.SduiComponent
import com.vpsmanager.core.sdui.SduiDataSource
import com.vpsmanager.core.sdui.SduiLogger
import com.vpsmanager.core.sdui.parseScreen
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Proves two things against the real fixture corpus in
 * `contracts/sdui/fixtures` (never a copy pasted into this file):
 *
 * 1. [renderPolicyFor] is exhaustive over every [SduiComponent] subclass with
 *    no `else` branch, so it is a compile error — not a silent runtime gap —
 *    to add a 9th subclass without updating this function.
 * 2. The critical-unknown-becomes-a-placeholder and
 *    non-critical-unknown-is-skipped-and-logged-once policies described in
 *    `core.sdui.parseScreen`'s KDoc are exactly what the renderer applies.
 */
class RegistryDispatchTest {

    @Test
    fun `renderPolicyFor is Render for every known component type`() {
        assertEquals(RenderPolicy.Render, renderPolicyFor(sampleForm()))
        assertEquals(RenderPolicy.Render, renderPolicyFor(sampleTable()))
        assertEquals(RenderPolicy.Render, renderPolicyFor(sampleList()))
        assertEquals(RenderPolicy.Render, renderPolicyFor(sampleDetail()))
        assertEquals(RenderPolicy.Render, renderPolicyFor(sampleAction()))
        assertEquals(RenderPolicy.Render, renderPolicyFor(sampleChart()))
        assertEquals(RenderPolicy.Render, renderPolicyFor(sampleConfirmDestructive()))
    }

    @Test
    fun `renderPolicyFor is Skip for a non-critical Unknown and NeedsUpdate for a critical one`() {
        val nonCritical = SduiComponent.Unknown(id = "g1", type = "gantt", critical = false)
        val critical = SduiComponent.Unknown(id = "g1", type = "gantt", critical = true)

        assertEquals(RenderPolicy.Skip, renderPolicyFor(nonCritical))
        assertEquals(RenderPolicy.NeedsUpdate, renderPolicyFor(critical))
    }

    @Test
    fun `unknown-noncritical fixture resolves to Render, Skip, Render in order`() {
        val envelope = parseScreen(SduiFixtures.read("unknown-noncritical.json"))

        val policies = envelope.screen.components.map(::renderPolicyFor)

        assertEquals(
            listOf(RenderPolicy.Render, RenderPolicy.Skip, RenderPolicy.Render),
            policies,
        )
    }

    @Test
    fun `unknown-critical fixture resolves to Render, NeedsUpdate in order`() {
        val envelope = parseScreen(SduiFixtures.read("unknown-critical.json"))

        val policies = envelope.screen.components.map(::renderPolicyFor)

        assertEquals(listOf(RenderPolicy.Render, RenderPolicy.NeedsUpdate), policies)
    }

    @Test
    fun `all-components fixture resolves to Render for all seven components`() {
        val envelope = parseScreen(SduiFixtures.read("all-components.json"))

        val policies = envelope.screen.components.map(::renderPolicyFor)

        assertEquals(7, envelope.screen.components.size)
        assertEquals(List(7) { RenderPolicy.Render }, policies)
    }

    @Test
    fun `parseScreen logs a skipped non-critical unknown exactly once`() {
        val logger = CountingLogger()

        parseScreen(SduiFixtures.read("unknown-noncritical.json"), logger)

        assertEquals(1, logger.skipCount)
        assertEquals(listOf("gantt" to "deploy-timeline"), logger.skipped)
    }

    private class CountingLogger : SduiLogger {
        var skipCount = 0
        val skipped = mutableListOf<Pair<String, String>>()

        override fun skippedUnknown(type: String, id: String) {
            skipCount += 1
            skipped += type to id
        }
    }

    private fun sampleForm() = SduiComponent.Form(
        id = "f1",
        fields = emptyList(),
        submitAction = com.vpsmanager.core.sdui.SduiActionRef(actionId = "submit"),
    )

    private fun sampleTable() = SduiComponent.Table(
        id = "t1",
        columns = emptyList(),
        rowsSource = SduiDataSource(endpoint = "/api/mobile/v1/x"),
    )

    private fun sampleList() = SduiComponent.ListComponent(
        id = "l1",
        itemTemplate = "default",
        rowsSource = SduiDataSource(endpoint = "/api/mobile/v1/x"),
    )

    private fun sampleDetail() = SduiComponent.Detail(
        id = "d1",
        dataSource = SduiDataSource(endpoint = "/api/mobile/v1/x"),
        fields = emptyList(),
    )

    private fun sampleAction() = SduiComponent.Action(
        id = "a1",
        label = "Go",
        actionId = "go",
    )

    private fun sampleChart() = SduiComponent.Chart(
        id = "c1",
        chartKind = "line",
        seriesSource = SduiDataSource(endpoint = "/api/mobile/v1/x"),
        xKey = "ts",
        yKey = "value",
    )

    private fun sampleConfirmDestructive() = SduiComponent.ConfirmDestructive(
        id = "cd1",
        actionId = "kill",
        message = "Tem certeza?",
    )
}
