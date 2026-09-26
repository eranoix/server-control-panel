package dev.servercontrolpanel.data.update

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import dev.servercontrolpanel.patchengine.ExpectedApk
import dev.servercontrolpanel.patchengine.HPatchCode
import dev.servercontrolpanel.patchengine.PatchResult
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
 * The fallback ladder, rung by rung: no path may leave the app stuck without an
 * update and without an explanation, and an APK whose SHA-256 does not match must
 * never reach the installer (`hpatchz` over the wrong base exits with success and
 * writes a complete, wrong file).
 */
@RunWith(RobolectricTestRunner::class)
class UpdateCoordinatorTest {

    private val context: Context = ApplicationProvider.getApplicationContext()

    /**
     * [newApkSha] is the real SHA-256 of these bytes: the coordinator re-hashes the
     * APK on disk to decide whether a retry can skip the rebuild.
     */
    private val newApkContent = ByteArray(4096) { (it % 97).toByte() }
    private val newApkSha = sha256(newApkContent)
    private val patchSha = "b".repeat(64)
    private val fullSha = "c".repeat(64)
    private val baseSha = "d".repeat(64)

    private val patch = UpdateArtifact(url = "/x?file=p", sizeBytes = 1_400_329, sha256 = patchSha)
    private val full = UpdateArtifact(url = "/x?file=f", sizeBytes = 10_029_237, sha256 = fullSha)

    private fun manifest(withPatch: Boolean = true, versionCode: Long = 7, upToDate: Boolean = false) = UpdateManifest(
        latest = UpdateRelease(
            versionName = "0.1.7",
            versionCode = versionCode,
            apkSha256 = newApkSha,
            apkSizeBytes = newApkContent.size.toLong(),
        ),
        upToDate = upToDate,
        patch = if (withPatch) patch else null,
        full = full,
        patchTool = "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m",
    )

    @Test
    fun `a new version with a patch announces the patch size, not the APK size`() {
        val state = decideAvailability(manifest(), installedVersionCode = 6)

        check(state is UpdateState.Available)
        assertEquals(1_400_329L, state.downloadBytes)
        assertTrue(state.incremental)
    }

    @Test
    fun `an unknown base announces the full artifact size, not the raw APK size`() {
        val state = decideAvailability(manifest(withPatch = false), installedVersionCode = 6)

        check(state is UpdateState.Available)
        assertEquals(10_029_237L, state.downloadBytes)
        assertFalse(state.incremental)
    }

    @Test
    fun `no banner when already on the latest version`() {
        assertEquals(UpdateState.Idle, decideAvailability(manifest(upToDate = true), installedVersionCode = 7))
    }

    /**
     * A development build has an unknown hash (`up_to_date` is false) and a higher
     * `versionCode`; offering it the older release would end in `INSTALL_FAILED_VERSION_DOWNGRADE`.
     */
    @Test
    fun `never offers a downgrade to a build newer than the published one`() {
        assertEquals(UpdateState.Idle, decideAvailability(manifest(versionCode = 7), installedVersionCode = 9))
    }

    @Test
    fun `an applied and verified patch reaches the installer`() = withCoordinator { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals(listOf(patchSha), lab.source.downloaded)
        assertEquals(1, lab.installer.commits.size)
        assertEquals(newApkSha, lab.patcher.expectedReceived?.sha256)
    }

    @Test
    fun `a corrupt patch is retried once before falling back to the full APK`() = withCoordinator(
        downloads = { artifact ->
            if (artifact.sha256 == patchSha) {
                ArtifactDownloadProgress.Failed("corrupted", corrupt = true)
            } else {
                null
            }
        },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        // Two patch attempts, then the full APK.
        assertEquals(listOf(patchSha, patchSha, fullSha), lab.source.downloaded)
        assertEquals(1, lab.installer.commits.size)
    }

