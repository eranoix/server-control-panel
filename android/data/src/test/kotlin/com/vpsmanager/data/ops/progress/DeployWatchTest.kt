package com.vpsmanager.data.ops.progress

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The progress notification uses this to decide when a deploy has ended; wrongly reporting a
 * finished deploy is worse than polling a little longer.
 */
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

    /**
     * Terminal states are an allow list, so a new server state (say `verifying`) keeps being
     * watched instead of being announced as complete.
     */
    @Test
    fun `an unknown server state is not treated as terminal`() {
        assertFalse(DeployWatchWorker.finished("verifying"))
        assertFalse(DeployWatchWorker.finished("draining"))
        assertFalse(DeployWatchWorker.finished(""))
    }

    /** Uppercase from the server must not change the decision. */
    @Test
    fun `the comparison ignores case`() {
        assertTrue(DeployWatchWorker.finished("ROLLED_BACK"))
        assertTrue(DeployWatchWorker.succeeded("OK"))
    }

    /** Finishing and succeeding differ: `rolled_back` is terminal but not a success. */
    @Test
    fun `a bad ending is still terminal but not a success`() {
        assertTrue(DeployWatchWorker.finished("rolled_back"))
        assertFalse(DeployWatchWorker.succeeded("rolled_back"))
        assertFalse(DeployWatchWorker.succeeded("failed"))
    }
}
