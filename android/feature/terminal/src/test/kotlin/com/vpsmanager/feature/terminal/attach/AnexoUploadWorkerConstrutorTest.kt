package com.vpsmanager.feature.terminal.attach

import android.content.Context
import androidx.work.WorkerParameters
import java.lang.reflect.Modifier
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The test that was missing when the attachment failed on the emulator with
 * "Não foi possível enviar o anexo" without ever having tried to send
 * anything.
 *
 * The WorkManager default factory instantiates a worker by REFLECTION, looking
 * for exactly the public `(Context, WorkerParameters)` constructor. A Kotlin
 * constructor with default-valued parameters (used here to inject the
 * repository and the storage under test) does not produce that constructor: it
 * produces the full one plus a synthetic one with a bitmask. The result is a
 * `NoSuchMethodException` inside the factory, "Could not create Worker" in the
 * log and the work marked FAILED before the first line of `doWork` — no unit
 * test that builds the worker DIRECTLY (in Kotlin, with named arguments)
 * notices this, because Kotlin resolves the defaults at compile time and never
 * goes through reflection.
 *
 * That is why this assertion goes through the same path WorkManager uses —
 * `getDeclaredConstructor` — and not by constructing the object.
 */
class AnexoUploadWorkerConstrutorTest {

    @Test
    fun `o worker expoe o construtor que a fabrica do WorkManager procura`() {
        val construtor = AnexoUploadWorker::class.java.getDeclaredConstructor(
            Context::class.java,
            WorkerParameters::class.java,
        )
        assertTrue("o construtor precisa ser público", Modifier.isPublic(construtor.modifiers))
    }
}
