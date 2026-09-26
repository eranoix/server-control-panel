package com.vpsmanager.core.sdui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class SduiParsingTest {

    @Test
    fun `all-components fixture parses into exactly 7 components, one per known sealed subclass`() {
        val envelope = parseScreen(SduiFixtures.read("all-components.json"))

        val components = envelope.screen.components
        assertEquals(7, components.size)

        val table = components[0] as SduiComponent.Table
        assertEquals("containers-table", table.id)
        assertEquals(2, table.columns.size)
        assertEquals("name", table.columns[0].key)
        assertEquals("text", table.columns[0].kind)
        assertEquals("status", table.columns[1].key)
        assertEquals(mapOf("running" to "success", "exited" to "neutral"), table.columns[1].badgeMap)
        assertEquals("/api/mobile/v1/docker/containers", table.rowsSource.endpoint)
        assertEquals("GET", table.rowsSource.method)
        assertEquals(
            listOf("container.restart", "container.kill"),
            table.rowActions?.map { it.actionId },
        )
        assertEquals("Nenhum container em execução", table.emptyState?.text)

        val form = components[1] as SduiComponent.Form
        assertEquals("notify-rule-form", form.id)
        assertEquals(2, form.fields.size)
        assertEquals("notify.rule.create", form.submitAction.actionId)

        val list = components[2] as SduiComponent.ListComponent
        assertEquals("notify-inbox", list.id)
        assertEquals("notification_card", list.itemTemplate)

        val detail = components[3] as SduiComponent.Detail
        assertEquals("container-detail", detail.id)
        assertEquals(2, detail.fields.size)

        val action = components[4] as SduiComponent.Action
        assertEquals("deploy-trigger", action.id)
        assertEquals("deploy.trigger", action.actionId)
        assertEquals("primary", action.style)

        val chart = components[5] as SduiComponent.Chart
        assertEquals("cpu-chart", chart.id)
        assertEquals("line", chart.chartKind)
        assertEquals("ts", chart.xKey)
        assertEquals("value", chart.yKey)

        val confirm = components[6] as SduiComponent.ConfirmDestructive
        assertEquals("kill-container-confirm", confirm.id)
        assertEquals("container.kill", confirm.actionId)
        assertEquals("meu-container", confirm.requireTypedConfirmation)
    }

    @Test
    fun `envelope and screen header fields are read correctly`() {
        val envelope = parseScreen(SduiFixtures.read("all-components.json"))

        assertEquals(1, envelope.sduiVersion)
        assertEquals("fixture.all", envelope.screen.id)
        assertEquals("Todos os componentes", envelope.screen.title)
    }

    @Test
    fun `critical defaults to false and permission_hint to null when absent`() {
        val envelope = parseScreen(SduiFixtures.read("all-components.json"))

        envelope.screen.components.forEach { component ->
            assertEquals("component ${component.id}", false, component.critical)
            assertNull("component ${component.id}", component.permissionHint)
        }
    }

    @Test
    fun `the sealed hierarchy has exactly 8 direct subclasses — 7 known plus Unknown`() {
        val subclasses = SduiComponent::class.sealedSubclasses
        assertEquals(8, subclasses.size)
        assertTrue(subclasses.any { it == SduiComponent.Unknown::class })
    }
}
