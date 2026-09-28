package dev.servercontrolpanel.data.update

import dev.servercontrolpanel.patchengine.ApkPatcher
import dev.servercontrolpanel.patchengine.ExpectedApk
import dev.servercontrolpanel.patchengine.PatchResult
import java.io.File
import java.util.Locale
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

enum class UpdateRecovery {
    NONE,

    FREE_SPACE,

    SHOW_DIAGNOSTICS,

    ALLOW_UNKNOWN_SOURCES,

    USE_BROWSER,
}

sealed interface UpdateState {

    data object Idle : UpdateState

    data object Checking : UpdateState

    data class Available(
        val versionName: String,
        val downloadBytes: Long,
        val incremental: Boolean,
    ) : UpdateState

    data class Downloading(
        val versionName: String,
        val downloadedBytes: Long,
        val totalBytes: Long,
    ) : UpdateState

    data class Applying(val versionName: String) : UpdateState

    data class Installing(val versionName: String) : UpdateState

    data class Failed(
        val message: String,
        val canRetry: Boolean,
        val recovery: UpdateRecovery = UpdateRecovery.NONE,
    ) : UpdateState

    data class UpToDate(val versionName: String) : UpdateState

    data class CheckFailed(val message: String) : UpdateState
}

interface ApkInstallerPort {
    fun canInstallFromUnknownSources(): Boolean

    fun installSourceKnown(): Boolean = true

    fun openSystemInstaller(apk: File): Boolean = false

    fun createSession(apkSizeBytes: Long, declarePackage: Boolean = true): Int?
    fun abandon(sessionId: Int)
    suspend fun requestPreapproval(sessionId: Int, label: CharSequence): PreapprovalOutcome
    suspend fun commit(sessionId: Int, apk: File): InstallOutcome
}

fun interface ApkPatcherPort {
    fun apply(baseApk: File, patch: File, outputApk: File, expected: ExpectedApk): PatchResult
}

class RealApkPatcher(private val patcher: ApkPatcher = ApkPatcher()) : ApkPatcherPort {
    override fun apply(baseApk: File, patch: File, outputApk: File, expected: ExpectedApk): PatchResult =
        patcher.apply(baseApk = baseApk, patch = patch, outputApk = outputApk, expected = expected)
}

