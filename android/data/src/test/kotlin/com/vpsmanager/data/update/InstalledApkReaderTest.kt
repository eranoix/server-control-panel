package com.vpsmanager.data.update

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.vpsmanager.patchengine.ApkPatcher
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * The identification of the installed APK — what becomes `base_sha256` in the
 * query to the server, and what decides whether there is a 1.4 MB patch or only
 * the full 10 MB.
 */
@RunWith(RobolectricTestRunner::class)
class InstalledApkReaderTest {

    @get:Rule
    val temp = TemporaryFolder()

    private val context: Context = ApplicationProvider.getApplicationContext()

    private fun apkFalso(nome: String, conteudo: ByteArray): File =
        File(temp.newFolder(), nome).apply {
            parentFile?.mkdirs()
            writeBytes(conteudo)
        }

    @Test
    fun `hasheia o APK que o sistema aponta agora`() {
        val conteudo = ByteArray(2048) { (it % 31).toByte() }
        val apk = apkFalso("base.apk", conteudo)
        context.applicationInfo.sourceDir = apk.path

        val result = InstalledApkReader(context).read()

        check(result is InstalledApkResult.Ok)
        assertEquals(ApkPatcher.sha256Of(apk), result.sha256)
        assertEquals(apk.path, result.file.path)
    }

    /** 0.2 to 0.5 s per reading of a 31 MB APK. Paid once, not on every opening. */
    @Test
    fun `o hash e cacheado — a segunda leitura nao reprocessa os bytes`() {
        val apk = apkFalso("base.apk", ByteArray(1024) { 7 })
        context.applicationInfo.sourceDir = apk.path
        var vezes = 0
        val reader = InstalledApkReader(context) { file -> vezes++; ApkPatcher.sha256Of(file) }

        val primeira = reader.read()
        val segunda = reader.read()

        assertEquals(1, vezes)
        assertEquals((primeira as InstalledApkResult.Ok).sha256, (segunda as InstalledApkResult.Ok).sha256)
    }

    /**
     * The APK path carries two RANDOM segments that Android changes on every
     * (re)installation. That is why the cache is keyed by it: after an update
     * the path is a different one and the old hash cannot be handed back.
     * Without that property, the app would ask for a patch against the PREVIOUS
     * version, the server would return the wrong patch, and `hpatchz` would
     * produce a corrupted APK with exit code zero.
     */
    @Test
    fun `caminho novo invalida o cache sozinho — nunca devolve o hash do APK anterior`() {
        val velho = apkFalso("base.apk", ByteArray(1024) { 1 })
        val novo = apkFalso("base.apk", ByteArray(1024) { 2 })
        val reader = InstalledApkReader(context)

        context.applicationInfo.sourceDir = velho.path
        val hashVelho = (reader.read() as InstalledApkResult.Ok).sha256

        context.applicationInfo.sourceDir = novo.path
        val hashNovo = (reader.read() as InstalledApkResult.Ok).sha256

        assertEquals(ApkPatcher.sha256Of(velho), hashVelho)
        assertEquals(ApkPatcher.sha256Of(novo), hashNovo)
        assertTrue("o cache não pode sobreviver a uma reinstalação", hashVelho != hashNovo)
    }

    /**
     * A base that cannot be identified is NOT the end of the road: with no base
     * the server sends `patch: null` plus the full path, which is the first rung
     * of the ladder.
     */
    @Test
    fun `APK ilegivel degrada para indisponivel em vez de explodir`() {
        context.applicationInfo.sourceDir = File(temp.newFolder(), "nao-existe.apk").path

        val result = InstalledApkReader(context).read()

        check(result is InstalledApkResult.Unavailable)
        assertTrue(result.reason.contains("nao-existe.apk"))
    }
}
