package com.vpsmanager.feature.files.transfer

import android.content.Context
import androidx.work.WorkerParameters
import java.lang.reflect.Modifier
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Both transfer workers have to expose the `(Context, WorkerParameters)`
 * constructor that WorkManager's default factory looks for by REFLECTION.
 * Without it the factory throws `NoSuchMethodException`, the log records
 * "Could not create Worker" and the job goes FAILED before the first line of
 * `doWork` — downloads and uploads from the file browser (and the share
 * target) simply did not happen on the device.
 *
 * The defect went unnoticed because Kotlin parameters with default values
 * (used here to inject dependencies in tests) do NOT generate that
 * constructor, and every existing test built the workers straight from
 * Kotlin, where defaults are resolved at compile time and reflection never
 * enters. It was found by running the terminal's attachment feature on a real
 * emulator.
 */
class TransferWorkersConstrutorTest {

    @Test
    fun `UploadWorker expoe o construtor que a fabrica do WorkManager procura`() {
        val construtor = UploadWorker::class.java.getDeclaredConstructor(
            Context::class.java,
            WorkerParameters::class.java,
        )
        assertTrue("o construtor precisa ser público", Modifier.isPublic(construtor.modifiers))
    }

    @Test
    fun `DownloadWorker expoe o construtor que a fabrica do WorkManager procura`() {
        val construtor = DownloadWorker::class.java.getDeclaredConstructor(
            Context::class.java,
            WorkerParameters::class.java,
        )
        assertTrue("o construtor precisa ser público", Modifier.isPublic(construtor.modifiers))
    }
}
