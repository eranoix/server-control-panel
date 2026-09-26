package com.vpsmanager.data.update

import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.mobileapiclient.api.MobileApi
import com.vpsmanager.mobileapiclient.infrastructure.ClientException
import com.vpsmanager.mobileapiclient.infrastructure.ServerException
import com.vpsmanager.patchengine.ApkPatcher
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.currentCoroutineContext
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Request

/** A `.hdiff` file to download. `sizeBytes`/`sha256` describe the ARTIFACT, not the APK it rebuilds. */
data class UpdateArtifact(val url: String, val sizeBytes: Long, val sha256: String)

/** The newest published version. `apkSha256`/`apkSizeBytes` describe the rebuilt SIGNED APK. */
data class UpdateRelease(
    val versionName: String,
    val versionCode: Long,
    val apkSha256: String,
    val apkSizeBytes: Long,
)

/**
 * The update manifest.
 *
 * [patch] is null whenever there is no patch for the exact base given —
 * unknown base, a version outside the retention window, or no base sent at all.
 * The server NEVER approximates. [full] is always present, including alongside
 * the patch, so that a failure to apply does not require a second trip to the
 * server.
 */
data class UpdateManifest(
    val latest: UpdateRelease,
    val upToDate: Boolean,
    val patch: UpdateArtifact?,
    val full: UpdateArtifact,
    val patchTool: String,
)

/** Desfecho de [UpdateSource.check]. */
sealed interface UpdateCheckResult {
    data class Success(val manifest: UpdateManifest) : UpdateCheckResult

    /**
     * 503: no release has been through the patch pipeline yet. It is SERVER
     * state, not a wrong resource — and therefore it does not become an "error"
     * on screen: the app simply has no incremental channel today, and keeps
     * quiet.
     */
    data object ChannelNotPublished : UpdateCheckResult

    data class Error(val reason: String) : UpdateCheckResult
}

/** Andamento de [UpdateSource.download]. */
sealed interface ArtifactDownloadProgress {
    data class Progress(val downloadedBytes: Long, val totalBytes: Long) : ArtifactDownloadProgress

    /** The file is complete AND its SHA-256 matches the manifest's. */
    data class Done(val file: File) : ArtifactDownloadProgress

    /**
     * [corrupt] separates "the bytes that arrived are wrong" (hash mismatch —
     * the file was DELETED, resuming will not help, it has to be downloaded
     * again) from "the connection dropped" (the partial file stayed on disk and
     * the next attempt resumes where it left off). Two different reactions on
     * the fallback ladder.
     */
    data class Failed(val reason: String, val corrupt: Boolean) : ArtifactDownloadProgress
}

/** The port the layers above see. It exists so ViewModel tests never touch the network. */
interface UpdateSource {
    suspend fun check(baseSha256: String?): UpdateCheckResult
    fun download(artifact: UpdateArtifact, target: File): Flow<ArtifactDownloadProgress>
}

/**
 * The single entry point into `GET /app/update` and `GET /app/update/artifact`.
 *
 * ### Why the base URL is re-derived on every call
 * Following [com.vpsmanager.data.auth.BffSessionRefresher]: the update check
 * runs long after boot (a periodic worker) and has to talk to the server
 * configured NOW, not the one that was in `System.getProperty` when the process
 * started.
 */
