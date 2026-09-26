package com.vpsmanager.core.sdui

import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.MissingFieldException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * The forward-compat proof: run against the shared golden fixtures on plain JVM, no
 * Android SDK, no emulator, no server.
 */
class ForwardCompatTest {

    private class RecordingLogger : SduiLogger {
        val skipped = mutableListOf<Pair<String, String>>()
        override fun skippedUnknown(type: String, id: String) {
            skipped += type to id
        }
    }

    @Test
    fun `unknown non-critical type is skipped without breaking the rest of the screen`() {
        val envelope = parseScreen(SduiFixtures.read("unknown-noncritical.json"))
        val components = envelope.screen.components
        assertEquals(3, components.size)

        val table = components[0] as SduiComponent.Table
        assertEquals("containers-table", table.id)
        assertEquals("/api/mobile/v1/docker/containers", table.rowsSource.endpoint)

        val unknown = components[1] as SduiComponent.Unknown
        assertEquals("gantt", unknown.type)
        assertEquals("deploy-timeline", unknown.id)
        assertFalse(unknown.critical)

        val action = components[2] as SduiComponent.Action
        assertEquals("deploy-trigger", action.id)
        assertEquals("deploy.trigger", action.actionId)
    }

    @Test
    fun `unknown critical type keeps the critical flag through parsing`() {
        val envelope = parseScreen(SduiFixtures.read("unknown-critical.json"))
        val components = envelope.screen.components
        assertEquals(2, components.size)

        val action = components[0] as SduiComponent.Action
        assertEquals("deploy-trigger", action.id)

        val unknown = components[1] as SduiComponent.Unknown
        assertEquals("gantt", unknown.type)
        assertEquals("g1", unknown.id)
        assertTrue(unknown.critical)
    }

    @Test
    fun `unknown fields, top-level and nested, are silently ignored`() {
        val envelope = parseScreen(SduiFixtures.read("unknown-extra-fields.json"))
        val components = envelope.screen.components
        assertEquals(1, components.size)

        val table = components[0] as SduiComponent.Table
        assertEquals("containers-table", table.id)
        assertEquals(1, table.columns.size)
        assertEquals("name", table.columns[0].key)
        assertEquals("text", table.columns[0].kind)
        assertEquals("/api/mobile/v1/docker/containers", table.rowsSource.endpoint)
    }

    @OptIn(ExperimentalSerializationApi::class)
    @Test
    fun `a missing required field inside a known component is still a parse error`() {
        val payloadMissingRowsSource = """
            {
              "sdui_version": 1,
              "screen": {
                "id": "fixture.broken-table",
                "title": "Tabela sem rows_source",
                "components": [
                  {
                    "type": "table",
                    "id": "containers-table",
                    "columns": [
                      { "key": "name", "label": "Nome", "kind": "text" }
                    ]
                  }
                ]
              }
            }
        """.trimIndent()

        try {
            parseScreen(payloadMissingRowsSource)
            fail("expected a MissingFieldException — a missing required field must not decode into a half-built component")
        } catch (expected: MissingFieldException) {
            assertTrue(expected.message.orEmpty().contains("rows_source"))
        }
    }

    @Test
    fun `every skipped non-critical unknown is logged exactly once`() {
        val logger = RecordingLogger()
        parseScreen(SduiFixtures.read("unknown-noncritical.json"), logger)

        assertEquals(1, logger.skipped.size)
        assertEquals("gantt" to "deploy-timeline", logger.skipped.single())
    }

    @Test
    fun `a critical unknown is not logged as a skip`() {
        val logger = RecordingLogger()
        parseScreen(SduiFixtures.read("unknown-critical.json"), logger)

        assertTrue(logger.skipped.isEmpty())
    }

    @Test
    fun `every fixture with an sdui_version parses without throwing`() {
        val fixtureFiles = SduiFixtures.directory.listFiles { file -> file.extension == "json" }
            ?: fail("could not list fixtures directory: ${SduiFixtures.directory}").let { emptyArray() }

        val screenFixtures = fixtureFiles.filter { it.readText().contains("\"sdui_version\"") }
        assertTrue("expected at least one screen fixture", screenFixtures.isNotEmpty())

        screenFixtures.forEach { file ->
            try {
                parseScreen(file.readText())
            } catch (e: Exception) {
                fail("fixture ${file.name} failed to parse: ${e.message}")
            }
        }
    }
}
