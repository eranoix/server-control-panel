package com.vpsmanager.app

import androidx.test.core.app.ApplicationProvider
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * [Bootstrap] guarantees: a throwing step never stops the ones after it, and an uncaught
 * exception is persisted readably before the process dies while still reaching the previously
 * installed handler.
 *
 * Uses the plain [android.app.Application]: [VpsManagerApplication.onCreate] starts a
 * background coroutine touching WorkManager, which fails under Robolectric and would write into
 * the same [Bootstrap.initFailures] these tests assert on.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class)
class BootstrapTest {

    @Before
    fun setUp() {
        Bootstrap.initFailures.clear()
    }

    @After
    fun tearDown() {
        Bootstrap.initFailures.clear()
    }

    @Test
    fun `a throwing step does not prevent the steps after it from running`() {
        var laterStepRan = false

        Bootstrap.step("failing step") { throw IllegalStateException("corrupted keystore") }
        Bootstrap.step("next step") { laterStepRan = true }

        assertTrue("a later step must still run after an earlier one throws", laterStepRan)
    }

    @Test
    fun `a throwing step records its name and the exception in initFailures`() {
        Bootstrap.step("registro de conta telefonica (Telecom)") {
            throw SecurityException("Neither user 10472 nor current process has READ_PHONE_NUMBERS")
        }

        assertEquals(1, Bootstrap.initFailures.size)
        assertTrue(Bootstrap.initFailures[0].contains("registro de conta telefonica (Telecom)"))
        assertTrue(Bootstrap.initFailures[0].contains("SecurityException"))
    }

    @Test
    fun `a step that does not throw never touches initFailures`() {
        Bootstrap.step("ok step") { /* no-op */ }

        assertTrue(Bootstrap.initFailures.isEmpty())
    }

    @Test
    fun `multiple failing steps each get their own entry, in order`() {
        Bootstrap.step("um") { throw RuntimeException("falha um") }
        Bootstrap.step("dois") { /* ok */ }
        Bootstrap.step("tres") { throw RuntimeException("falha tres") }

        assertEquals(2, Bootstrap.initFailures.size)
        assertTrue(Bootstrap.initFailures[0].startsWith("um:"))
        assertTrue(Bootstrap.initFailures[1].startsWith("tres:"))
    }

    @Test
    fun `installCrashReporter persists a readable report and still runs the previous handler`() {
        val context = ApplicationProvider.getApplicationContext<android.app.Application>()
        Bootstrap.clearLastCrash(context)
        var previousHandlerRan = false
        var previousHandlerSawTheSameError: Throwable? = null
        Thread.setDefaultUncaughtExceptionHandler { _, error ->
            previousHandlerRan = true
            previousHandlerSawTheSameError = error
        }

        Bootstrap.installCrashReporter(context)
        Bootstrap.step("step that is reported but does not fail") { /* ok, just populates initFailures with nothing */ }
        val crashError = IllegalStateException("EncryptedSharedPreferences.create falhou apos rotacao de chave")
        Thread.getDefaultUncaughtExceptionHandler()!!.uncaughtException(Thread.currentThread(), crashError)

        val persisted = Bootstrap.lastCrash(context)
        assertTrue(persisted != null && persisted.contains("EncryptedSharedPreferences.create falhou"))
        assertTrue(
            "the crash report must be readable text, including the thread name",
            persisted!!.contains("thread: ${Thread.currentThread().name}"),
        )
        assertTrue("installCrashReporter must chain to whatever handler was already installed", previousHandlerRan)
        assertEquals(crashError, previousHandlerSawTheSameError)
    }

    @Test
    fun `installCrashReporter's own report includes init failures that happened before the crash`() {
        val context = ApplicationProvider.getApplicationContext<android.app.Application>()
        Bootstrap.clearLastCrash(context)
        Thread.setDefaultUncaughtExceptionHandler(null)

        Bootstrap.installCrashReporter(context)
        Bootstrap.step("canais de notificacao") { throw RuntimeException("channel refused by the manufacturer") }
        Thread.getDefaultUncaughtExceptionHandler()!!.uncaughtException(
            Thread.currentThread(),
            RuntimeException("fatal crash"),
        )

        val persisted = Bootstrap.lastCrash(context)
        assertTrue(persisted != null && persisted.contains("canais de notificacao"))
    }

    @Test
    fun `clearLastCrash removes the persisted report`() {
        val context = ApplicationProvider.getApplicationContext<android.app.Application>()
        Bootstrap.installCrashReporter(context)
        Thread.getDefaultUncaughtExceptionHandler()!!.uncaughtException(Thread.currentThread(), RuntimeException("x"))
        assertTrue(Bootstrap.lastCrash(context) != null)

        Bootstrap.clearLastCrash(context)

        assertNull(Bootstrap.lastCrash(context))
    }
}