open class UpdateRepository(
    private val serverConfigRepository: ServerConfigRepository,
    private val mobileApiFactory: (String) -> MobileApi = { basePath -> MobileApi(basePath) },
    private val sha256Of: (File) -> String = ApkPatcher::sha256Of,
) : UpdateSource {

    override suspend fun check(baseSha256: String?): UpdateCheckResult {
        val api = api() ?: return UpdateCheckResult.Error("No server configured.")
        return try {
            val body = api.getAppUpdate(baseSha256 = baseSha256)
            UpdateCheckResult.Success(
                UpdateManifest(
                    latest = UpdateRelease(
                        versionName = body.latest.versionName,
                        versionCode = body.latest.versionCode,
                        apkSha256 = body.latest.sha256,
                        apkSizeBytes = body.latest.sizeBytes,
                    ),
                    upToDate = body.upToDate,
                    patch = body.patch?.toDomain(),
                    full = body.full.toDomain(),
                    patchTool = body.patchTool,
                ),
            )
        } catch (e: ClientException) {
            UpdateCheckResult.Error("Could not check for updates (error ${e.statusCode}).")
        } catch (e: ServerException) {
            if (e.statusCode == HTTP_SERVICE_UNAVAILABLE) {
                UpdateCheckResult.ChannelNotPublished
            } else {
                UpdateCheckResult.Error("The server is unavailable right now.")
            }
        } catch (e: IOException) {
            UpdateCheckResult.Error("Connection failed. Check your network and try again.")
        } catch (e: IllegalStateException) {
            UpdateCheckResult.Error("Configuration error while checking for updates.")
        } catch (e: UnsupportedOperationException) {
            UpdateCheckResult.Error("Unexpected response from the server.")
        } catch (e: Exception) {
            UpdateCheckResult.Error("Could not check for updates.")
        }
    }

    /**
     * Downloads [artifact] into [target], RESUMING where it left off.
     *
     * This is the point of the whole feature: the owner's connection is poor,
     * and a 10 MB download that restarts from zero on every drop never
     * finishes.
     *
     * ### The resume contract, and why `If-Range` is mandatory
     * `Range: bytes=N-` on its own is dangerous: if the file on the server had
     * changed between the two attempts, the bytes from N onward would belong to
     * DIFFERENT content, silently spliced onto what is already on disk.
     * `If-Range` with the strong ETag (which here IS the artifact's SHA-256)
     * closes that: if the target is not byte-for-byte the same, the server
     * answers 200 with the whole file instead of 206, and the code below
     * truncates and starts over.
     *
     * The three possible responses are handled explicitly, none assumed:
     * - **206** — resume. The offset comes from the SERVER's `Content-Range`,
     *   not from what we asked for, and the file is truncated at that offset
     *   before writing.
     * - **200** — the server ignored the Range (or the `If-Range` did not
     *   match): truncate to zero and write the whole body.
     * - **416** — the disk holds more bytes than the entire artifact (a file
     *   from an earlier version under the same name): delete it and ask for a
     *   fresh attempt.
     *
     * The result's SHA-256 is checked before [ArtifactDownloadProgress.Done].
     */
    override fun download(artifact: UpdateArtifact, target: File): Flow<ArtifactDownloadProgress> = flow {
        val api = api()
        if (api == null) {
            emit(ArtifactDownloadProgress.Failed("No server configured.", corrupt = false))
            return@flow
        }
        val url = resolveArtifactUrl(api.baseUrl, artifact.url)
        if (url == null) {
            emit(ArtifactDownloadProgress.Failed("Configuration error while downloading the update.", corrupt = false))
            return@flow
        }

        target.parentFile?.let { if (!it.isDirectory) it.mkdirs() }
        var onDisk = if (target.isFile) target.length() else 0L
        if (onDisk > artifact.sizeBytes) {
            // More on disk than the target: this is not a resume, it is debris from something else.
            target.delete()
            onDisk = 0L
        }
        if (onDisk == artifact.sizeBytes) {
            emit(verify(artifact, target))
            return@flow
        }

        val request = Request.Builder().url(url).apply {
            if (onDisk > 0) {
                header("Range", "bytes=$onDisk-")
                // The server's strong ETag: the quotes are part of the value.
                header("If-Range", "\"${artifact.sha256}\"")
            }
        }.build()

        try {
            api.client.newCall(request).execute().use { response ->
                val writeFrom = when {
                    response.code == HTTP_RANGE_NOT_SATISFIABLE -> {
                        target.delete()
                        emit(
                            ArtifactDownloadProgress.Failed(
                                "The downloaded file no longer matches the server's. Try again.",
                                corrupt = true,
                            ),
                        )
                        return@flow
                    }

                    response.code == HTTP_PARTIAL_CONTENT ->
                        contentRangeStart(response.header("Content-Range")) ?: onDisk

                    response.isSuccessful -> 0L

                    else -> {
                        emit(
                            ArtifactDownloadProgress.Failed(
                                "Could not download the update (error ${response.code}).",
                                corrupt = false,
                            ),
                        )
                        return@flow
                    }
                }

                val body = response.body
                if (body == null) {
                    emit(ArtifactDownloadProgress.Failed("Empty response from the server.", corrupt = false))
                    return@flow
                }

                var written = writeFrom
                var lastReported = writeFrom
                RandomAccessFile(target, "rw").use { out ->
                    // Truncate BEFORE writing: a 200 after a partial 206 has
                    // to erase the partial rather than write over it and leave
                    // an old tail at the end of the file.
                    out.setLength(writeFrom)
                    out.seek(writeFrom)
                    body.byteStream().use { input ->
                        val buffer = ByteArray(DOWNLOAD_CHUNK_SIZE_BYTES)
                        while (true) {
                            // The user cancelled: leave, keeping the partial
                            // on disk, which is exactly what the resume wants.
                            currentCoroutineContext().ensureActive()
                            val read = input.read(buffer)
                            if (read == -1) break
                            out.write(buffer, 0, read)
                            written += read
                            if (written - lastReported >= PROGRESS_STEP_BYTES) {
                                lastReported = written
                                emit(ArtifactDownloadProgress.Progress(written, artifact.sizeBytes))
                            }
                        }
                    }
                }
                emit(ArtifactDownloadProgress.Progress(written, artifact.sizeBytes))

                if (written != artifact.sizeBytes) {
                    // Connection cut mid-way: the partial STAYS, to resume from.
                    emit(
                        ArtifactDownloadProgress.Failed(
                            "Connection failed. Check your network and try again.",
                            corrupt = false,
                        ),
                    )
                    return@flow
                }
            }
        } catch (e: IOException) {
            emit(ArtifactDownloadProgress.Failed("Connection failed. Check your network and try again.", corrupt = false))
            return@flow
        }

        emit(verify(artifact, target))
    }.flowOn(Dispatchers.IO)

    /**
     * The barrier. A `.hdiff` with the wrong hash never becomes `hpatchz`
     * input — and since `hpatchz` can return SUCCESS over wrong input and write
     * a complete, wrong APK, letting one through here would mean relying on the
     * final APK check alone. Two barriers, not one.
     */
    private fun verify(artifact: UpdateArtifact, target: File): ArtifactDownloadProgress = try {
        val actual = sha256Of(target)
        if (actual.equals(artifact.sha256, ignoreCase = true)) {
            ArtifactDownloadProgress.Done(target)
        } else {
            target.delete()
            ArtifactDownloadProgress.Failed(
                "The downloaded file arrived corrupted (SHA-256 mismatch).",
                corrupt = true,
            )
        }
    } catch (e: IOException) {
        ArtifactDownloadProgress.Failed("Could not read the downloaded file: ${e.message}", corrupt = true)
    }

    private fun api(): MobileApi? {
        val baseUrl = serverConfigRepository.currentBaseUrl() ?: return null
        return mobileApiFactory("$baseUrl$API_PREFIX")
    }

    private companion object {
        const val API_PREFIX = "/api/mobile/v1"
        const val DOWNLOAD_CHUNK_SIZE_BYTES = 64 * 1024
        const val PROGRESS_STEP_BYTES = 128L * 1024
        const val HTTP_PARTIAL_CONTENT = 206
        const val HTTP_RANGE_NOT_SATISFIABLE = 416
        const val HTTP_SERVICE_UNAVAILABLE = 503
    }
}

