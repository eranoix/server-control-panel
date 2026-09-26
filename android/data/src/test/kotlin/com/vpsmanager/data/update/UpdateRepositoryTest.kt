package com.vpsmanager.data.update

import com.vpsmanager.core.model.ServerConfig
import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.data.config.ServerConfigStore
import com.vpsmanager.mobileapiclient.api.MobileApi
import java.io.File
import java.security.MessageDigest
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/**
 * Proof of the update channel against a real server (MockWebServer).
 *
 * The focus is RESUMPTION: the owner of this app has bad internet, and a 10 MB
 * download that restarts from zero on every drop never finishes. The tests
 * here are not satisfied with "the API was called": they cut the download in
 * half, check the bytes left on disk, and check that the second attempt asks
 * for exactly the right offset and produces an intact file.
 */
class UpdateRepositoryTest {

    @get:Rule
    val temp = TemporaryFolder()

    private lateinit var server: MockWebServer

    /** 40 KiB of deterministic content — big enough to cross several 64 KiB buffers halfway. */
    private val conteudo = ByteArray(40 * 1024) { (it % 251).toByte() }
    private val conteudoSha = sha256(conteudo)

    @Before
    fun start() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun stop() {
        server.shutdown()
    }

    private fun repository() = UpdateRepository(
        serverConfigRepository = ServerConfigRepository(FakeStore(baseUrl())),
        mobileApiFactory = { basePath -> MobileApi(basePath) },
    )

    private fun baseUrl() = server.url("/").toString().removeSuffix("/")

    private fun artifact(size: Int = conteudo.size, sha: String = conteudoSha) = UpdateArtifact(
        url = "/api/mobile/v1/app/update/artifact?file=patches%2Fa-b.hdiff",
        sizeBytes = size.toLong(),
        sha256 = sha,
    )

    // ------------------------------------------------------------------
    // Manifesto
    // ------------------------------------------------------------------

    /**
     * The regression that matters most in this file. The server OMITS `patch`
     * when there is none for the base it was given, and the generated client
     * has to accept that — before the contract marked the field optional,
     * `kotlinx.serialization` blew up with "unexpected null" and the ENTIRE
     * feature died on the most common rung of the ladder (unknown base).
     */
    @Test
    fun manifestoSemPatchNaoQuebraADesserializacao() = runTest {
        server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody(MANIFESTO_SEM_PATCH))

        val result = repository().check(baseSha256 = "aa".repeat(32))

