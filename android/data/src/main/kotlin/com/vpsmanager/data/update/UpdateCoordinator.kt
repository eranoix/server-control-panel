package com.vpsmanager.data.update

import com.vpsmanager.patchengine.ApkPatcher
import com.vpsmanager.patchengine.ExpectedApk
import com.vpsmanager.patchengine.PatchResult
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

    /** Out of space — the message already says how many MB. */
    FREE_SPACE,

    /**
     * The install failed for a reason only the system's own message explains.
     * That message is too long to fit in a banner; the whole text — and the
     * text of earlier attempts — is in Diagnostics.
     */
    SHOW_DIAGNOSTICS,

    /** "Install unknown apps" is off; there is a direct shortcut to the switch. */
    ALLOW_UNKNOWN_SOURCES,

    /** This device will not install by this route. The `/android/install` page is what is left. */
    USE_BROWSER,
}

/** The state the banner draws. */
sealed interface UpdateState {

    /** Nothing to show: no update, or the channel has not been published yet. */
    data object Idle : UpdateState

    data object Checking : UpdateState

    /**
     * There is a new version. [downloadBytes] is the REAL size of what will be
     * downloaded (the patch when one exists, otherwise the full artifact). On
     * the owner's connection that number is the most important thing on the
     * screen, so it is the size of the traffic and never of the rebuilt APK.
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

    /** Rebuilding the APK from the patch. Seconds, and no network. */
    data class Applying(val versionName: String) : UpdateState

    data class Installing(val versionName: String) : UpdateState

    data class Failed(
        val message: String,
        val canRetry: Boolean,
        val recovery: UpdateRecovery = UpdateRecovery.NONE,
    ) : UpdateState

    /**
     * "I just looked, and there is nothing new."
     *
     * This exists only because a check that was ASKED FOR has to answer. The
     * automatic check is silent on purpose — a "nothing new" notice on every
     * launch is pure noise. But whoever taps "Check for updates" and sees
     * nothing happen concludes, correctly, that the button is broken. Silence
     * is only honest when nobody asked.
     *
     * It clears itself: it is the answer to a question, not a system state.
     */
    data class UpToDate(val versionName: String) : UpdateState

    /**
     * The check that was ASKED FOR could not reach the server.
     *
     * This has no counterpart on the automatic path either, for the same reason
     * inverted: there a network failure is swallowed because there is nothing
     * to be done with it; here it is the answer to the question just asked.
     */
    data class CheckFailed(val message: String) : UpdateState
}

/** A testable boundary over `PackageInstaller`, which does not exist on a host JVM. */
interface ApkInstallerPort {
    fun canInstallFromUnknownSources(): Boolean

