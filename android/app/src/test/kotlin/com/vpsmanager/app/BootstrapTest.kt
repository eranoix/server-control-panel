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
 * This app was built with no device and no emulator available -- 412 unit tests green, zero
 * real execution. [Bootstrap] exists so a first boot on a real phone can report its own failure
 * instead of dying mute. These tests pin the two guarantees the operator's phone actually
 * needed: a throwing step never stops the ones after it, and an uncaught exception is persisted
 * readably before the process dies, without swallowing whatever crash handler was already
 * installed.
 *
 * Uses the plain framework [android.app.Application], not [VpsManagerApplication]: Robolectric
 * instantiates and runs `onCreate()` on whatever Application the manifest declares for every
 * test in this module, and [VpsManagerApplication.onCreate] launches a background `appScope`
 * coroutine that touches `WorkManager.getInstance()` (unavailable under Robolectric without
 * [androidx.work.testing.WorkManagerTestInitHelper]) -- left on the real Application, that
 * stray coroutine intermittently writes into the very [Bootstrap.initFailures] list these tests
 * assert on. These tests exercise [Bootstrap] directly and never needed the real app's wiring.
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

        Bootstrap.step("etapa que falha") { throw IllegalStateException("keystore corrompido") }
        Bootstrap.step("etapa seguinte") { laterStepRan = true }

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
        Bootstrap.step("etapa ok") { /* no-op */ }

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
        Thread.setDefaultUncaughtExceptionHandler { _, erro ->
            previousHandlerRan = true
            previousHandlerSawTheSameError = erro
        }

        Bootstrap.installCrashReporter(context)
        Bootstrap.step("etapa que sera reportada mas nao falha") { /* ok, just populates initFailures with nothing */ }
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
        Bootstrap.step("canais de notificacao") { throw RuntimeException("canal recusado pelo fabricante") }
        Thread.getDefaultUncaughtExceptionHandler()!!.uncaughtException(
            Thread.currentThread(),
            RuntimeException("crash fatal"),
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
