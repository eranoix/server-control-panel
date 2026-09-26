package dev.servercontrolpanel.feature.jira

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.unit.IntSize
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Test

class CardDragTest {

    private fun stateWithColumns() = DragState().apply {
        registerColumn("To Do", 0f, 100f)
        registerColumn("In Progress", 100f, 200f)
    }

    @Test
    fun `release reports the column under the card before clearing the drag`() {
        val state = stateWithColumns()
        state.pick(card("KAN-1"), "To Do", Offset(10f, 0f), IntSize(80, 40))
        state.drag(Offset(100f, 0f))

        val released = state.release()

        assertEquals(card("KAN-1") to "In Progress", released)
        assertFalse(state.dragging)
    }

    @Test
    fun `release outside every column reports no column`() {
        val state = stateWithColumns()
        state.pick(card("KAN-1"), "To Do", Offset(10f, 0f), IntSize(80, 40))
        state.drag(Offset(500f, 0f))

        assertEquals(card("KAN-1") to null, state.release())
    }

    @Test
    fun `release without a drag returns nothing`() {
        assertNull(stateWithColumns().release())
    }
}