    /**
     * False when Android does not know who installed this app — the case for
     * every hand-downloaded APK. See `ApkInstaller.installSourceKnown`.
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
 * Drives the update from beginning to end and NEVER gets stuck.
 *
 * ### The fallback ladder
 * Each rung fails in its own way and calls for its own reaction — that is what
 * justifies typed results instead of a blanket `try/catch`:
 *
 * | situation | what happens |
 * |---|---|
 * | unknown base (dev build, version outside the window) | the server sends `patch: null`; take the full artifact (10 MB, not 31) |
 * | the `.hdiff` arrived corrupt | download it again ONCE; then drop to the full artifact |
 * | **rebuilt APK with a different hash** | discard it, drop to the full artifact and record the base hash in diagnostics |
 * | `hpatchz` error | drop to the full artifact |
 * | not enough space | say how many MB are missing and do not start |
 * | download interrupted | the partial file stays on disk; the next attempt resumes it by Range |
 * | install fails | the APK is kept for a retry; the system's message goes to Diagnostics |
 * | unknown sources denied | direct shortcut to the switch |
 * | device blocks sideloading | say so plainly and point at `/android/install` |
 *
 * ### The inviolable rule
 * An APK whose SHA-256 has not been checked NEVER reaches the installer.
 * `hpatchz` applied over the wrong base returns SUCCESS and writes a complete,
 * wrong file — its exit code is evidence of nothing. The checking is done by
 * `:patch-engine`, and [PatchResult.Applied] is the only outcome this code
 * accepts as "ready to install".
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
    // Applying the patch BLOCKS (file I/O plus decompression, seconds for a
    // 30 MB APK). Injectable so a test can await it deterministically — a
    // hard-wired `Dispatchers.IO` would let `advanceUntilIdle` return before
    // the rebuild had finished.
    private val ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
) {

    private val _state = MutableStateFlow<UpdateState>(UpdateState.Idle)
    val state: StateFlow<UpdateState> = _state.asStateFlow()

    @Volatile
    private var manifest: UpdateManifest? = null

    @Volatile
    private var runningJob: Job? = null

    /**
     * Fetches the manifest. Cheap: it is small JSON, and NOTHING is downloaded
     * here.
     *
     * Network errors are silent on purpose. A "could not check for updates"
     * banner is pure noise on a bad connection: it shows up constantly and
     * there is nothing to do about it. Only actionable things become banners.
     */
    suspend fun check() {
        // Never on top of a download or install in flight: the manifest would
        // change underneath what is already being downloaded.
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
     * The check the OWNER asks for, which also installs whatever it finds.
     *
     * ## How it differs from [check]
     *
     * [check] is background routine: a network error becomes silence, "nothing
     * new" becomes silence, and finding a new version merely LIGHTS UP the
     * banner and waits for a second tap. That is right for something that runs
     * by itself on every launch.
     *
     * Here all three invert, because an explicit question was asked: the
     * failure is stated, the "nothing new" is stated, and finding a new version
     * STARTS the update right away — the tap on the button was already the
     * authorisation, and demanding a second tap on a banner that sits behind
     * the drawer would hand back exactly the work the owner asked not to do.
     *
     * Android's own confirmation stays where it always was: nothing is
     * installed without the system dialog. What this path removes is the wait,
     * not the consent.
     */
    suspend fun checkAndUpdate() {
        // An update already under way is not restarted: tapping the button
        // again while 7 MB are coming down would throw away what has arrived.
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
     * An answer to a question has a moment to leave the screen; a system state
     * does not. Only [UpdateState.UpToDate] and [UpdateState.CheckFailed]
     * come through here — and only while they are still the current state, so
     * a download started in the meantime is not wiped.
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
                // Safety net: an UNEXPECTED exception — not one of the ones
                // the typed results cover — would escape to the app's
                // CoroutineExceptionHandler and leave the banner stuck on an
                // "Installing…" that never ends, saying nothing and allowing
                // no retry. It really happened: a `commit` on an
                // already-destroyed session threw IllegalStateException. That
                // catch was fixed, and this one is here for the next case
                // nobody saw coming.
                recordDiagnostic("Update: unexpected failure — ${e.javaClass.simpleName}: ${e.message}")
                _state.value = UpdateState.Failed(
                    "The update stopped with an unexpected error. Diagnostics has the details.",
                    canRetry = true,
                    recovery = UpdateRecovery.SHOW_DIAGNOSTICS,
                )
            }
        }
    }

    /**
     * Cancels whatever is in flight. The partial `.hdiff` STAYS on disk — it is
     * precisely what the next attempt resumes from.
     */
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

        // WITH a known install origin: a declared session, and the question
        // asked before the download. WITHOUT one: an ordinary session, the
        // question at the end — Android refuses the declared session with
        // "Self update is blocked by unknown source package", which was the
        // loop the owner's device got stuck in.
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
            // The label goes through UNTOUCHED, with no version appended: the
            // system compares it with the app's own label and destroys the
            // session if they differ. See ApkInstaller.requestPreapproval.
            // Without `setAppPackageName` there is no pre-approval to be had:
            // it requires the declared session. It is not even attempted —
            // asking and failing would destroy the session and force another
            // one to be created, for nothing.
            if (!canDeclareSelf) {
                recordDiagnostic(
                    "Update: this app was installed outside an app store, so Android does not " +
                        "allow confirming before the download. The confirmation appears at the end.",
                )
            }
            // PRE-APPROVAL REMOVED — not merely switched off.
            //
            // It existed to ask BEFORE the download, sparing 10 MB to whoever
            // would decline at the end. That was worth it only while there was
            // a question at the end to bring forward.
            //
            // Now there is none: the declared session asks to install WITHOUT
            // user action, and the two are mutually exclusive — the session
            // said "ask nothing" and, on the next line, asked for approval. The
            // system abandoned the session, and the symptom on the owner's
            // device was exact: `INSTALL_FAILED_ABORTED: Session was
            // abandoned`, appearing only once the install origin became known —
            // which is when the two were switched on together.
            //
            // And on the other half (unknown origin) pre-approval was already
            // impossible: it REQUIRES the declared session. So there is no path
            // left on which it applies — hence it goes, rather than sitting
            // behind a condition that is never true.
            //
            // The cost accepted: if the system REFUSES the silent install, the
            // question appears at the end, after the download. Downloading
            // 1.5 MB more in the rare case beats abandoning the session in the
            // common one.

            val candidates = listOfNotNull(current.patch, current.full)
            val apkTarget = staging.rebuiltApkFile(current.latest.apkSha256)
            val expected = ExpectedApk(sha256 = current.latest.apkSha256, sizeBytes = current.latest.apkSizeBytes)

            // An intact APK from a previous INSTALL attempt is not downloaded
            // again: fetching and rebuilding 10 MB to arrive at the same file
            // would waste exactly the resource that is scarce.
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
                        // The connection dropped: the full artifact does not
                        // fix that — it is SEVEN TIMES larger and would drop
                        // just the same. The partial file stayed on disk, and
                        // that is what the next attempt resumes from.
                        is DownloadAttempt.Abort -> return
                        // Corrupt bytes twice: this path is bad, the next rung
                        // may not be.
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
                            // The case that matters most: `hpatchz` said OK
                            // and produced the wrong bytes. The file has
                            // already been deleted by :patch-engine. Recording
                            // the base hash is what makes it possible to find
                            // out LATER why that device's patch did not fit.
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
                    // The system has taken over; this process dies next. The
                    // artifacts stay — the cleanup happens on the next boot,
                    // when the installed version is already known.
                }
                is InstallOutcome.Failed -> {
                    // LAST RUNG. The session was refused, but the APK is on
                    // disk with its hash checked: handing it to the standard
                    // install screen is exactly what happens when you tap a
                    // downloaded APK, and it works where the session does not.
                    // Without this, a refused session becomes an endless
                    // "try again" loop — which the owner lived through eight
                    // times.
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
                        // The rebuilt APK STAYS on disk: a retry repeats
                        // neither the download nor the rebuild, and on a bad
                        // connection that is the difference between a cheap
                        // retry and starting over.
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
     * Downloads [artifact], and if it arrives CORRUPT tries exactly one more
     * time. A second wrong hash is not bad luck in transmission: it is the
     * wrong path, and insisting on it only burns the owner's data.
     *
     * A dropped connection does not count as an attempt spent on another path —
     * the partial file stays on disk and resuming is what fixes it.
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
     * The base that `hpatchz` runs against.
     *
     * The "full" artifact is a `.hdiff` too — `hdiffz` against an EMPTY base.
     * That is why the device has a single code path: the base file changes, not
     * the algorithm.
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
         * How long the answer to a requested check stays on screen.
         *
         * 6 s: above the time it takes to read one line after closing the
         * drawer (the banner sits behind it, so reading only starts once the
         * drawer closes), and below the point where a motionless notice becomes
         * part of the scenery and stops being read.
         */
        const val RESPONSE_DURATION_MS = 6_000L

        // Same unit as formatDownloadSize — the owner must not see "1.4 MB" in
        // the banner and a figure in another base in the out-of-space message.
        fun megabytes(bytes: Long): String = String.format(PT_BR, "%.1f", bytes / 1_000_000.0)
    }
}

