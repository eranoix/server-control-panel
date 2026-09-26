package com.vpsmanager.feature.terminal.attach

import android.content.Context
import androidx.work.WorkerParameters
import java.lang.reflect.Modifier
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * WorkManager's default factory creates workers by reflection and needs a public
 * `(Context, WorkerParameters)` constructor. A Kotlin constructor with default
 * parameters does not produce one, so this looks it up the same way the factory does
 * instead of constructing the worker directly.
 */
class AttachmentUploadWorkerConstructorTest {

    @Test
    fun `the worker exposes the constructor the WorkManager factory looks for`() {
        val constructor = AttachmentUploadWorker::class.java.getDeclaredConstructor(
            Context::class.java,
            WorkerParameters::class.java,
        )
        assertTrue("the constructor must be public",Modifier.isPublic(constructor.modifiers))
    }
}
