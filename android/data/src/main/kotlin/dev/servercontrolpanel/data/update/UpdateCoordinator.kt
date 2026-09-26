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

/** What the update can offer the owner after a failure. */
enum class UpdateRecovery {
    /** Nothing beyond trying again. */
    NONE,

    /** Out of space; the message already says how many MB. */
    FREE_SPACE,

    /** The install failed for a reason only the system message explains; the full text is in Diagnostics. */
    SHOW_DIAGNOSTICS,

    /** "Install unknown apps" is off; there is a direct shortcut to the switch. */
    ALLOW_UNKNOWN_SOURCES,

    /** This device will not install this way; the `/android/install` page is what is left. */
    USE_BROWSER,
}

/** The state the banner draws. */
sealed interface UpdateState {

    /** Nothing to show: no update, or the channel has not been published yet. */
    data object Idle : UpdateState

    data object Checking : UpdateState

    /**
     * There is a new version. [downloadBytes] is the real download size (patch
     * if present, otherwise the full artifact), never the rebuilt APK size.
     */
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

    /** Rebuilding the APK from the patch: seconds, no network. */
    data class Applying(val versionName: String) : UpdateState

    data class Installing(val versionName: String) : UpdateState

    data class Failed(
        val message: String,
        val canRetry: Boolean,
        val recovery: UpdateRecovery = UpdateRecovery.NONE,
    ) : UpdateState

    /**
     * "Nothing new", shown only for a check the user asked for (the automatic
     * check stays silent). Clears itself after a moment.
     */
    data class UpToDate(val versionName: String) : UpdateState

    /** A check the user asked for could not reach the server (the automatic check swallows this). */
    data class CheckFailed(val message: String) : UpdateState
}

/** A testable boundary over `PackageInstaller`, which does not exist on a host JVM. */
interface ApkInstallerPort {
    fun canInstallFromUnknownSources(): Boolean

    /**
     * False when Android does not know who installed this app, as with any
     * hand-downloaded APK. See `ApkInstaller.installSourceKnown`.
     */
    fun installSourceKnown(): Boolean = true

    /** Returns false when nothing on the device is able to open an APK. */
    fun openSystemInstaller(apk: File): Boolean = false

    fun createSession(apkSizeBytes: Long, declarePackage: Boolean = true): Int?
    fun abandon(sessionId: Int)
    suspend fun requestPreapproval(sessionId: Int, label: CharSequence): PreapprovalOutcome
    suspend fun commit(sessionId: Int, apk: File): InstallOutcome
}

/** A testable boundary over the native `hpatchz`. */
fun interface ApkPatcherPort {
    fun apply(baseApk: File, patch: File, outputApk: File, expected: ExpectedApk): PatchResult
}

/** The real implementation, on top of `:patch-engine`. */
class RealApkPatcher(private val patcher: ApkPatcher = ApkPatcher()) : ApkPatcherPort {
    override fun apply(baseApk: File, patch: File, outputApk: File, expected: ExpectedApk): PatchResult =
        patcher.apply(baseApk = baseApk, patch = patch, outputApk = outputApk, expected = expected)
}

