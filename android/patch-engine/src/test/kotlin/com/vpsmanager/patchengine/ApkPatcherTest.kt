package com.vpsmanager.patchengine

import java.io.File
import java.nio.file.Files
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * JVM (host) tests of the logic around the native call: missing inputs, disk
 * space, error-code translation and — most important — what happens when the
 * native side says "ok" and hands back the wrong bytes.
 *
 * The `.so` is bionic: it does not load on a host JVM, nor under Robolectric.
 * That is why [NativePatcher] exists as a seam. The real native path is proven
 * in `ApkPatcherSmokeTest` and `ApkPatcherRealApkTest` (instrumented).
 */
class ApkPatcherTest {

    private val tempDir: File = Files.createTempDirectory("patch-engine-test").toFile()

    @After
    fun tearDown() {
        tempDir.deleteRecursively()
    }

    private fun file(name: String, bytes: ByteArray = ByteArray(0)): File =
        File(tempDir, name).apply { writeBytes(bytes) }

    /** Fake native that writes [produces] to the output and returns [returns]. */
    private fun fakeNative(returns: Int, produces: ByteArray? = null) = NativePatcher {
        _, _, outNewFileName, _, _, _ ->
        if (produces != null) File(outNewFileName).writeBytes(produces)
        returns
    }

    private val newBytes = "conteudo novo".toByteArray()
    private val newSha = ApkPatcher.sha256Of(File(tempDir, "seed").apply { writeBytes(newBytes) })
    private val expected get() = ExpectedApk(sha256 = newSha, sizeBytes = newBytes.size.toLong())

    @Test
    fun apply_quandoTudoBate_devolveAppliedComOHashCalculado() {
        val patcher = ApkPatcher(fakeNative(returns = 0, produces = newBytes))
        val out = File(tempDir, "out.apk")

        val result = patcher.apply(file("base.apk", byteArrayOf(1)), file("p.hdiff", byteArrayOf(2)), out, expected)

        assertTrue("esperava Applied, veio $result", result is PatchResult.Applied)
        result as PatchResult.Applied
        assertEquals(out, result.newFile)
        assertEquals(newSha, result.sha256)
        assertTrue(out.exists())
    }

    @Test
    fun apply_quandoNativoDizOkMasOsBytesEstaoErrados_reprovaEApagaASaida() {
        // This is the scenario that justifies verification living in this
        // module: it was MEASURED on the host's hpatchz that applying a patch
        // over a wrong base of the same size returns 0 and writes a whole,
        // wrong file.
        val patcher = ApkPatcher(fakeNative(returns = 0, produces = "outra coisa".toByteArray()))
        val out = File(tempDir, "out.apk")

        val result = patcher.apply(file("base.apk", byteArrayOf(1)), file("p.hdiff", byteArrayOf(2)), out, expected)

        assertTrue("esperava IntegrityMismatch, veio $result", result is PatchResult.IntegrityMismatch)
        result as PatchResult.IntegrityMismatch
        assertEquals(newSha, result.expectedSha256)
        assertFalse("o arquivo errado nao pode sobreviver ao resultado", out.exists())
    }

    @Test
    fun apply_hashEsperadoEmMaiusculas_continuaCasando() {
        val patcher = ApkPatcher(fakeNative(returns = 0, produces = newBytes))
        val out = File(tempDir, "out.apk")

        val result = patcher.apply(
            file("base.apk", byteArrayOf(1)),
            file("p.hdiff", byteArrayOf(2)),
            out,
            ExpectedApk(sha256 = newSha.uppercase(), sizeBytes = newBytes.size.toLong()),
        )

        assertTrue("hash e hexadecimal, caixa nao deveria importar; veio $result", result is PatchResult.Applied)
    }

    @Test
    fun apply_semApkBase_naoChamaONativo() {
        var called = false
        val patcher = ApkPatcher(NativePatcher { _, _, _, _, _, _ -> called = true; 0 })

        val result = patcher.apply(
            File(tempDir, "nao-existe.apk"),
            file("p.hdiff", byteArrayOf(2)),
            File(tempDir, "out.apk"),
            expected,
        )

        assertEquals(
            PatchResult.InputMissing(PatchResult.InputRole.BASE_APK, File(tempDir, "nao-existe.apk").path),
            result,
        )
        assertFalse("nao faz sentido entrar no JNI sem os arquivos", called)
    }

    @Test
    fun apply_semPatch_reportaQualEntradaFaltou() {
        val patcher = ApkPatcher(fakeNative(returns = 0, produces = newBytes))

        val result = patcher.apply(
            file("base.apk", byteArrayOf(1)),
            File(tempDir, "nao-existe.hdiff"),
            File(tempDir, "out.apk"),
            expected,
        )

        assertTrue(result is PatchResult.InputMissing)
        assertEquals(PatchResult.InputRole.PATCH, (result as PatchResult.InputMissing).role)
    }

    @Test
    fun apply_erroNativoComum_viraNativeFailureTraduzido() {
        val patcher = ApkPatcher(fakeNative(returns = 9))
        val out = File(tempDir, "out.apk")

        val result = patcher.apply(file("base.apk", byteArrayOf(1)), file("p.hdiff", byteArrayOf(2)), out, expected)

        assertEquals(PatchResult.NativeFailure(HPatchCode.HDIFF_INFO_ERROR, 9), result)
        assertFalse(out.exists())
    }

