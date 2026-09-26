package com.vpsmanager.data.terminal

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What these tests protect: the backups sheet was organised by the structure of
 * the ARCHIVE — a scheduled backup that saved eight sessions turned into eight
 * identical cards showing the same date. The owner called it a mess, and he was
 * right: nobody looks for a backup by archive, they look for *"the `main`
 * session from before I broke everything"*.
 */
class GruposDeBackupTest {

    private fun backup(
        id: String,
        criadoEm: Long,
        vararg sessoes: Pair<String, String>,
        bytes: Long = 1_000L,
        origem: String = "scheduled",
    ) = SessionBackup(
        id = id,
        criadoEm = criadoEm,
        origem = origem,
        bytes = bytes,
        sessoes = sessoes.map { (nome, resumo) -> BackupSession(nome, resumo, 0) },
    )

    /** The case in the photo: one snapshot with eight sessions turned into eight cards. */
    @Test
    fun `um snapshot de varias sessoes vira um grupo por sessao, nao um cartao por sessao`() {
        val grupos = agruparPorSessao(
            listOf(backup("b1", 100, "main" to "", "Vpsm" to "", "proxy" to "")),
        )

        assertEquals(3, grupos.size)
        assertEquals(listOf("main", "proxy", "Vpsm"), grupos.map { it.sessao })
        // Each group has ONE version — the one from the snapshot the session was in.
        assertTrue(grupos.all { it.versoes.size == 1 })
    }

    /** The same session in three snapshots becomes three versions of a single group. */
    @Test
    fun `a mesma sessao em varios snapshots vira versoes de um grupo`() {
        val grupos = agruparPorSessao(
            listOf(
                backup("b1", 300, "main" to ""),
                backup("b2", 100, "main" to ""),
                backup("b3", 200, "main" to ""),
            ),
        )

        assertEquals(1, grupos.size)
        assertEquals(3, grupos.single().versoes.size)
    }

    /** The version you want is almost always the last good one: it goes on top. */
    @Test
    fun `as versoes vem da mais nova para a mais velha`() {
        val grupos = agruparPorSessao(
            listOf(
                backup("b1", 100, "main" to ""),
                backup("b2", 300, "main" to ""),
                backup("b3", 200, "main" to ""),
            ),
        )

        assertEquals(listOf(300L, 200L, 100L), grupos.single().versoes.map { it.criadoEm })
    }

    /**
     * Alphabetical order, CASE-INSENSITIVE. String `compareTo` orders by code
     * point, so "Vpsm" would come before "main" — in a list of names chosen by
     * people, that reads as a bug.
     */
    @Test
    fun `os grupos sao alfabeticos ignorando maiuscula`() {
        val grupos = agruparPorSessao(
            listOf(backup("b1", 100, "Vpsm" to "", "main" to "", "Aplicativo" to "")),
        )

        assertEquals(listOf("Aplicativo", "main", "Vpsm"), grupos.map { it.sessao })
    }

    /**
     * The group summary is the clue to WHICH session that is when the name does
     * not say (`tt`, `proxy`). Inheriting the empty one from the most recent
     * version would hide the only textual clue there is.
     */
    @Test
    fun `o resumo do grupo vem da versao mais recente QUE TENHA um`() {
        val grupos = agruparPorSessao(
            listOf(
                backup("b1", 300, "main" to ""),
                backup("b2", 200, "main" to "rodando o deploy"),
                backup("b3", 100, "main" to "outra coisa mais velha"),
            ),
        )

        assertEquals("rodando o deploy", grupos.single().resumo)
    }

    /**
     * `sessoesNoBackup` decides two sentences on the screen: whether the size
     * reads "from a backup with N sessions", and whether the "restore all"
     * button shows up. Without it, "89 kB" next to ONE session would suggest
     * that session takes up 89 kB.
     */
    @Test
    fun `cada versao sabe quantas sessoes havia no snapshot dela`() {
        val grupos = agruparPorSessao(
            listOf(backup("b1", 100, "main" to "", "Vpsm" to "", bytes = 89_000L)),
        )

        assertTrue(grupos.all { it.versoes.single().sessoesNoBackup == 2 })
        assertEquals(89_000L, grupos.first().versoes.single().bytes)
    }

    /** An empty name does not become a ghost group. */
    @Test
    fun `sessao sem nome e descartada`() {
        val grupos = agruparPorSessao(listOf(backup("b1", 100, "" to "", "main" to "")))

        assertEquals(listOf("main"), grupos.map { it.sessao })
    }

    @Test
    fun `lista vazia nao produz grupo`() {
        assertTrue(agruparPorSessao(emptyList()).isEmpty())
    }
}
