package dev.servercontrolpanel.feature.terminal.attach

import android.content.Context
import androidx.work.WorkerParameters
import java.lang.reflect.Modifier
import org.junit.Assert.assertTrue
import org.junit.Test

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
