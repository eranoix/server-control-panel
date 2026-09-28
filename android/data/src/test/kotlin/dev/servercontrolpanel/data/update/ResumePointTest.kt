package dev.servercontrolpanel.data.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment

@RunWith(RobolectricTestRunner::class)
class ResumePointTest {

    private val app get() = RuntimeEnvironment.getApplication()

    @Test
    fun `the route comes back once and then disappears`() {
        val store = ResumePoint(app)
        store.save("deploys")

        assertEquals("deploys", store.consume())
        assertNull(store.consume())
    }

    @Test
    fun `a blank route clears instead of saving blank`() {
        val store = ResumePoint(app)
        store.save("terminal")
        store.save("   ")
        assertNull(store.consume())
    }

    @Test
    fun `a stale route does not hijack a later launch`() {
        var clock = 1_000_000L
        val store = ResumePoint(app, now = { clock })
        store.save("whatsapp")

        clock += 11 * 60 * 1000L
        assertNull("after the deadline the route is no longer valid", store.consume())
    }

    @Test
    fun `within the deadline the route is still valid`() {
        var clock = 1_000_000L
        val store = ResumePoint(app, now = { clock })
        store.save("files")

        clock += 30_000L
        assertEquals("files", store.consume())
    }

    @Test
    fun `forget clears without consuming`() {
        val store = ResumePoint(app)
        store.save("notifications")
        store.forget()
        assertNull(store.consume())
    }
}
