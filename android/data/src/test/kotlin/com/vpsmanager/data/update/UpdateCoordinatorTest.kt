package com.vpsmanager.data.update

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.vpsmanager.patchengine.ExpectedApk
import com.vpsmanager.patchengine.HPatchCode
import com.vpsmanager.patchengine.PatchResult
import java.io.File
import java.security.MessageDigest
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * The fallback ladder, rung by rung.
 *
 * What each test here defends is not "the code runs": it is that NO path ends
 * with the app stuck without an update and without an explanation, and — the
 * inviolable rule — that an APK whose SHA-256 does not match NEVER reaches the
 * installer. That last one is [apkComHashDivergenteNuncaChegaAoInstalador], and
 * it exists because `hpatchz` applied over the wrong base RETURNS SUCCESS and
 * writes a complete, wrong file: its exit code is evidence of nothing.
 */
@RunWith(RobolectricTestRunner::class)
class UpdateCoordinatorTest {

    private val context: Context = ApplicationProvider.getApplicationContext()

    /**
     * The "rebuilt" APK in this test is 4 KiB, not 31 MB — and
     * [apkNovoSha] is the REAL SHA-256 of those bytes, computed here. That
     * matters: the coordinator re-reads the hash of the APK on disk (with the
     * real `ApkPatcher.sha256Of`) to decide whether it can skip the rebuild on
     * a retry, and a made-up hash would leave that decision never exercised.
     */
    private val apkNovoConteudo = ByteArray(4096) { (it % 97).toByte() }
    private val apkNovoSha = sha256(apkNovoConteudo)
    private val patchSha = "b".repeat(64)
    private val fullSha = "c".repeat(64)
    private val baseSha = "d".repeat(64)

    private val patch = UpdateArtifact(url = "/x?file=p", sizeBytes = 1_400_329, sha256 = patchSha)
    private val full = UpdateArtifact(url = "/x?file=f", sizeBytes = 10_029_237, sha256 = fullSha)

    private fun manifest(comPatch: Boolean = true, versionCode: Long = 7, upToDate: Boolean = false) = UpdateManifest(
        latest = UpdateRelease(
            versionName = "0.1.7",
            versionCode = versionCode,
            apkSha256 = apkNovoSha,
            apkSizeBytes = apkNovoConteudo.size.toLong(),
        ),
        upToDate = upToDate,
        patch = if (comPatch) patch else null,
        full = full,
        patchTool = "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m",
    )

    // ------------------------------------------------------------------
    // Degrau 0 — o que vira banner
    // ------------------------------------------------------------------

    @Test
    fun `versao nova com patch anuncia o tamanho do PATCH, nao o do APK`() {
        val state = decideAvailability(manifest(), installedVersionCode = 6)

        check(state is UpdateState.Available)
        assertEquals(1_400_329L, state.downloadBytes)
        assertTrue(state.incremental)
    }

    @Test
    fun `base desconhecida anuncia o completo — 10 MB, nao os 31 do APK cru`() {
        val state = decideAvailability(manifest(comPatch = false), installedVersionCode = 6)

        check(state is UpdateState.Available)
        assertEquals(10_029_237L, state.downloadBytes)
        assertFalse(state.incremental)
    }

    @Test
    fun `quem ja esta na ultima versao nao ve banner`() {
        assertEquals(UpdateState.Idle, decideAvailability(manifest(upToDate = true), installedVersionCode = 7))
    }

    /**
     * A development build has an unknown hash (so `up_to_date` is false) and a
     * higher `versionCode`. Without this guard it would be offered an OLDER
     * version to install — which Android refuses with
     * `INSTALL_FAILED_VERSION_DOWNGRADE`, after spending the whole download.
     */
    @Test
    fun `nunca oferece downgrade para um build mais novo que o publicado`() {
        assertEquals(UpdateState.Idle, decideAvailability(manifest(versionCode = 7), installedVersionCode = 9))
    }

