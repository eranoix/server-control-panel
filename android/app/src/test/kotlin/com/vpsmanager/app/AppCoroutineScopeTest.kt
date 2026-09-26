package com.vpsmanager.app

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

/**
 * A sibling audit flagged that [VpsManagerApplication.sweepAbandonedTransfers] runs on
 * `Dispatchers.IO` inside `appScope`, and `appScope` had no [kotlinx.coroutines.CoroutineExceptionHandler].
 * Confirmed real: [Bootstrap.step]'s try/catch only wraps the synchronous `launch()` call itself
 * (which never throws), not the coroutine body -- so a throw inside that body used to escape
 * straight to the thread's uncaught-exception handler, exactly like an unguarded crash, unlike
 * every OTHER `Bootstrap.step` failure which degrades silently. These tests pin [appCoroutineScope]
 * as the fix: it gives this scope the same isolation contract as [Bootstrap.step].
 *
 * Uses the plain framework [android.app.Application], not [VpsManagerApplication], on purpose:
 * these tests pin [appCoroutineScope] and [Bootstrap.initFailures] in isolation, not the real
 * app's `onCreate()` wiring -- Robolectric instantiates and calls `onCreate()` on whatever
 * Application the manifest declares for every single test in this module regardless of whether
 * the test asks for it, and [VpsManagerApplication.onCreate] itself launches a background
 * `appScope` coroutine (`sweepAbandonedTransfers`) that touches `WorkManager.getInstance()` --
 * unavailable under Robolectric without [androidx.work.testing.WorkManagerTestInitHelper]. Left
 * on the real Application, that stray coroutine intermittently writes into the very
 * [Bootstrap.initFailures] list these tests assert on, racing the test body. Swapping to the
 * bare Application removes the real app's `onCreate()` (and that race) without touching any
 * production wiring.
 */
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
                throw IllegalStateException("falha simulada na faxina de transferencias")
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
            throw IllegalStateException("EncryptedSharedPreferences indisponivel")
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

        val failing = scope.launch(Dispatchers.IO) { throw RuntimeException("falha um") }
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
        val job = scope.launch(Dispatchers.IO) { /* no-op */ }
        runBlocking { job.join() }

        assertTrue(Bootstrap.initFailures.isEmpty())
    }

    /**
     * Shape of the regression: a `CoroutineScope` with a `SupervisorJob` and
     * NO `CoroutineExceptionHandler` — the exact construction `appScope` used
     * before this fix — has nowhere to send the exception, and it escapes to
     * the thread's default handler.
     *
     * ## Why this test NO longer produces a real leak
     *
     * It used to: it launched a coroutine that really did blow up. Except
     * that `kotlinx-coroutines-test`, merely by being on the classpath,
     * CAPTURES uncaught exceptions globally and reports them on the NEXT
     * `runTest` as `UncaughtExceptionsBeforeTest`. The result was a suite that
     * failed on a different test every run — never on this one, always on its
     * neighbour, with the message "simulated failure" turning up in a test
     * that has nothing to do with it.
     *
     * Restoring `Thread.setDefaultUncaughtExceptionHandler` in the `finally`,
     * as used to be done, does not solve it: coroutines-test takes another
     * path.
     *
     * The property that matters is still proved, and with no side effect: the
     * raw scope does NOT have a `CoroutineExceptionHandler` in its context,
     * and that — and only that — is what the leak follows from. That a
     * coroutine with no handler escapes to the default handler is the
     * library's own guarantee, not this project's; what this project has to
     * guarantee is that its scope HAS a handler, and that is what the
     * neighbouring test proves.
     */
    @Test
    fun `a construcao pre-correcao nao tem handler nenhum — e por isso vazava`() {
        val escopoCru = kotlinx.coroutines.CoroutineScope(SupervisorJob() + Dispatchers.Default)
        assertNull(
            "um escopo sem CoroutineExceptionHandler nao tem para onde mandar a " +
                "excecao: ela escapa para o handler padrao da thread",
            escopoCru.coroutineContext[kotlinx.coroutines.CoroutineExceptionHandler],
        )
        escopoCru.cancel()
    }

}