class UpdateCoordinator(
    private val source: UpdateSource,
    private val staging: UpdateStaging,
    private val installer: ApkInstallerPort,
    private val readInstalledApk: () -> InstalledApkResult,
    private val installedVersionCode: Long,
    private val appLabel: CharSequence,
    private val patcher: ApkPatcherPort = RealApkPatcher(),
    private val recordDiagnostic: (String) -> Unit = {},
    private val manualInstallUrl: () -> String? = { null },
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
) {

    private val _state = MutableStateFlow<UpdateState>(UpdateState.Idle)
    val state: StateFlow<UpdateState> = _state.asStateFlow()

    @Volatile
    private var manifest: UpdateManifest? = null

    @Volatile
    private var runningJob: Job? = null

    suspend fun check() {
        if (runningJob?.isActive == true) return
        _state.value = UpdateState.Checking

        val base = readInstalledApk()
        if (base is InstalledApkResult.Unavailable) {
            recordDiagnostic("Update: base not identified (${base.reason}). Only the full path is possible.")
        }
        val baseSha = (base as? InstalledApkResult.Ok)?.sha256

        when (val result = source.check(baseSha)) {
            is UpdateCheckResult.ChannelNotPublished -> _state.value = UpdateState.Idle
            is UpdateCheckResult.Error -> _state.value = UpdateState.Idle
            is UpdateCheckResult.Success -> {
                manifest = result.manifest
                _state.value = decideAvailability(result.manifest, installedVersionCode)
            }
        }
    }

    suspend fun checkAndUpdate() {
        if (runningJob?.isActive == true) return
        _state.value = UpdateState.Checking

        val base = readInstalledApk()
        val baseSha = (base as? InstalledApkResult.Ok)?.sha256

        when (val result = source.check(baseSha)) {
            is UpdateCheckResult.ChannelNotPublished ->
                _state.value = UpdateState.CheckFailed("The server has not published any version of the app yet.")
            is UpdateCheckResult.Error ->
                _state.value = UpdateState.CheckFailed("Could not check for updates right now. Check your network.")
            is UpdateCheckResult.Success -> {
                manifest = result.manifest
                when (val decided = decideAvailability(result.manifest, installedVersionCode)) {
                    is UpdateState.Available -> {
                        _state.value = decided
                        start()
                    }
                    else -> _state.value = UpdateState.UpToDate(result.manifest.latest.versionName)
                }
            }
        }
        deleteResponseAfterRead()
    }

    private fun deleteResponseAfterRead() {
        scope.launch {
            kotlinx.coroutines.delay(RESPONSE_DURATION_MS)
            val current = _state.value
            if (current is UpdateState.UpToDate || current is UpdateState.CheckFailed) {
                _state.value = UpdateState.Idle
            }
        }
    }

    fun start() {
        if (runningJob?.isActive == true) return
        runningJob = scope.launch {
            try {
                runUpdate()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Throwable) {
                recordDiagnostic("Update: unexpected failure — ${e.javaClass.simpleName}: ${e.message}")
                _state.value = UpdateState.Failed(
                    "The update stopped with an unexpected error. Diagnostics has the details.",
                    canRetry = true,
                    recovery = UpdateRecovery.SHOW_DIAGNOSTICS,
                )
            }
        }
    }

    fun cancel() {
        runningJob?.cancel()
        runningJob = null
        val current = manifest
        _state.value = if (current == null) UpdateState.Idle else decideAvailability(current, installedVersionCode)
    }

    private suspend fun runUpdate() {
        val current = manifest ?: return
        val versionName = current.latest.versionName

        if (!installer.canInstallFromUnknownSources()) {
            _state.value = UpdateState.Failed(
                "To update, Android needs your permission for this app to install updates.",
                canRetry = true,
                recovery = UpdateRecovery.ALLOW_UNKNOWN_SOURCES,
            )
            return
        }

        val canDeclareSelf = installer.installSourceKnown()
        var sessionId = installer.createSession(current.latest.apkSizeBytes, canDeclareSelf)
        if (sessionId == null) {
            _state.value = UpdateState.Failed(
                "Could not prepare the installation on this device.",
                canRetry = true,
            )
            return
        }

        var handedOver = false
        try {
            if (!canDeclareSelf) {
                recordDiagnostic(
                    "Update: this app was installed outside an app store, so Android does not " +
                        "allow confirming before the download. The confirmation appears at the end.",
                )
            }

            val candidates = listOfNotNull(current.patch, current.full)
            val apkTarget = staging.rebuiltApkFile(current.latest.apkSha256)
            val expected = ExpectedApk(sha256 = current.latest.apkSha256, sizeBytes = current.latest.apkSizeBytes)

            var readyApk: File? = apkTarget.takeIf { it.isFile && isAlreadyRebuilt(it, expected) }

            if (readyApk == null) {
                for ((index, candidate) in candidates.withIndex()) {
                    val isLast = index == candidates.lastIndex
                    val artifactFile = staging.artifactFile(candidate.sha256)

                    when (val reservation = staging.reserve(candidate.sizeBytes + current.latest.apkSizeBytes)) {
                        is StorageReservation.NotEnoughSpace -> {
                            _state.value = UpdateState.Failed(
                                "Not enough space: free up ${megabytes(reservation.missingBytes)} MB and try again.",
                                canRetry = true,
                                recovery = UpdateRecovery.FREE_SPACE,
                            )
                            return
                        }
                        is StorageReservation.Unknown -> recordDiagnostic(
                            "Update: could not check free space (${reservation.reason}). Continuing anyway.",
                        )
                        is StorageReservation.Reserved -> Unit
                    }

                    val downloaded = when (val attempt = downloadWithOneRetry(candidate, artifactFile, versionName)) {
                        is DownloadAttempt.Ready -> attempt.file
                        is DownloadAttempt.Abort -> return
                        is DownloadAttempt.NextCandidate -> continue
                    }

                    _state.value = UpdateState.Applying(versionName)
                    val baseApk = baseFileFor(candidate, current)
                    val patchResult = withContext(ioDispatcher) {
                        patcher.apply(
                            baseApk = baseApk,
                            patch = downloaded,
                            outputApk = apkTarget,
                            expected = expected,
                        )
                    }

                    when (patchResult) {
                        is PatchResult.Applied -> {
                            readyApk = patchResult.newFile
                        }
                        is PatchResult.InsufficientStorage -> {
                            _state.value = UpdateState.Failed(
                                "Not enough space: free up " +
                                    "${megabytes(patchResult.requiredBytes - patchResult.usableBytes)} MB and try again.",
                                canRetry = true,
                                recovery = UpdateRecovery.FREE_SPACE,
                            )
                            return
                        }
                        is PatchResult.IntegrityMismatch -> {
                            recordDiagnostic(
                                "Update: the rebuilt APK does not match the server's and was discarded.\n" +
                                    "  installed base: ${(readInstalledApk() as? InstalledApkResult.Ok)?.sha256 ?: "unknown"}\n" +
                                    "  expected: ${patchResult.expectedSha256}\n" +
                                    "  got:      ${patchResult.actualSha256}\n" +
                                    "  tool: ${current.patchTool}",
                            )
                            artifactFile.delete()
                        }
                        else -> {
                            recordDiagnostic("Update: failed to rebuild the APK ($patchResult). Trying the full path.")
                            artifactFile.delete()
                        }
                    }

                    if (readyApk != null) break
                }
            }

            val apk = readyApk
            if (apk == null) {
                _state.value = UpdateState.Failed(
                    manualInstallMessage(),
                    canRetry = true,
                    recovery = UpdateRecovery.USE_BROWSER,
                )
                return
            }

            _state.value = UpdateState.Installing(versionName)
            when (val outcome = installer.commit(sessionId, apk)) {
                is InstallOutcome.Committed -> {
                    handedOver = true
                }
                is InstallOutcome.Failed -> {
                    val fellBackToSystem = !outcome.blocked && installer.openSystemInstaller(apk)
                    if (fellBackToSystem) {
                        recordDiagnostic(
                            "Update: the session was rejected (${outcome.message}) — " +
                                "opening the system installer with the already downloaded APK.",
                        )
                        handedOver = true
                        _state.value = UpdateState.Installing(versionName)
                        return
                    }
                    _state.value = UpdateState.Failed(
                    if (outcome.blocked) {
                        "This device does not allow installing apps from outside the store. ${manualInstallMessage()}"
                    } else {
                        "The installation failed: ${outcome.message} The file was kept — you can try again."
                    },
                    canRetry = !outcome.blocked,
                    recovery = if (outcome.blocked) UpdateRecovery.USE_BROWSER else UpdateRecovery.SHOW_DIAGNOSTICS,
                    )
                }
            }
        } finally {
            if (!handedOver) installer.abandon(sessionId)
        }
    }

    private suspend fun downloadWithOneRetry(
        artifact: UpdateArtifact,
        target: File,
        versionName: String,
    ): DownloadAttempt {
        repeat(2) { attempt ->
            var done: File? = null
            var failure: ArtifactDownloadProgress.Failed? = null
            source.download(artifact, target).collect { progress ->
                when (progress) {
                    is ArtifactDownloadProgress.Progress -> _state.value = UpdateState.Downloading(
                        versionName = versionName,
                        downloadedBytes = progress.downloadedBytes,
                        totalBytes = progress.totalBytes,
                    )
                    is ArtifactDownloadProgress.Done -> done = progress.file
                    is ArtifactDownloadProgress.Failed -> failure = progress
                }
            }
            done?.let { return DownloadAttempt.Ready(it) }
            val reason = failure
            if (reason != null && !reason.corrupt) {
                _state.value = UpdateState.Failed(reason.reason, canRetry = true)
                return DownloadAttempt.Abort
            }
            if (attempt == 0) {
                recordDiagnostic("Update: ${reason?.reason ?: "invalid download"} Downloading again.")
            } else {
                recordDiagnostic(
                    "Update: ${reason?.reason ?: "invalid download"} " +
                        "The same wrong hash twice — stepping one rung down the ladder.",
                )
            }
        }
        return DownloadAttempt.NextCandidate
    }

    private fun baseFileFor(artifact: UpdateArtifact, manifest: UpdateManifest): File =
        if (artifact === manifest.patch) {
            (readInstalledApk() as? InstalledApkResult.Ok)?.file ?: staging.emptyBaseFile()
        } else {
            staging.emptyBaseFile()
        }

    private fun isAlreadyRebuilt(apk: File, expected: ExpectedApk): Boolean =
        apk.length() == expected.sizeBytes &&
            runCatching { ApkPatcher.sha256Of(apk) }.getOrNull()?.equals(expected.sha256, ignoreCase = true) == true

    private fun manualInstallMessage(): String {
        val url = manualInstallUrl()
        return if (url == null) {
            "Could not update from here. Install it from the server's install page."
        } else {
            "Could not update from here. Install it from $url"
        }
    }

    private companion object {
        const val RESPONSE_DURATION_MS = 6_000L

        fun megabytes(bytes: Long): String = String.format(SIZE_LOCALE, "%.1f", bytes / 1_000_000.0)
    }
}

private val SIZE_LOCALE: Locale = Locale.US

fun formatDownloadSize(bytes: Long): String = when {
    bytes < 1_000 -> "$bytes B"
    bytes < 1_000_000 -> String.format(SIZE_LOCALE, "%d KB", bytes / 1_000)
    else -> String.format(SIZE_LOCALE, "%.1f MB", bytes / 1_000_000.0)
}

private sealed interface DownloadAttempt {
    data class Ready(val file: File) : DownloadAttempt

    data object Abort : DownloadAttempt

    data object NextCandidate : DownloadAttempt
}

internal fun decideAvailability(manifest: UpdateManifest, installedVersionCode: Long): UpdateState {
    if (manifest.upToDate) return UpdateState.Idle
    if (manifest.latest.versionCode <= installedVersionCode) return UpdateState.Idle
    val artifact = manifest.patch ?: manifest.full
    return UpdateState.Available(
        versionName = manifest.latest.versionName,
        downloadBytes = artifact.sizeBytes,
        incremental = manifest.patch != null,
    )
}
