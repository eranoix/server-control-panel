package com.vpsmanager.feature.files.transfer

import android.content.Context
import androidx.work.WorkerParameters
import java.lang.reflect.Modifier
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * WorkManager's default factory finds the `(Context, WorkerParameters)` constructor by
 * reflection; without it the job fails before `doWork`. Kotlin default parameters do not
 * generate that constructor, and direct Kotlin construction in other tests would not notice.
 */
class TransferWorkersConstructorTest {

    @Test
    fun `UploadWorker exposes the constructor the WorkManager factory looks for`() {
        val constructor = UploadWorker::class.java.getDeclaredConstructor(
            Context::class.java,
            WorkerParameters::class.java,
        )
        assertTrue("the constructor must be public", Modifier.isPublic(constructor.modifiers))
    }

    @Test
    fun `DownloadWorker exposes the constructor the WorkManager factory looks for`() {
        val constructor = DownloadWorker::class.java.getDeclaredConstructor(
            Context::class.java,
            WorkerParameters::class.java,
        )
        assertTrue("the constructor must be public", Modifier.isPublic(constructor.modifiers))
    }
}