        check(result is UpdateCheckResult.Success)
        assertNull(result.manifest.patch)
        assertEquals(10_029_237L, result.manifest.full.sizeBytes)
        assertEquals("0.1.6", result.manifest.latest.versionName)
        assertFalse(result.manifest.upToDate)
    }

    /** The other shape of the same case: an explicit `"patch": null`, which an older server would send. */
    @Test
    fun manifestoComPatchNuloExplicitoTambemEAceito() = runTest {
        server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody(MANIFESTO_PATCH_NULO))

        val result = repository().check(baseSha256 = null)

        check(result is UpdateCheckResult.Success)
        assertNull(result.manifest.patch)
    }

    @Test
    fun manifestoComPatchDevolveOTamanhoDoPatchNaoODoApk() = runTest {
        server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody(MANIFESTO_COM_PATCH))

        val result = repository().check(baseSha256 = "bb".repeat(32))

        check(result is UpdateCheckResult.Success)
        assertEquals(1_400_329L, result.manifest.patch?.sizeBytes)
        assertEquals(31_135_416L, result.manifest.latest.apkSizeBytes)
    }

    /** 503 is server state ("I have not published anything yet"), not an error — and the app must not confuse the two. */
    @Test
    fun canalNaoPublicadoNaoViraErro() = runTest {
        server.enqueue(MockResponse().setResponseCode(503).setBody("""{"detail":"canal de atualização ainda não publicado"}"""))

        assertEquals(UpdateCheckResult.ChannelNotPublished, repository().check(baseSha256 = null))
    }

    @Test
    fun semServidorConfiguradoNaoTentaRede() = runTest {
        val repo = UpdateRepository(
            serverConfigRepository = ServerConfigRepository(FakeStore(null)),
            mobileApiFactory = { basePath -> MobileApi(basePath) },
        )

        val result = repo.check(baseSha256 = null)

        check(result is UpdateCheckResult.Error)
        assertEquals(0, server.requestCount)
    }

    // ------------------------------------------------------------------
    // Resumable download
    // ------------------------------------------------------------------

    @Test
    fun downloadCompletoDoZeroConfereOHashEEntregaOArquivo() = runTest {
        server.enqueue(respostaTotal(conteudo))
        val alvo = File(temp.newFolder(), "artefato.hdiff")

        val eventos = repository().download(artifact(), alvo).toList()

        val done = eventos.filterIsInstance<ArtifactDownloadProgress.Done>().single()
        assertArrayEquals(conteudo, done.file.readBytes())
        // No Range on the first trip: there was nothing on disk to resume.
        val pedido = server.takeRequest()
        assertNull(pedido.getHeader("Range"))
        assertNull(pedido.getHeader("If-Range"))
    }

    /**
     * THE proof of resumption, in two parts.
     *
     * Part 1: the server delivers only half and stops. The download fails as
     * "connection" (not as "corrupted"), and — the point — the partial file
     * STAYS on disk with exactly the bytes received.
     *
     * Part 2: the second attempt asks for `Range: bytes=<half>-` with the
     * ETag's `If-Range`, gets a 206 with the rest, and the final file is
     * byte-for-byte identical to the original — with the SHA-256 checked.
     */
    @Test
    fun downloadInterrompidoRetomaDeOndeParou() = runTest {
        val metade = conteudo.size / 2
        server.enqueue(respostaParcial(conteudo, de = 0, ate = metade - 1, totalDeclarado = conteudo.size))
        server.enqueue(respostaParcial(conteudo, de = metade, ate = conteudo.size - 1, totalDeclarado = conteudo.size))

        val alvo = File(temp.newFolder(), "artefato.hdiff")
        val repo = repository()

        val primeira = repo.download(artifact(), alvo).toList()
        val falha = primeira.filterIsInstance<ArtifactDownloadProgress.Failed>().single()
        assertFalse("queda de conexão não pode ser tratada como corrupção", falha.corrupt)
        assertEquals("o parcial tem que ficar no disco para a retomada", metade.toLong(), alvo.length())
        server.takeRequest()

        val segunda = repo.download(artifact(), alvo).toList()

        val done = segunda.filterIsInstance<ArtifactDownloadProgress.Done>().single()
        assertArrayEquals(conteudo, done.file.readBytes())
        assertEquals(conteudoSha, sha256(done.file.readBytes()))

        val retomada = server.takeRequest()
        assertEquals("bytes=$metade-", retomada.getHeader("Range"))
        assertEquals("\"$conteudoSha\"", retomada.getHeader("If-Range"))
    }

    /**
     * `If-Range` did not match: the server answers 200 with the WHOLE file.
     * Writing that from the old offset would splice old bytes onto new ones —
     * silently. The file has to be truncated first.
     */
    @Test
    fun respostaDuzentosAUmPedidoDeRangeTruncaOParcialEmVezDeEmendar() = runTest {
        val alvo = File(temp.newFolder(), "artefato.hdiff")
        alvo.writeBytes(ByteArray(1000) { 0x7f })
        server.enqueue(respostaTotal(conteudo))

        val eventos = repository().download(artifact(), alvo).toList()

        val done = eventos.filterIsInstance<ArtifactDownloadProgress.Done>().single()
        assertArrayEquals(conteudo, done.file.readBytes())
        assertEquals(conteudo.size.toLong(), alvo.length())
    }

    /** 416: the disk holds more bytes than the whole artifact. Delete it and ask for a fresh attempt. */
    @Test
    fun faixaImpossivelApagaOParcialEPedeNovaTentativa() = runTest {
        val alvo = File(temp.newFolder(), "artefato.hdiff")
        alvo.writeBytes(ByteArray(conteudo.size - 1) { 0x11 })
        server.enqueue(MockResponse().setResponseCode(416))

        val eventos = repository().download(artifact(), alvo).toList()

        val falha = eventos.filterIsInstance<ArtifactDownloadProgress.Failed>().single()
        assertTrue(falha.corrupt)
        assertFalse("o parcial inválido não pode sobreviver", alvo.exists())
    }

    /**
     * The rung that protects everything downstream: bytes that arrive with the
     * wrong hash never become input to `hpatchz`. And the file is DELETED —
     * resuming a corrupted download would only repeat the corruption.
     */
    @Test
    fun hashDivergenteApagaOArquivoEMarcaComoCorrompido() = runTest {
        server.enqueue(respostaTotal(conteudo))
        val alvo = File(temp.newFolder(), "artefato.hdiff")

        val eventos = repository().download(artifact(sha = "ff".repeat(32)), alvo).toList()

        val falha = eventos.filterIsInstance<ArtifactDownloadProgress.Failed>().single()
        assertTrue(falha.corrupt)
        assertFalse(alvo.exists())
    }

    /** Already downloaded and intact: does not spend the owner's internet again. */
    @Test
    fun arquivoJaCompletoEIntegroNaoEBaixadoOutraVez() = runTest {
        val alvo = File(temp.newFolder(), "artefato.hdiff")
        alvo.writeBytes(conteudo)

        val eventos = repository().download(artifact(), alvo).toList()

        assertTrue(eventos.single() is ArtifactDownloadProgress.Done)
        assertEquals(0, server.requestCount)
    }

    /** A partial BIGGER than the target is not a resumption, it is leftovers from something else: start over from zero. */
    @Test
    fun parcialMaiorQueOAlvoEDescartadoEODownloadRecomeca() = runTest {
        val alvo = File(temp.newFolder(), "artefato.hdiff")
        alvo.writeBytes(ByteArray(conteudo.size + 500) { 0x22 })
        server.enqueue(respostaTotal(conteudo))

        val eventos = repository().download(artifact(), alvo).toList()

        assertTrue(eventos.filterIsInstance<ArtifactDownloadProgress.Done>().size == 1)
        assertNull(server.takeRequest().getHeader("Range"))
    }

    @Test
    fun progressoEReportadoAteOTotal() = runTest {
        server.enqueue(respostaTotal(conteudo))
        val alvo = File(temp.newFolder(), "artefato.hdiff")

        val eventos = repository().download(artifact(), alvo).toList()

        val progressos = eventos.filterIsInstance<ArtifactDownloadProgress.Progress>()
        assertTrue("tem que haver progresso antes do fim", progressos.isNotEmpty())
        assertEquals(conteudo.size.toLong(), progressos.last().downloadedBytes)
        assertEquals(conteudo.size.toLong(), progressos.last().totalBytes)
    }

    // ------------------------------------------------------------------
    // URL resolution — the manifest's `url` comes off the network
    // ------------------------------------------------------------------

    @Test
    fun urlDoManifestoEResolvidaContraABaseConfigurada() {
        val resolvida = resolveArtifactUrl(
            baseUrl = "https://vpsm.exemplo.com/api/mobile/v1",
            artifactUrl = "/api/mobile/v1/app/update/artifact?file=patches%2Fa-b.hdiff",
        )

        assertEquals(
            "https://vpsm.exemplo.com/api/mobile/v1/app/update/artifact?file=patches%2Fa-b.hdiff",
            resolvida.toString(),
        )
    }

    /**
     * The `url` comes from a network response. Following it blindly is how an
     * open redirect turns into token exfiltration: this stack's
     * `Authorization` interceptor would attach the Bearer to whatever host
     * showed up there.
     */
    @Test
    fun urlQueTrocaDeHostERecusada() {
        assertNull(resolveArtifactUrl("https://vpsm.exemplo.com/api/mobile/v1", "https://atacante.exemplo/x.hdiff"))
        assertNull(resolveArtifactUrl("https://vpsm.exemplo.com/api/mobile/v1", "http://vpsm.exemplo.com/x.hdiff"))
    }

    @Test
    fun contentRangeEInterpretadoPeloServidorNaoPeloQueFoiPedido() {
        assertEquals(1024L, contentRangeStart("bytes 1024-2047/4096"))
        assertEquals(0L, contentRangeStart("bytes 0-10/11"))
        assertNull(contentRangeStart(null))
        assertNull(contentRangeStart("items 1-2/3"))
    }

    // ------------------------------------------------------------------
    // Helpers
    // ------------------------------------------------------------------

    private fun respostaTotal(corpo: ByteArray) = MockResponse()
        .setResponseCode(200)
        .setHeader("ETag", "\"${sha256(corpo)}\"")
        .setBody(okio.Buffer().write(corpo))

    private fun respostaParcial(corpo: ByteArray, de: Int, ate: Int, totalDeclarado: Int) = MockResponse()
        .setResponseCode(206)
        .setHeader("ETag", "\"${sha256(corpo)}\"")
        .setHeader("Content-Range", "bytes $de-$ate/$totalDeclarado")
        .setBody(okio.Buffer().write(corpo, de, ate - de + 1))

    private fun assertArrayEquals(esperado: ByteArray, obtido: ByteArray) {
        assertEquals("tamanho", esperado.size, obtido.size)
        assertTrue("conteúdo byte a byte", esperado.contentEquals(obtido))
    }

    private class FakeStore(private val baseUrl: String?) : ServerConfigStore {
        override fun load(): ServerConfig? = baseUrl?.let { ServerConfig(baseUrl = it, allowInsecureHttp = true) }
        override fun save(config: ServerConfig) = Unit
        override fun clear() = Unit
    }

    private companion object {
        fun sha256(bytes: ByteArray): String =
            MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

        const val MANIFESTO_SEM_PATCH = """
            {
              "latest": {"version_name":"0.1.6","version_code":6,"sha256":"4456a4ca","size_bytes":31135416},
              "up_to_date": false,
              "full": {"url":"/api/mobile/v1/app/update/artifact?file=full%2Fx.hdiff","size_bytes":10029237,"sha256":"deadbeef"},
              "patch_tool": "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m"
            }
        """

        const val MANIFESTO_PATCH_NULO = """
            {
              "latest": {"version_name":"0.1.6","version_code":6,"sha256":"4456a4ca","size_bytes":31135416},
              "up_to_date": false,
              "patch": null,
              "full": {"url":"/api/mobile/v1/app/update/artifact?file=full%2Fx.hdiff","size_bytes":10029237,"sha256":"deadbeef"},
              "patch_tool": "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m"
            }
        """

        const val MANIFESTO_COM_PATCH = """
            {
              "latest": {"version_name":"0.1.6","version_code":6,"sha256":"4456a4ca","size_bytes":31135416},
              "up_to_date": false,
              "patch": {"url":"/api/mobile/v1/app/update/artifact?file=patches%2Fa-b.hdiff","size_bytes":1400329,"sha256":"cafe"},
              "full": {"url":"/api/mobile/v1/app/update/artifact?file=full%2Fx.hdiff","size_bytes":10029237,"sha256":"deadbeef"},
              "patch_tool": "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m"
            }
        """
    }
}