/** Fixed pt-BR: this app's interface is in Portuguese, and "1,4 MB" takes a comma. */
private val PT_BR: Locale = Locale.forLanguageTag("pt-BR")

/**
 * The download size as it appears in the banner.
 *
 * One decimal place and never more: "1,4 MB" is the information; "1,40 MB" and
 * "1400329 B" are noise. Below 1 MB it uses KB, because "0,1 MB" says nothing.
 *
 * ### Decimal MB (10^6), not MiB (2^20)
 * Not sloppiness: it is the same unit Android itself has used for file sizes in
 * its interface since Android 6, and the same one the incremental-update notes
 * use for the measured figures (1 400 329 B = "1,40 MB"). In MiB the same patch
 * would read "1,3 MB" — and the owner would see a number different from the one
 * written in the documentation and from what the device's own Settings show for
 * the same file.
 */
fun formatDownloadSize(bytes: Long): String = when {
    bytes < 1_000 -> "$bytes B"
    bytes < 1_000_000 -> String.format(PT_BR, "%d KB", bytes / 1_000)
    else -> String.format(PT_BR, "%.1f MB", bytes / 1_000_000.0)
}

/**
 * The outcome of one attempt at downloading ONE candidate from the ladder.
 *
 * The two failure modes demand opposite reactions, and collapsing them into a
 * `null` was exactly the defect this type exists to prevent: a dropped
 * connection is NOT a reason to drop to the full artifact (which is seven times
 * larger and would drop just the same — what fixes it is resuming the partial
 * file left on disk), whereas corrupt bytes twice in a row ARE.
 */
private sealed interface DownloadAttempt {
    data class Ready(val file: File) : DownloadAttempt

    /** Stop everything. The screen already explains, and the partial file is on disk to resume from. */
    data object Abort : DownloadAttempt

    /** Step down one rung of the ladder. */
    data object NextCandidate : DownloadAttempt
}

/**
 * The pure decision: does this manifest become a banner or not?
 *
 * Outside the class and free of dependencies so a plain test can pin it down.
 * The `versionCode` test is NOT redundant with `up_to_date`: the server decides
 * `up_to_date` from the base hash, so a development build (unknown hash, higher
 * `versionCode`) arrives here as "not current" and would be offered an OLDER
 * version to install — which Android refuses with
 * `INSTALL_FAILED_VERSION_DOWNGRADE`, after spending the whole download.
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