/**
 * Drives the update from beginning to end and never gets stuck.
 *
 * Fallback ladder (each failure gets its own reaction, hence typed results):
 *
 * | situation | what happens |
 * |---|---|
 * | unknown base (dev build, version outside the window) | server sends `patch: null`; take the full artifact |
 * | the `.hdiff` arrived corrupt | download it again once, then drop to the full artifact |
 * | rebuilt APK with a different hash | discard it, drop to the full artifact, record the base hash in diagnostics |
 * | `hpatchz` error | drop to the full artifact |
 * | not enough space | say how many MB are missing and do not start |
 * | download interrupted | the partial file stays; the next attempt resumes it by Range |
 * | install fails | keep the APK for a retry; the system message goes to Diagnostics |
 * | unknown sources denied | direct shortcut to the switch |
 * | device blocks sideloading | say so and point at `/android/install` |
 *
 * Inviolable rule: an APK whose SHA-256 was not checked NEVER reaches the
 * installer. `hpatchz` over the wrong base returns success with a wrong file, so
 * only [PatchResult.Applied] from `:patch-engine` counts as ready.
 */
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
    // Applying the patch blocks for seconds; injectable so tests can await it
    // (`advanceUntilIdle` does not wait for a hard-wired `Dispatchers.IO`).
    private val ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
) {

    private val _state = MutableStateFlow<UpdateState>(UpdateState.Idle)
    val state: StateFlow<UpdateState> = _state.asStateFlow()

    @Volatile
    private var manifest: UpdateManifest? = null

    @Volatile
    private var runningJob: Job? = null

    /**
     * Fetches the manifest (small JSON, nothing downloaded). Network errors are
     * silent on purpose: on a bad connection such a banner is constant noise with
     * nothing to act on.
     */
    suspend fun check() {
        // Never during a download or install: the manifest would change under it.
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

    /**
     * The check the owner asks for explicitly. Unlike [check], failures and
     * "nothing new" are reported, and a new version starts updating right away
     * since the tap was the authorisation. Android's own install confirmation
     * still applies.
     */
    suspend fun checkAndUpdate() {
        // Do not restart an update in progress, which would discard what already arrived.
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

    /**
     * Clears [UpdateState.UpToDate] and [UpdateState.CheckFailed] after a delay,
     * only if still current, so a download started meanwhile is not wiped.
     */
    private fun deleteResponseAfterRead() {
        scope.launch {
            kotlinx.coroutines.delay(RESPONSE_DURATION_MS)
            val current = _state.value
            if (current is UpdateState.UpToDate || current is UpdateState.CheckFailed) {
                _state.value = UpdateState.Idle
            }
        }
    }

    /** Starts (or resumes) download+apply+install. Idempotent while already running. */
    fun start() {
        if (runningJob?.isActive == true) return
        runningJob = scope.launch {
            try {
                runUpdate()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Throwable) {
                // Safety net for unexpected exceptions, which would otherwise
                // leave the banner stuck on "Installing…" with no retry.
                recordDiagnostic("Update: unexpected failure — ${e.javaClass.simpleName}: ${e.message}")
                _state.value = UpdateState.Failed(
                    "The update stopped with an unexpected error. Diagnostics has the details.",
                    canRetry = true,
                    recovery = UpdateRecovery.SHOW_DIAGNOSTICS,
                )
            }
        }
    }

    /** Cancels whatever is in flight. The partial `.hdiff` stays on disk for resuming. */
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

        // With a known install origin: a declared session. Without one: a regular
        // session, since Android refuses a declared one with "Self update is
        // blocked by unknown source package".
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
            // Without a declared session there is no pre-approval (it requires
            // `setAppPackageName`), so it is not even attempted.
            if (!canDeclareSelf) {
                recordDiagnostic(
                    "Update: this app was installed outside an app store, so Android does not " +
                        "allow confirming before the download. The confirmation appears at the end.",
                )
            }
            // No pre-approval: the declared session requests install without user
            // action, and asking for pre-approval on it makes the system abandon the
            // session (`INSTALL_FAILED_ABORTED: Session was abandoned`). If the
            // silent install is refused, the confirmation appears after the download.

            val candidates = listOfNotNull(current.patch, current.full)
            val apkTarget = staging.rebuiltApkFile(current.latest.apkSha256)
            val expected = ExpectedApk(sha256 = current.latest.apkSha256, sizeBytes = current.latest.apkSizeBytes)

            // Reuse an intact APK from a previous install attempt instead of
            // downloading and rebuilding it again.
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
                        // Connection dropped: the larger full artifact would drop
                        // too. The partial file stays for the next attempt.
                        is DownloadAttempt.Abort -> return
                        // Corrupt twice: this path is bad, try the next one.
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
                            // `hpatchz` said OK but produced wrong bytes (already
                            // deleted by :patch-engine). Record the base hash to
                            // investigate why the patch did not fit.
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
                    // The system took over and this process dies next. Artifacts
                    // are cleaned up on the next launch.
                }
                is InstallOutcome.Failed -> {
                    // Last fallback: the APK is on disk with its hash checked, so
                    // hand it to the standard install screen, which works where the
                    // session was refused. Otherwise it would loop on "try again".
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
                        // The rebuilt APK stays on disk, so a retry repeats
                        // neither the download nor the rebuild.
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

    /**
     * Downloads [artifact], retrying exactly once if it arrives corrupt; a second
     * wrong hash means the path is bad. A dropped connection aborts instead, since
     * resuming the partial file is what fixes it.
     */
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

    /**
     * The base `hpatchz` runs against. The full artifact is also a `.hdiff`
     * (against an empty base), so only the base file changes, not the algorithm.
     */
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
        /**
         * How long the answer to a requested check stays on screen: 6 s, enough to
         * read a line after closing the drawer that covers the banner.
         */
        const val RESPONSE_DURATION_MS = 6_000L

        // Same unit and locale as formatDownloadSize, so all sizes shown match.
        fun megabytes(bytes: Long): String = String.format(PT_BR, "%.1f", bytes / 1_000_000.0)
    }
}

/** Fixed pt-BR locale, so sizes use a decimal comma ("1,4 MB"). */
private val PT_BR: Locale = Locale.forLanguageTag("pt-BR")

/**
 * The download size shown in the banner, with one decimal place ("1,4 MB") and
 * KB below 1 MB. Decimal MB (10^6), the unit Android Settings and the update
 * notes use.
 */
fun formatDownloadSize(bytes: Long): String = when {
    bytes < 1_000 -> "$bytes B"
    bytes < 1_000_000 -> String.format(PT_BR, "%d KB", bytes / 1_000)
    else -> String.format(PT_BR, "%.1f MB", bytes / 1_000_000.0)
}

/**
 * The outcome of downloading one candidate. The two failures need opposite
 * reactions: a dropped connection must resume (the full artifact would drop too),
 * while corrupt bytes twice mean moving to the next candidate.
 */
private sealed interface DownloadAttempt {
    data class Ready(val file: File) : DownloadAttempt

    /** Stop. The screen already explains, and the partial file stays for resuming. */
    data object Abort : DownloadAttempt

    /** Move to the next candidate. */
    data object NextCandidate : DownloadAttempt
}

/**
 * Pure decision: does this manifest become a banner? The `versionCode` check is
 * not redundant with `up_to_date`: a dev build (unknown hash, higher
 * `versionCode`) would otherwise be offered an older version that Android refuses
 * with `INSTALL_FAILED_VERSION_DOWNGRADE` after the whole download.
 */
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