    // ------------------------------------------------------------------
    // Degrau 1 — caminho feliz
    // ------------------------------------------------------------------

    @Test
    fun `patch aplicado e conferido chega ao instalador`() = comCoordenador { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals(listOf(patchSha), lab.source.baixados)
        assertEquals(1, lab.installer.commits.size)
        assertEquals(apkNovoSha, lab.patcher.expectedRecebido?.sha256)
    }

    // ------------------------------------------------------------------
    // Rung 2 — patch with the wrong SHA-256: demote once, then the full APK
    // ------------------------------------------------------------------

    @Test
    fun `patch corrompido e rebaixado UMA vez e so entao cai para o completo`() = comCoordenador(
        downloads = { artifact ->
            if (artifact.sha256 == patchSha) {
                ArtifactDownloadProgress.Failed("corrompido", corrupt = true)
            } else {
                null
            }
        },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        // Two patch attempts (the second is the "demote once"), then the full APK.
        assertEquals(listOf(patchSha, patchSha, fullSha), lab.source.baixados)
        assertEquals(1, lab.installer.commits.size)
    }

    /** A dropped connection does NOT spend the attempt or skip to the full APK: the partial stays and resuming is what fixes it. */
    @Test
    fun `queda de conexao nao derruba para o completo — o parcial fica para retomar`() = comCoordenador(
        downloads = { ArtifactDownloadProgress.Failed("Falha de conexão.", corrupt = false) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals(listOf(patchSha), lab.source.baixados)
        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertTrue(state.canRetry)
        assertEquals(0, lab.installer.commits.size)
    }

    // ------------------------------------------------------------------
    // Rung 3 — rebuilt APK with a different hash (the central finding)
    // ------------------------------------------------------------------

    @Test
    fun `APK reconstruido com hash diferente e descartado e o hash da base vai para o diagnostico`() = comCoordenador(
        patches = { base, _ ->
            if (base.length() > 0) {
                PatchResult.IntegrityMismatch(expectedSha256 = apkNovoSha, actualSha256 = "e".repeat(64))
            } else {
                null
            }
        },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals(listOf(patchSha, fullSha), lab.source.baixados)
        assertEquals(1, lab.installer.commits.size)
        val diagnostico = lab.diagnosticos.joinToString("\n")
        assertTrue("o hash da base tem que ficar registrado:\n$diagnostico", diagnostico.contains(baseSha))
        assertTrue(diagnostico.contains("e".repeat(64)))
        assertTrue("a ferramenta que gerou o patch é o que permite achar a incompatibilidade", diagnostico.contains("hdiffz"))
    }

    /**
     * THE INVIOLABLE RULE. If neither the patch nor the full download produces
     * an APK whose hash matches, nothing is installed — and the screen points
     * at the server's install page instead of going on offering "try again".
     */
    @Test
    fun `apkComHashDivergenteNuncaChegaAoInstalador`() = comCoordenador(
        patches = { _, _ -> PatchResult.IntegrityMismatch(expectedSha256 = apkNovoSha, actualSha256 = "f".repeat(64)) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals("NENHUM commit pode ter acontecido", 0, lab.installer.commits.size)
        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertEquals(UpdateRecovery.USE_BROWSER, state.recovery)
        assertTrue(state.message.contains("/android/install"))
        assertTrue("a sessão de instalação tem que ser abandonada", lab.installer.abandonadas.isNotEmpty())
    }

    // ------------------------------------------------------------------
    // Rung 4 — patcher error
    // ------------------------------------------------------------------

    @Test
    fun `erro do hpatchz no patch cai para o completo`() = comCoordenador(
        patches = { base, _ ->
            if (base.length() > 0) PatchResult.NativeFailure(HPatchCode.HPATCH_ERROR, 11) else null
        },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals(listOf(patchSha, fullSha), lab.source.baixados)
        assertEquals(1, lab.installer.commits.size)
    }

    @Test
    fun `motor nativo ausente cai para o completo em vez de travar`() = comCoordenador(
        patches = { base, _ ->
            if (base.length() > 0) PatchResult.EngineUnavailable("sem libhpatchz.so para esta ABI") else null
        },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals(1, lab.installer.commits.size)
    }

    // ------------------------------------------------------------------
    // Rung 5 — not enough space
    // ------------------------------------------------------------------

    @Test
    fun `sem espaco diz quantos MB faltam e nao comeca a baixar`() = comCoordenador(
        reserva = { StorageReservation.NotEnoughSpace(missingBytes = 5_500_000) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals("nada pode ter sido baixado", emptyList<String>(), lab.source.baixados)
        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertEquals(UpdateRecovery.FREE_SPACE, state.recovery)
        assertTrue("a mensagem tem que dizer o número: ${state.message}", state.message.contains("5,5"))
    }

    /** Failing to QUERY free space must not block a perfectly good update. */
    @Test
    fun `consulta de espaco indisponivel nao impede a atualizacao`() = comCoordenador(
        reserva = { StorageReservation.Unknown("volume sem UUID") },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals(1, lab.installer.commits.size)
    }

    @Test
    fun `disco cheio detectado pelo patcher tambem diz quantos MB faltam`() = comCoordenador(
        patches = { _, _ -> PatchResult.InsufficientStorage(requiredBytes = 31_135_416, usableBytes = 20_000_000) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertEquals(UpdateRecovery.FREE_SPACE, state.recovery)
        assertEquals(0, lab.installer.commits.size)
    }

    // ------------------------------------------------------------------
    // Rung 6 — installation
    // ------------------------------------------------------------------

    @Test
    fun `instalacao falhada guarda o APK e manda a mensagem do sistema para o diagnostico`() = comCoordenador(
        instalacao = { InstallOutcome.Failed("INSTALL_FAILED_UPDATE_INCOMPATIBLE: assinaturas não conferem", blocked = false) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertTrue(state.canRetry)
        assertEquals(UpdateRecovery.SHOW_DIAGNOSTICS, state.recovery)
        assertTrue(state.message.contains("assinaturas não conferem"))
        assertTrue("o APK tem que ficar guardado para retentar", lab.staging.rebuiltApkFile(apkNovoSha).isFile)
    }

    /** Retrying after a failed install demotes nothing: the intact APK is already on disk. */
    @Test
    fun `retentar apos falha de instalacao nao rebaixa o artefato`() = comCoordenador(
        instalacao = { InstallOutcome.Failed("erro qualquer", blocked = false) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()
        val baixadosNaPrimeira = lab.source.baixados.size

        lab.coordinator.start()
        lab.avancar()

        assertEquals("a segunda tentativa não pode gastar internet", baixadosNaPrimeira, lab.source.baixados.size)
        assertEquals(2, lab.installer.commits.size)
    }

    @Test
    fun `fontes desconhecidas negada aponta para o interruptor e nao abre sessao`() = comCoordenador(
        podeInstalar = false,
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertEquals(UpdateRecovery.ALLOW_UNKNOWN_SOURCES, state.recovery)
        assertEquals(0, lab.installer.sessoesCriadas)
        assertEquals(emptyList<String>(), lab.source.baixados)
    }

    /** Android 16 Advanced Protection and enterprise policy: retrying does not help, and the screen must not pretend it does. */
    @Test
    fun `aparelho que bloqueia sideload aponta para a pagina de instalacao`() = comCoordenador(
        instalacao = { InstallOutcome.Failed("Instalação bloqueada pela política do dispositivo", blocked = true) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertFalse("oferecer retentar num muro é empurrar o dono para o mesmo muro", state.canRetry)
        assertEquals(UpdateRecovery.USE_BROWSER, state.recovery)
        assertTrue(state.message.contains("https://vpsm.exemplo.com/android/install"))
    }

    // ------------------------------------------------------------------
    // Pre-approval
    // ------------------------------------------------------------------





    /**
     * Safety net: an exception that NO typed result anticipated must not leave
     * the banner stuck on an eternal "Installing…" — which is indistinguishable
     * from working and leaves no way to retry.
     */
    @Test
    fun `falha inesperada vira estado de erro, nunca um banner preso`() = comCoordenador(
        instalacao = { throw IllegalStateException("sessão já não existe") },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertTrue(state.canRetry)
        assertEquals(UpdateRecovery.SHOW_DIAGNOSTICS, state.recovery)
        assertTrue(lab.diagnosticos.joinToString("\n").contains("IllegalStateException"))
    }

    // ------------------------------------------------------------------
    // Origem de instalacao desconhecida (APK baixado a mao)
    // ------------------------------------------------------------------

    /**
     * THE DEFECT, reported with eight attempts in a row on the owner's device:
     *
     * ```
     * INSTALL_FAILED_ABORTED: Self update is blocked by unknown source package
     * ```
     *
     * The message names both conditions. One of them — the app having been
     * sideloaded, with no installer on record — has no fix on the app side
     * (setting the installer requires `INSTALL_PACKAGES`, a privileged
     * permission). The other one does: stop declaring the session as an update
     * of our own package. This test guards exactly that decision.
     */
    @Test
    fun `com origem desconhecida a sessao nao se declara como do proprio pacote`() = comCoordenador(
        origemConhecida = false,
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals(listOf(false), lab.installer.declararam)
    }

    /**
     * Pre-approval REQUIRES the declared session, so without a known origin it
     * cannot even be attempted — asking for it would destroy the session and
     * the download would start over from zero.
     */
    @Test
    fun `com origem desconhecida a preaprovacao nem e pedida`() = comCoordenador(
        origemConhecida = false,
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals(null, lab.installer.rotuloPedido)
    }

    /**
     * THE NEW RULE, and the defect it closes.
     *
     * Pre-approval ("ask before the download") and installing with no user
     * action ("do not ask anything") are MUTUALLY EXCLUSIVE. Both depended on
     * the same condition, and once the install provenance became known both
     * turned on together: the session declared that there would be no prompt
     * and, on the next line, asked for approval. The system abandoned the
     * session, with `INSTALL_FAILED_ABORTED: Session was abandoned` on the
     * owner's device.
     *
     * Pre-approval is now asked for on no path at all — neither with a known
     * origin (the session is silent) nor without one (pre-approval requires a
     * declared session). This test locks both halves.
     */
    @Test
    fun `pre-aprovacao nunca e pedida — ela colide com a instalacao sem dialogo`() = comCoordenador { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals("com origem CONHECIDA nao se pede aprovacao previa", null, lab.installer.rotuloPedido)
    }

    /** Com origem conhecida a sessao continua declarada — e agora tambem silenciosa. */
    @Test
    fun `com origem conhecida a sessao continua declarada`() = comCoordenador { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertEquals(listOf(true), lab.installer.declararam)
    }

    /**
     * The last rung of the ladder. Without it, a refused session turns into a
     * "try again" loop that never goes anywhere — which is what the owner
     * lived through.
     */
    @Test
    fun `sessao recusada cai para o instalador do sistema`() = comCoordenador(
        instalacao = { InstallOutcome.Failed("Self update is blocked by unknown source package", blocked = false) },
        sistemaAceita = true,
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        assertTrue(lab.installer.abertoNoSistema != null)
        assertTrue(lab.coordinator.state.value is UpdateState.Installing)
    }

    /**
     * If not even the system installer accepts it, the error has to surface
     * carrying the SYSTEM's own wording. "Installation cancelled" hid the one
     * clue to why eight attempts had failed.
     */
    @Test
    fun `sem instalador do sistema o erro mostra a frase do sistema`() = comCoordenador(
        instalacao = { InstallOutcome.Failed("Self update is blocked by unknown source package", blocked = false) },
        sistemaAceita = false,
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertTrue(state.message.contains("Self update is blocked"))
        assertTrue(state.canRetry)
    }

    // ------------------------------------------------------------------
    // Canal / cancelamento
    // ------------------------------------------------------------------

    @Test
    fun `canal ainda nao publicado fica quieto em vez de mostrar erro`() = comCoordenador(
        verificacao = UpdateCheckResult.ChannelNotPublished,
    ) { lab ->
        lab.coordinator.check()

        assertEquals(UpdateState.Idle, lab.coordinator.state.value)
    }

    @Test
    fun `erro de rede ao verificar nao vira banner`() = comCoordenador(
        verificacao = UpdateCheckResult.Error("Falha de conexão."),
    ) { lab ->
        lab.coordinator.check()

        assertEquals(UpdateState.Idle, lab.coordinator.state.value)
    }

    @Test
    fun `cancelar volta a oferecer a atualizacao em vez de sumir`() = comCoordenador(
        downloads = { ArtifactDownloadProgress.Failed("Falha de conexão.", corrupt = false) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.avancar()

        lab.coordinator.cancel()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Available)
        assertEquals("0.1.7", state.versionName)
    }

    // ------------------------------------------------------------------
    // Harness
    // ------------------------------------------------------------------

    private class Lab(
        val coordinator: UpdateCoordinator,
        val source: FakeSource,
        val installer: FakeInstaller,
        val patcher: FakePatcher,
        val staging: UpdateStaging,
        val diagnosticos: MutableList<String>,
        val avancar: () -> Unit,
    )

    /**
     * Wires the coordinator up with fakes and runs [bloco].
     *
     * Each parameter is a rung of the ladder the test wants to force. The
     * default is the happy path — which makes every test state, in its own
     * signature, exactly which failure it is proving.
     */
    private fun comCoordenador(
        verificacao: UpdateCheckResult? = null,
        downloads: (UpdateArtifact) -> ArtifactDownloadProgress.Failed? = { null },
        patches: (File, File) -> PatchResult? = { _, _ -> null },
        reserva: (Long) -> StorageReservation = { StorageReservation.Reserved },
        instalacao: () -> InstallOutcome = { InstallOutcome.Committed },
        preaprovacao: PreapprovalOutcome = PreapprovalOutcome.Approved,
        podeInstalar: Boolean = true,
        origemConhecida: Boolean = true,
        sistemaAceita: Boolean = false,
        bloco: suspend (Lab) -> Unit,
    ) = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val scope = TestScope(dispatcher)
        val staging = object : UpdateStaging(context) {
            override fun reserve(bytes: Long): StorageReservation = reserva(bytes)
        }
        staging.sweep(emptySet())
        val baseApk = File(staging.dir, "base-instalada.apk").apply { writeBytes(ByteArray(4096) { 0x5a }) }
        val source = FakeSource(verificacao ?: UpdateCheckResult.Success(manifest()), downloads)
        val installer = FakeInstaller(podeInstalar, preaprovacao, instalacao, origemConhecida, sistemaAceita)
        val patcher = FakePatcher(patches, apkNovoConteudo)
        val diagnosticos = mutableListOf<String>()

        val coordinator = UpdateCoordinator(
            source = source,
            staging = staging,
            installer = installer,
            readInstalledApk = { InstalledApkResult.Ok(baseApk, baseSha) },
            installedVersionCode = 6,
            appLabel = "VPS Manager",
            patcher = patcher,
            recordDiagnostic = { diagnosticos += it },
            manualInstallUrl = { "https://vpsm.exemplo.com/android/install" },
            scope = scope,
            ioDispatcher = dispatcher,
        )

        bloco(Lab(coordinator, source, installer, patcher, staging, diagnosticos) { advanceUntilIdle() })
    }

    private class FakeSource(
        private val verificacao: UpdateCheckResult,
        private val falhaDeDownload: (UpdateArtifact) -> ArtifactDownloadProgress.Failed?,
    ) : UpdateSource {
        val baixados = mutableListOf<String>()

        override suspend fun check(baseSha256: String?): UpdateCheckResult = verificacao

        override fun download(artifact: UpdateArtifact, target: File): Flow<ArtifactDownloadProgress> = flow {
            baixados += artifact.sha256
            emit(ArtifactDownloadProgress.Progress(artifact.sizeBytes / 2, artifact.sizeBytes))
            val falha = falhaDeDownload(artifact)
            if (falha != null) {
                if (falha.corrupt) target.delete()
                emit(falha)
            } else {
                target.parentFile?.mkdirs()
                target.writeBytes(ByteArray(64) { 0x33 })
                emit(ArtifactDownloadProgress.Progress(artifact.sizeBytes, artifact.sizeBytes))
                emit(ArtifactDownloadProgress.Done(target))
            }
        }
    }

    private class FakeInstaller(
        private val podeInstalar: Boolean,
        private val preaprovacao: PreapprovalOutcome,
        private val resultado: () -> InstallOutcome,
        private val origemConhecida: Boolean = true,
        private val sistemaAceita: Boolean = false,
    ) : ApkInstallerPort {
        var sessoesCriadas = 0
        /** Sessions created declaring our own package (the "self update" path). */
        val declararam = mutableListOf<Boolean>()
        var abertoNoSistema: File? = null
        val commits = mutableListOf<File>()
        val commitadasEm = mutableListOf<Int>()
        val abandonadas = mutableListOf<Int>()
        var rotuloPedido: String? = null

        override fun canInstallFromUnknownSources(): Boolean = podeInstalar

        override fun origemDeInstalacaoConhecida(): Boolean = origemConhecida

        override fun abrirInstaladorDoSistema(apk: File): Boolean {
            abertoNoSistema = apk
            return sistemaAceita
        }

        override fun createSession(apkSizeBytes: Long, declararPacote: Boolean): Int {
            sessoesCriadas++
            declararam += declararPacote
            return sessoesCriadas
        }

        override fun abandon(sessionId: Int) {
            abandonadas += sessionId
        }

        override suspend fun requestPreapproval(sessionId: Int, label: CharSequence): PreapprovalOutcome {
            rotuloPedido = label.toString()
            return preaprovacao
        }

        override suspend fun commit(sessionId: Int, apk: File): InstallOutcome {
            commits += apk
            commitadasEm += sessionId
            return resultado()
        }
    }

    /**
     * Mimics `:patch-engine`, including in the part that matters most: it only
     * returns [PatchResult.Applied] after WRITING a file whose SHA-256 is the
     * declared one. A fake that returned `Applied` without producing bytes
     * would silently let through a coordinator that installs a file that is
     * not there.
     */
    private class FakePatcher(
        private val roteiro: (File, File) -> PatchResult?,
        private val apkConteudo: ByteArray,
    ) : ApkPatcherPort {
        var expectedRecebido: ExpectedApk? = null

        override fun apply(baseApk: File, patch: File, outputApk: File, expected: ExpectedApk): PatchResult {
            expectedRecebido = expected
            roteiro(baseApk, patch)?.let {
                // The real :patch-engine DELETES the file before returning
                // IntegrityMismatch. A fake that left the wrong file on disk
                // would hide a coordinator that went on to install it.
                if (it is PatchResult.IntegrityMismatch) outputApk.delete()
                return it
            }
            outputApk.parentFile?.mkdirs()
            outputApk.writeBytes(apkConteudo)
            return PatchResult.Applied(outputApk, expected.sha256)
        }
    }

    private companion object {
        fun sha256(bytes: ByteArray): String =
            MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
    }
}