    @Test
    fun apply_discoCheioNoMeioDaEscrita_viraInsufficientStorageENaoCodigoNativo() {
        // 24 = HPATCH_FILEWRITE_NO_SPACE_ERROR. The caller reacts to this
        // differently (asking to free up space), and should not have to know
        // the native enum to find out.
        val patcher = ApkPatcher(fakeNative(returns = 24))

        val result = patcher.apply(
            file("base.apk", byteArrayOf(1)),
            file("p.hdiff", byteArrayOf(2)),
            File(tempDir, "out.apk"),
            expected,
        )

        assertTrue("esperava InsufficientStorage, veio $result", result is PatchResult.InsufficientStorage)
        assertEquals(newBytes.size.toLong(), (result as PatchResult.InsufficientStorage).requiredBytes)
    }

    @Test
    fun apply_semAlibHpatchz_viraEngineUnavailable() {
        val patcher = ApkPatcher(
            NativePatcher { _, _, _, _, _, _ -> throw UnsatisfiedLinkError("dlopen failed: libhpatchz.so") },
        )

        val result = patcher.apply(
            file("base.apk", byteArrayOf(1)),
            file("p.hdiff", byteArrayOf(2)),
            File(tempDir, "out.apk"),
            expected,
        )

        assertTrue("esperava EngineUnavailable, veio $result", result is PatchResult.EngineUnavailable)
    }

    @Test
    fun apply_apagaSobraDeExecucaoAnteriorAntesDeChamarONativo() {
        val out = File(tempDir, "out.apk")
        out.writeBytes("apk pela metade de uma tentativa que falhou".toByteArray())

        var sawLeftover = true
        val patcher = ApkPatcher(
            NativePatcher { _, _, outNewFileName, _, _, _ ->
                sawLeftover = File(outNewFileName).exists()
                File(outNewFileName).writeBytes(newBytes)
                0
            },
        )

        val result = patcher.apply(file("base.apk", byteArrayOf(1)), file("p.hdiff", byteArrayOf(2)), out, expected)

        assertFalse("o nativo nao pode encontrar resto de tentativa anterior", sawLeftover)
        assertTrue(result is PatchResult.Applied)
    }

    @Test
    fun apply_repassaOCacheMemoryPadraoDeQuatroMebibytes() {
        var seenCache = -1L
        var seenThreads = -1
        var seenChecksum = false
        val patcher = ApkPatcher(
            NativePatcher { _, _, outNewFileName, cacheMemory, threadNum, isChecksumNewData ->
                seenCache = cacheMemory
                seenThreads = threadNum
                seenChecksum = isChecksumNewData
                File(outNewFileName).writeBytes(newBytes)
                0
            },
        )

        patcher.apply(file("base.apk", byteArrayOf(1)), file("p.hdiff", byteArrayOf(2)), File(tempDir, "out.apk"), expected)

        // 4 MiB is not a decorative number: with _IS_NEED_CACHE_OLD_ALL=1 in
        // Android.mk, a cacheMemory >= the size of the old APK makes the
        // patcher load the whole APK into memory. If someone "optimizes" this
        // default upwards, this test is the warning.
        assertEquals(4L * 1024 * 1024, seenCache)
        assertEquals(4L * 1024 * 1024, ApkPatcher.DEFAULT_CACHE_MEMORY_BYTES)
        assertEquals(1, seenThreads)
        assertTrue(seenChecksum)
    }

    @Test
    fun sha256Of_baseComNoVetorConhecidoDoNist() {
        val f = File(tempDir, "abc.bin").apply { writeBytes("abc".toByteArray()) }
        assertEquals(
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
            ApkPatcher.sha256Of(f),
        )
    }

    @Test
    fun sha256Of_arquivoMaiorQueOBufferDeLeitura() {
        // 64 KiB is the size of the internal buffer; going past it exercises the loop.
        val bytes = ByteArray(200_000) { (it % 251).toByte() }
        val f = File(tempDir, "grande.bin").apply { writeBytes(bytes) }

        val digest = java.security.MessageDigest.getInstance("SHA-256").digest(bytes)
        val hex = digest.joinToString("") { "%02x".format(it) }

        assertEquals(hex, ApkPatcher.sha256Of(f))
    }

    @Test
    fun hPatchCode_traduzOsSaltosDoEnumUpstream() {
        // Upstream jumps from 18 to 20 and from 25 to 103; translating by
        // `ordinal` would fail silently on exactly the checksum codes.
        assertEquals(HPatchCode.OPTIONS_ERROR, HPatchCode.fromRaw(1))
        assertEquals(HPatchCode.DECOMPRESSER_OPEN_ERROR, HPatchCode.fromRaw(20))
        assertEquals(HPatchCode.FILE_WRITE_NO_SPACE_ERROR, HPatchCode.fromRaw(24))
        assertEquals(HPatchCode.CHECKSUM_SET_ERROR, HPatchCode.fromRaw(103))
        assertEquals(HPatchCode.CHECKSUM_NEWDATA_ERROR, HPatchCode.fromRaw(106))
    }

    @Test
    fun hPatchCode_codigoDesconhecidoNaoSeDisfarcaDeConhecido() {
        assertEquals(HPatchCode.UNKNOWN, HPatchCode.fromRaw(201))
        assertEquals(HPatchCode.UNKNOWN, HPatchCode.fromRaw(9999))

        val patcher = ApkPatcher(fakeNative(returns = 201))
        val result = patcher.apply(
            file("base.apk", byteArrayOf(1)),
            file("p.hdiff", byteArrayOf(2)),
            File(tempDir, "out.apk"),
            expected,
        )
        // The raw integer survives: it is the only way to trace it in upstream's source.
        assertEquals(PatchResult.NativeFailure(HPatchCode.UNKNOWN, 201), result)
    }
}