private fun com.vpsmanager.mobileapiclient.model.AppUpdateArtifact.toDomain() =
    UpdateArtifact(url = url, sizeBytes = sizeBytes, sha256 = sha256)

/**
 * Resolves the path the manifest published against the client's base, and
 * REFUSES a result that changes host.
 *
 * The `url` field comes off the network. It is from our own authenticated
 * server, but blindly following a URL that arrived in a response is how an open
 * redirect becomes token exfiltration: this stack's `Authorization` interceptor
 * would attach the Bearer to whatever host showed up there. Pinning the host to
 * the configured one costs three lines and closes the whole class of bug.
 */
internal fun resolveArtifactUrl(baseUrl: String, artifactUrl: String): HttpUrl? {
    val base = baseUrl.toHttpUrlOrNull() ?: return null
    val resolved = base.resolve(artifactUrl) ?: return null
    if (resolved.host != base.host || resolved.port != base.port || resolved.scheme != base.scheme) return null
    return resolved
}

/** Extracts the start offset from a `Content-Range: bytes <start>-<end>/<total>`. */
internal fun contentRangeStart(header: String?): Long? {
    val value = header?.trim() ?: return null
    if (!value.startsWith("bytes ")) return null
    val range = value.removePrefix("bytes ").substringBefore('/')
    return range.substringBefore('-').trim().toLongOrNull()
}
