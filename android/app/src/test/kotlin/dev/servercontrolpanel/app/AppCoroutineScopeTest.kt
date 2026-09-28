package dev.servercontrolpanel.app

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancel
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class)
class AppCoroutineScopeTest {

    @Before
    fun setUp() {
        Bootstrap.initFailures.clear()
    }

    @After
    fun tearDown() {
        Bootstrap.initFailures.clear()
    }

    @Test
    fun `a coroutine that throws on appCoroutineScope never reaches the thread's default handler`() {
        var defaultHandlerRan = false
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { _, _ -> defaultHandlerRan = true }
        try {
            val scope = appCoroutineScope()
            val job = scope.launch(Dispatchers.IO) {
                throw IllegalStateException("simulated failure in the transfer sweep")
            }
            runBlocking { job.join() }

            assertFalse(
                "an exception in an appCoroutineScope coroutine must not reach the process's default handler",
                defaultHandlerRan,
            )
        } finally {
            Thread.setDefaultUncaughtExceptionHandler(previous)
        }
    }

    @Test
    fun `a coroutine that throws on appCoroutineScope is recorded in Bootstrap initFailures`() {
        val scope = appCoroutineScope()
        val job = scope.launch(Dispatchers.IO) {
            throw IllegalStateException("EncryptedSharedPreferences unavailable")
        }
        runBlocking { job.join() }

        assertEquals(1, Bootstrap.initFailures.size)
        assertTrue(Bootstrap.initFailures[0].startsWith("appScope:"))
        assertTrue(Bootstrap.initFailures[0].contains("IllegalStateException"))
    }

    @Test
    fun `a failing coroutine does not cancel a sibling coroutine on the same appCoroutineScope`() {
        val scope = appCoroutineScope()
        var siblingRan = false

        val failing = scope.launch(Dispatchers.IO) { throw RuntimeException("failure one") }
        val sibling = scope.launch(Dispatchers.IO) { siblingRan = true }
        runBlocking {
            failing.join()
            sibling.join()
        }

        assertTrue("SupervisorJob must keep sibling coroutines alive after one fails", siblingRan)
    }

    @Test
    fun `a coroutine that does not throw never touches Bootstrap initFailures`() {
        val scope = appCoroutineScope()
        val job = scope.launch(Dispatchers.IO) {  }
        runBlocking { job.join() }

        assertTrue(Bootstrap.initFailures.isEmpty())
    }

    @Test
    fun `a raw supervisor scope has no exception handler, so exceptions escape`() {
        val rawScope = kotlinx.coroutines.CoroutineScope(SupervisorJob() + Dispatchers.Default)
        assertNull(
            "a scope without a CoroutineExceptionHandler has nowhere to send the " +
                "exception: it escapes to the thread's default handler",
            rawScope.coroutineContext[kotlinx.coroutines.CoroutineExceptionHandler],
        )
        rawScope.cancel()
    }

}
