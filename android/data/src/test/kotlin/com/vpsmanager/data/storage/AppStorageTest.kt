package com.vpsmanager.data.storage

import com.vpsmanager.data.storage.AppStorage.Kind
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/**
 * A sweep that deletes what it should not is worse than no sweep at all: the
 * person loses what the network will not bring back, and finds out afterwards.
 * These tests pin exactly the boundaries the policy declares.
 */
class AppStorageTest {

    @get:Rule
    val tempDir = TemporaryFolder()

    private val now = 1_700_000_000_000L
    private val oneDay = 24L * 60 * 60 * 1000

    private fun file(dir: File, name: String, bytes: Int, ageInDays: Long): File {
        dir.mkdirs()
        val f = File(dir, name)
        f.writeBytes(ByteArray(bytes))
        f.setLastModified(now - ageInDays * oneDay)
        return f
    }

    @Test
    fun `deposito auto-limitado nunca e tocado pela rotina`() {
        // The HTTP cache and media have ceilings of their own. Deleting them
        // here returns no space that was not already bounded, and costs network
        // on the next opening — on bad internet that is the opposite of upkeep.
        val cache = tempDir.newFolder("bff-http")
        val old = file(cache, "resposta", 1_000, ageInDays = 90)

        val r = AppStorage.maintenance(
            listOf(AppStorage.StorageArea("Cache", "", cache, Kind.AUTO_BOUNDED)),
            nowMs = now,
        )

        assertTrue("o cache se poda sozinho; a rotina nao mexe", old.exists())
        assertEquals(0L, r.freedBytes)
    }

    @Test
    fun `temporario velho sai, temporario recente fica`() {
        val tmp = tempDir.newFolder("anexos")
        val stale = file(tmp, "envio-abandonado", 4_000, ageInDays = 10)
        val recent = file(tmp, "subindo-agora", 2_000, ageInDays = 1)

        val r = AppStorage.maintenance(
            listOf(AppStorage.StorageArea("Temporários", "", tmp, Kind.TEMPORARY)),
            nowMs = now,
        )

        assertFalse("um envio parado ha dez dias nao vai concluir", stale.exists())
        assertTrue("um envio de ontem pode estar so esperando rede", recent.exists())
        assertEquals(4_000L, r.freedBytes)
        assertEquals(1, r.filesRemoved)
    }

    @Test
    fun `atualizacao EM USO nunca e apagada, mesmo sendo a mais nova`() {
        // Deleting a download in flight throws away what the person already
        // paid for in network — on bad internet, the cost that hurts most.
        val staging = tempDir.newFolder("atualizacoes")
        val downloading = file(staging, "nova.hdiff", 9_000, ageInDays = 0)
        val fromPreviousVersion = file(staging, "antiga.apk", 50_000, ageInDays = 0)

        val r = AppStorage.maintenance(
            listOf(AppStorage.StorageArea("Atualizações", "", staging, Kind.IN_TRANSIT)),
            nowMs = now,
            inUse = setOf(downloading),
        )

        assertTrue("o download em andamento tem de sobreviver", downloading.exists())
        assertFalse("o artefato da versao ja instalada e lixo puro", fromPreviousVersion.exists())
        assertEquals(50_000L, r.freedBytes)
    }

    @Test
    fun `em transito nao espera a idade - o criterio e nao estar em uso`() {
        // A rebuilt APK is tens of MB. Holding on for three days to something
        // already installed would be upkeep that keeps nothing.
        val staging = tempDir.newFolder("atualizacoes")
        val installedToday = file(staging, "ja-instalado.apk", 60_000, ageInDays = 0)

        val r = AppStorage.maintenance(
            listOf(AppStorage.StorageArea("Atualizações", "", staging, Kind.IN_TRANSIT)),
            nowMs = now,
        )

        assertFalse(installedToday.exists())
        assertEquals(60_000L, r.freedBytes)
    }

    @Test
    fun `arquivo sem data valida nao e confundido com antigo`() {
        // `lastModified` returns 0 when the filesystem does not know. Treating
        // 0 as "the year 1970, therefore old" would delete precisely what is
        // unknown — the wrong decision when the information is missing.
        val tmp = tempDir.newFolder("anexos")
        val noDate = file(tmp, "sem-data", 1_500, ageInDays = 0)
        noDate.setLastModified(0)

        AppStorage.maintenance(
            listOf(AppStorage.StorageArea("Temporários", "", tmp, Kind.TEMPORARY)),
            nowMs = now,
        )

        assertTrue("sem data conhecida, nao se apaga", noDate.exists())
    }

    @Test
    fun `medir soma recursivo e sobrevive a diretorio ausente`() {
        val dir = tempDir.newFolder("midia")
        file(File(dir, "sub"), "a", 700, ageInDays = 0)
        file(dir, "b", 300, ageInDays = 0)

        val usages = AppStorage.measure(
            listOf(
                AppStorage.StorageArea("Mídia", "", dir, Kind.AUTO_BOUNDED),
                AppStorage.StorageArea("Nunca criado", "", File(dir, "inexistente"), Kind.TEMPORARY),
            ),
        )

        assertEquals(1_000L, usages[0].bytes)
        assertEquals("diretorio que nunca existiu e zero, nao erro", 0L, usages[1].bytes)
    }

    @Test
    fun `formatar usa a mesma base que as telas do Android`() {
        assertEquals("512 B", AppStorage.formatBytes(512))
        assertEquals("1,5 kB", AppStorage.formatBytes(1_500).replace('.', ','))
        assertEquals("24,0 MB", AppStorage.formatBytes(24_000_000).replace('.', ','))
    }
}
