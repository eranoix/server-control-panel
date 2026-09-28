package dev.servercontrolpanel.data.ops.progress

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class DeployWatchTest {

    @Test
    fun `known final states are terminal`() {
        listOf("ok", "success", "succeeded", "done", "failed", "error", "rolled_back", "cancelled")
            .forEach { assertTrue(it, DeployWatchWorker.finished(it)) }
    }

    @Test
    fun `in-progress states are not terminal`() {
        listOf("running", "queued", "pending", "started").forEach {
            assertFalse(it, DeployWatchWorker.finished(it))
        }
    }

    @Test
    fun `an unknown server state is not treated as terminal`() {
        assertFalse(DeployWatchWorker.finished("verifying"))
        assertFalse(DeployWatchWorker.finished("draining"))
        assertFalse(DeployWatchWorker.finished(""))
    }

    @Test
    fun `the comparison ignores case`() {
        assertTrue(DeployWatchWorker.finished("ROLLED_BACK"))
        assertTrue(DeployWatchWorker.succeeded("OK"))
    }

    @Test
    fun `a bad ending is still terminal but not a success`() {
        assertTrue(DeployWatchWorker.finished("rolled_back"))
        assertFalse(DeployWatchWorker.succeeded("rolled_back"))
        assertFalse(DeployWatchWorker.succeeded("failed"))
    }
}
