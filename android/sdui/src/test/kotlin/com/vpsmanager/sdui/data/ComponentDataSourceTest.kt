package com.vpsmanager.sdui.data

import com.vpsmanager.data.sdui.SduiDataResult
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * [toComponentDataState] is the pure mapping the four read components rely
 * on to decide Loading/Error/Empty/Data — exercised directly here, with no
 * Compose test infrastructure needed.
 */
class ComponentDataSourceTest {

    @Test
    fun `Error result maps to Error with the same reason`() {
        val state = toComponentDataState(SduiDataResult.Error("falhou"))

        assertEquals(ComponentDataState.Error("falhou"), state)
    }

    @Test
    fun `Empty result maps to Empty`() {
        assertEquals(ComponentDataState.Empty, toComponentDataState(SduiDataResult.Empty))
    }

    @Test
    fun `a JSON array body maps to Data with each element as a row`() {
        val body = Json.parseToJsonElement("""[{"name":"web"},{"name":"db"}]""")

        val state = toComponentDataState(SduiDataResult.Success(body))

        assertTrue(state is ComponentDataState.Data)
        assertEquals(2, (state as ComponentDataState.Data).rows.size)
        assertEquals("web", state.rows[0].jsonPrimitiveValue("name"))
    }

    @Test
    fun `an empty JSON array body maps to Empty, not an empty Data list`() {
        val body = Json.parseToJsonElement("[]")

        assertEquals(ComponentDataState.Empty, toComponentDataState(SduiDataResult.Success(body)))
    }

    @Test
    fun `a single JSON object body maps to Data with one row`() {
        val body = Json.parseToJsonElement("""{"id":"c1","name":"web"}""")

        val state = toComponentDataState(SduiDataResult.Success(body))

        assertTrue(state is ComponentDataState.Data)
        assertEquals(1, (state as ComponentDataState.Data).rows.size)
    }

    @Test
    fun `a JSON array containing a non-object element maps to Error`() {
        val body = Json.parseToJsonElement("""[{"name":"web"}, 42]""")

        assertTrue(toComponentDataState(SduiDataResult.Success(body)) is ComponentDataState.Error)
    }

    @Test
    fun `a bare JSON primitive body maps to Error`() {
        val body = Json.parseToJsonElement("42")

        assertTrue(toComponentDataState(SduiDataResult.Success(body)) is ComponentDataState.Error)
    }

    @Test
    fun `a rows-wrapped body -- the shape TestSchedulerRows_WireShape pins server-side -- maps to Data`() {
        val body = Json.parseToJsonElement("""{"rows":[{"id":"job-1"},{"id":"job-2"}]}""")

        val state = toComponentDataState(SduiDataResult.Success(body))

        assertTrue(state is ComponentDataState.Data)
        assertEquals(2, (state as ComponentDataState.Data).rows.size)
        assertEquals("job-1", state.rows[0].jsonPrimitiveValue("id"))
    }

    @Test
    fun `an empty rows-wrapped body maps to Empty, not an empty Data list`() {
        val body = Json.parseToJsonElement("""{"rows":[]}""")

        assertEquals(ComponentDataState.Empty, toComponentDataState(SduiDataResult.Success(body)))
    }

    @Test
    fun `a rows-wrapped body with a non-object element maps to Error`() {
        val body = Json.parseToJsonElement("""{"rows":[{"id":"job-1"}, 42]}""")

        assertTrue(toComponentDataState(SduiDataResult.Success(body)) is ComponentDataState.Error)
    }

    private fun JsonObject.jsonPrimitiveValue(key: String): String? =
        this[key]?.let { it as? kotlinx.serialization.json.JsonPrimitive }?.content
}
