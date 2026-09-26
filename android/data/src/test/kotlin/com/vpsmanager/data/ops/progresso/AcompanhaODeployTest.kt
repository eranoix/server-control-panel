package com.vpsmanager.data.ops.progresso

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What these tests protect: the progress notification decides, through this
 * function, **when to stop asking and announce the end**. Erring on the side
 * of "it finished" means announcing a completed deploy that is still halfway —
 * the worst false news this app can deliver.
 */
class AcompanhaODeployTest {

    @Test
    fun `os estados finais conhecidos terminam`() {
        listOf("ok", "success", "succeeded", "done", "failed", "error", "rolled_back", "cancelled")
            .forEach { assertTrue(it, AcompanhaODeployWorker.terminou(it)) }
    }

    @Test
    fun `estado em andamento nao termina`() {
        listOf("running", "queued", "pending", "started").forEach {
            assertFalse(it, AcompanhaODeployWorker.terminou(it))
        }
    }

    /**
     * WHY IT IS AN ALLOW LIST.
     *
     * If the function listed the IN PROGRESS states and treated the rest as the
     * end, a new server state — `verifying`, say — would read as terminal, and
     * the notification would announce "Deploy complete" in the middle of the
     * verification. With an allow list, the possible error is the harmless one:
     * following along a little longer than it needed to.
     */
    @Test
    fun `estado desconhecido do servidor NAO e tratado como fim`() {
        assertFalse(AcompanhaODeployWorker.terminou("verifying"))
        assertFalse(AcompanhaODeployWorker.terminou("draining"))
        assertFalse(AcompanhaODeployWorker.terminou(""))
    }

    /** Uppercase from the server must not change the decision. */
    @Test
    fun `a comparacao ignora caixa`() {
        assertTrue(AcompanhaODeployWorker.terminou("ROLLED_BACK"))
        assertTrue(AcompanhaODeployWorker.deuCerto("OK"))
    }

    /**
     * Finishing and finishing WELL are different questions: `rolled_back` is an
     * end, and it is a bad end. Confusing the two would make the notification
     * say "Deploy complete" about a rollback.
     */
    @Test
    fun `terminar mal continua sendo terminar, mas nao e sucesso`() {
        assertTrue(AcompanhaODeployWorker.terminou("rolled_back"))
        assertFalse(AcompanhaODeployWorker.deuCerto("rolled_back"))
        assertFalse(AcompanhaODeployWorker.deuCerto("failed"))
    }
}