    /** A dropped connection does not skip to the full APK: the partial file stays so the download can resume. */
    @Test
    fun `a dropped connection keeps the partial download instead of falling back to the full APK`() = withCoordinator(
        downloads = { ArtifactDownloadProgress.Failed("Connection failed.", corrupt = false) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals(listOf(patchSha), lab.source.downloaded)
        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertTrue(state.canRetry)
        assertEquals(0, lab.installer.commits.size)
    }

    @Test
    fun `a rebuilt APK with a different hash is discarded and the base hash is logged`() = withCoordinator(
        patches = { base, _ ->
            if (base.length() > 0) {
                PatchResult.IntegrityMismatch(expectedSha256 = newApkSha, actualSha256 = "e".repeat(64))
            } else {
                null
            }
        },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals(listOf(patchSha, fullSha), lab.source.downloaded)
        assertEquals(1, lab.installer.commits.size)
        val diagnostic = lab.diagnostics.joinToString("\n")
        assertTrue("the base hash must be recorded:\n$diagnostic", diagnostic.contains(baseSha))
        assertTrue(diagnostic.contains("e".repeat(64)))
        assertTrue("the tool that built the patch is what reveals the incompatibility", diagnostic.contains("hdiffz"))
    }

    /**
     * If neither the patch nor the full download produces an APK with a matching
     * hash, nothing is installed and the screen points at the server's install page.
     */
    @Test
    fun `an APK with a mismatching hash never reaches the installer`() = withCoordinator(
        patches = { _, _ -> PatchResult.IntegrityMismatch(expectedSha256 = newApkSha, actualSha256 = "f".repeat(64)) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals("no commit may have happened", 0, lab.installer.commits.size)
        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertEquals(UpdateRecovery.USE_BROWSER, state.recovery)
        assertTrue(state.message.contains("/android/install"))
        assertTrue("the install session must be abandoned", lab.installer.abandoned.isNotEmpty())
    }

    @Test
    fun `an hpatchz error falls back to the full APK`() = withCoordinator(
        patches = { base, _ ->
            if (base.length() > 0) PatchResult.NativeFailure(HPatchCode.HPATCH_ERROR, 11) else null
        },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals(listOf(patchSha, fullSha), lab.source.downloaded)
        assertEquals(1, lab.installer.commits.size)
    }

    @Test
    fun `a missing native engine falls back to the full APK instead of hanging`() = withCoordinator(
        patches = { base, _ ->
            if (base.length() > 0) PatchResult.EngineUnavailable("no libhpatchz.so for this ABI") else null
        },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals(1, lab.installer.commits.size)
    }

    @Test
    fun `not enough space reports the missing MB and does not start downloading`() = withCoordinator(
        reservation = { StorageReservation.NotEnoughSpace(missingBytes = 5_500_000) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals("nothing may have been downloaded", emptyList<String>(), lab.source.downloaded)
        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertEquals(UpdateRecovery.FREE_SPACE, state.recovery)
        assertTrue("the message must state the number: ${state.message}", state.message.contains("5.5"))
    }

    /** Failing to QUERY free space must not block a perfectly good update. */
    @Test
    fun `an unavailable free-space query does not block the update`() = withCoordinator(
        reservation = { StorageReservation.Unknown("volume without UUID") },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals(1, lab.installer.commits.size)
    }

    @Test
    fun `a full disk detected by the patcher also reports the missing MB`() = withCoordinator(
        patches = { _, _ -> PatchResult.InsufficientStorage(requiredBytes = 31_135_416, usableBytes = 20_000_000) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertEquals(UpdateRecovery.FREE_SPACE, state.recovery)
        assertEquals(0, lab.installer.commits.size)
    }

    @Test
    fun `a failed install keeps the APK and logs the system message`() = withCoordinator(
        installation = { InstallOutcome.Failed("INSTALL_FAILED_UPDATE_INCOMPATIBLE: signatures do not match", blocked = false) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertTrue(state.canRetry)
        assertEquals(UpdateRecovery.SHOW_DIAGNOSTICS, state.recovery)
        assertTrue(state.message.contains("signatures do not match"))
        assertTrue("the APK must be kept for a retry", lab.staging.rebuiltApkFile(newApkSha).isFile)
    }

    /** Retrying after a failed install demotes nothing: the intact APK is already on disk. */
    @Test
    fun `retrying after a failed install does not demote the artifact`() = withCoordinator(
        installation = { InstallOutcome.Failed("some error", blocked = false) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()
        val downloadedOnFirst = lab.source.downloaded.size

        lab.coordinator.start()
        lab.advance()

        assertEquals("the second attempt must not download again", downloadedOnFirst, lab.source.downloaded.size)
        assertEquals(2, lab.installer.commits.size)
    }

    @Test
    fun `denied unknown sources points to the toggle and opens no session`() = withCoordinator(
        canInstall = false,
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertEquals(UpdateRecovery.ALLOW_UNKNOWN_SOURCES, state.recovery)
        assertEquals(0, lab.installer.sessionsCreated)
        assertEquals(emptyList<String>(), lab.source.downloaded)
    }

    /** Android 16 Advanced Protection and enterprise policy block sideloading, so retrying cannot help. */
    @Test
    fun `a device that blocks sideloading points to the install page`() = withCoordinator(
        installation = { InstallOutcome.Failed("Installation blocked by device policy", blocked = true) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertFalse("offering a retry here would only fail again", state.canRetry)
        assertEquals(UpdateRecovery.USE_BROWSER, state.recovery)
        assertTrue(state.message.contains("https://panel.example.com/android/install"))
    }

    /** An unexpected exception must not leave the banner stuck on "Installing" with no way to retry. */
    @Test
    fun `an unexpected failure becomes an error state, never a stuck banner`() = withCoordinator(
        installation = { throw IllegalStateException("session no longer exists") },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertTrue(state.canRetry)
        assertEquals(UpdateRecovery.SHOW_DIAGNOSTICS, state.recovery)
        assertTrue(lab.diagnostics.joinToString("\n").contains("IllegalStateException"))
    }

    /**
     * A sideloaded app (no installer on record) gets `INSTALL_FAILED_ABORTED: Self update
     * is blocked by unknown source package` when the session declares our own package,
     * so it must not declare it.
     */
    @Test
    fun `with an unknown source the session does not declare its own package`() = withCoordinator(
        knownSource = false,
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals(listOf(false), lab.installer.declared)
    }

    /** Pre-approval requires a declared session, so without a known source it is never requested. */
    @Test
    fun `with an unknown source pre-approval is not requested`() = withCoordinator(
        knownSource = false,
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals(null, lab.installer.requestedLabel)
    }

    /**
     * Pre-approval and installing without user action are mutually exclusive: asking for
     * both makes the system abandon the session, so pre-approval is never requested.
     */
    @Test
    fun `pre-approval is never requested because it conflicts with a silent install`() = withCoordinator { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals("with a known source no pre-approval is requested", null, lab.installer.requestedLabel)
    }

    /** With a known source the session is still declared (and silent). */
    @Test
    fun `with a known source the session is still declared`() = withCoordinator { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertEquals(listOf(true), lab.installer.declared)
    }

    /** Last rung: a refused session hands the APK to the system installer instead of looping on retry. */
    @Test
    fun `a refused session falls back to the system installer`() = withCoordinator(
        installation = { InstallOutcome.Failed("Self update is blocked by unknown source package", blocked = false) },
        systemAccepts = true,
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        assertTrue(lab.installer.openedInSystem != null)
        assertTrue(lab.coordinator.state.value is UpdateState.Installing)
    }

    /** If the system installer also refuses, the error must carry the system's own wording. */
    @Test
    fun `without a system installer the error shows the system message`() = withCoordinator(
        installation = { InstallOutcome.Failed("Self update is blocked by unknown source package", blocked = false) },
        systemAccepts = false,
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Failed)
        assertTrue(state.message.contains("Self update is blocked"))
        assertTrue(state.canRetry)
    }

    @Test
    fun `an unpublished channel stays idle instead of showing an error`() = withCoordinator(
        check = UpdateCheckResult.ChannelNotPublished,
    ) { lab ->
        lab.coordinator.check()

        assertEquals(UpdateState.Idle, lab.coordinator.state.value)
    }

    @Test
    fun `a network error while checking does not show a banner`() = withCoordinator(
        check = UpdateCheckResult.Error("Connection failed."),
    ) { lab ->
        lab.coordinator.check()

        assertEquals(UpdateState.Idle, lab.coordinator.state.value)
    }

    @Test
    fun `cancelling offers the update again instead of hiding it`() = withCoordinator(
        downloads = { ArtifactDownloadProgress.Failed("Connection failed.", corrupt = false) },
    ) { lab ->
        lab.coordinator.check()
        lab.coordinator.start()
        lab.advance()

        lab.coordinator.cancel()

        val state = lab.coordinator.state.value
        check(state is UpdateState.Available)
        assertEquals("0.1.7", state.versionName)
    }

    private class Lab(
        val coordinator: UpdateCoordinator,
        val source: FakeSource,
        val installer: FakeInstaller,
        val patcher: FakePatcher,
        val staging: UpdateStaging,
        val diagnostics: MutableList<String>,
        val advance: () -> Unit,
    )

    /**
     * Wires the coordinator up with fakes and runs [tile]. Each parameter forces one
     * failure rung; the defaults are the happy path.
     */
    private fun withCoordinator(
        check: UpdateCheckResult? = null,
        downloads: (UpdateArtifact) -> ArtifactDownloadProgress.Failed? = { null },
        patches: (File, File) -> PatchResult? = { _, _ -> null },
        reservation: (Long) -> StorageReservation = { StorageReservation.Reserved },
        installation: () -> InstallOutcome = { InstallOutcome.Committed },
        preapproval: PreapprovalOutcome = PreapprovalOutcome.Approved,
        canInstall: Boolean = true,
        knownSource: Boolean = true,
        systemAccepts: Boolean = false,
        tile: suspend (Lab) -> Unit,
    ) = runTest {
        val dispatcher = StandardTestDispatcher(testScheduler)
        val scope = TestScope(dispatcher)
        val staging = object : UpdateStaging(context) {
            override fun reserve(bytes: Long): StorageReservation = reservation(bytes)
        }
        staging.sweep(emptySet())
        val baseApk = File(staging.dir, "installed-base.apk").apply { writeBytes(ByteArray(4096) { 0x5a }) }
        val source = FakeSource(check ?: UpdateCheckResult.Success(manifest()), downloads)
        val installer = FakeInstaller(canInstall, preapproval, installation, knownSource, systemAccepts)
        val patcher = FakePatcher(patches, newApkContent)
        val diagnostics = mutableListOf<String>()

        val coordinator = UpdateCoordinator(
            source = source,
            staging = staging,
            installer = installer,
            readInstalledApk = { InstalledApkResult.Ok(baseApk, baseSha) },
            installedVersionCode = 6,
            appLabel = "Server Control Panel",
            patcher = patcher,
            recordDiagnostic = { diagnostics += it },
            manualInstallUrl = { "https://panel.example.com/android/install" },
            scope = scope,
            ioDispatcher = dispatcher,
        )

        tile(Lab(coordinator, source, installer, patcher, staging, diagnostics) { advanceUntilIdle() })
    }

    private class FakeSource(
        private val check: UpdateCheckResult,
        private val downloadFailure: (UpdateArtifact) -> ArtifactDownloadProgress.Failed?,
    ) : UpdateSource {
        val downloaded = mutableListOf<String>()

        override suspend fun check(baseSha256: String?): UpdateCheckResult = check

        override fun download(artifact: UpdateArtifact, target: File): Flow<ArtifactDownloadProgress> = flow {
            downloaded += artifact.sha256
            emit(ArtifactDownloadProgress.Progress(artifact.sizeBytes / 2, artifact.sizeBytes))
            val failure = downloadFailure(artifact)
            if (failure != null) {
                if (failure.corrupt) target.delete()
                emit(failure)
            } else {
                target.parentFile?.mkdirs()
                target.writeBytes(ByteArray(64) { 0x33 })
                emit(ArtifactDownloadProgress.Progress(artifact.sizeBytes, artifact.sizeBytes))
                emit(ArtifactDownloadProgress.Done(target))
            }
        }
    }

    private class FakeInstaller(
        private val canInstall: Boolean,
        private val preapproval: PreapprovalOutcome,
        private val result: () -> InstallOutcome,
        private val knownSource: Boolean = true,
        private val systemAccepts: Boolean = false,
    ) : ApkInstallerPort {
        var sessionsCreated = 0
        /** Sessions created declaring our own package (the "self update" path). */
        val declared = mutableListOf<Boolean>()
        var openedInSystem: File? = null
        val commits = mutableListOf<File>()
        val committedAt = mutableListOf<Int>()
        val abandoned = mutableListOf<Int>()
        var requestedLabel: String? = null

        override fun canInstallFromUnknownSources(): Boolean = canInstall

        override fun installSourceKnown(): Boolean = knownSource

        override fun openSystemInstaller(apk: File): Boolean {
            openedInSystem = apk
            return systemAccepts
        }

        override fun createSession(apkSizeBytes: Long, declarePackage: Boolean): Int {
            sessionsCreated++
            declared += declarePackage
            return sessionsCreated
        }

        override fun abandon(sessionId: Int) {
            abandoned += sessionId
        }

        override suspend fun requestPreapproval(sessionId: Int, label: CharSequence): PreapprovalOutcome {
            requestedLabel = label.toString()
            return preapproval
        }

        override suspend fun commit(sessionId: Int, apk: File): InstallOutcome {
            commits += apk
            committedAt += sessionId
            return result()
        }
    }

    /**
     * Mimics `:patch-engine`: it returns [PatchResult.Applied] only after writing a file
     * with the declared SHA-256, so a coordinator installing a missing file would fail.
     */
    private class FakePatcher(
        private val script: (File, File) -> PatchResult?,
        private val apkContent: ByteArray,
    ) : ApkPatcherPort {
        var expectedReceived: ExpectedApk? = null

        override fun apply(baseApk: File, patch: File, outputApk: File, expected: ExpectedApk): PatchResult {
            expectedReceived = expected
            script(baseApk, patch)?.let {
                // The real engine deletes the output before returning IntegrityMismatch.
                if (it is PatchResult.IntegrityMismatch) outputApk.delete()
                return it
            }
            outputApk.parentFile?.mkdirs()
            outputApk.writeBytes(apkContent)
            return PatchResult.Applied(outputApk, expected.sha256)
        }
    }

    private companion object {
        fun sha256(bytes: ByteArray): String =
            MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
    }
}
