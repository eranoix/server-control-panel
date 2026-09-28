package dev.servercontrolpanel.data.update

import dev.servercontrolpanel.data.config.ServerConfigRepository
import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.patchengine.ApkPatcher
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

data class UpdateArtifact(val url: String, val sizeBytes: Long, val sha256: String)

data class UpdateRelease(
    val versionName: String,
    val versionCode: Long,
    val apkSha256: String,
    val apkSizeBytes: Long,
)

data class UpdateManifest(
    val latest: UpdateRelease,
    val upToDate: Boolean,
    val patch: UpdateArtifact?,
    val full: UpdateArtifact,
    val patchTool: String,
)

sealed interface UpdateCheckResult {
    data class Success(val manifest: UpdateManifest) : UpdateCheckResult

    data object ChannelNotPublished : UpdateCheckResult

    data class Error(val reason: String) : UpdateCheckResult
}

sealed interface ArtifactDownloadProgress {
    data class Progress(val downloadedBytes: Long, val totalBytes: Long) : ArtifactDownloadProgress

    data class Done(val file: File) : ArtifactDownloadProgress

    data class Failed(val reason: String, val corrupt: Boolean) : ArtifactDownloadProgress
}

interface UpdateSource {
    suspend fun check(baseSha256: String?): UpdateCheckResult
    fun download(artifact: UpdateArtifact, target: File): Flow<ArtifactDownloadProgress>
}

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
                    out.setLength(writeFrom)
                    out.seek(writeFrom)
                    body.byteStream().use { input ->
                        val buffer = ByteArray(DOWNLOAD_CHUNK_SIZE_BYTES)
                        while (true) {
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

private fun dev.servercontrolpanel.mobileapiclient.model.AppUpdateArtifact.toDomain() =
    UpdateArtifact(url = url, sizeBytes = sizeBytes, sha256 = sha256)

internal fun resolveArtifactUrl(baseUrl: String, artifactUrl: String): HttpUrl? {
    val base = baseUrl.toHttpUrlOrNull() ?: return null
    val resolved = base.resolve(artifactUrl) ?: return null
    if (resolved.host != base.host || resolved.port != base.port || resolved.scheme != base.scheme) return null
    return resolved
}

internal fun contentRangeStart(header: String?): Long? {
    val value = header?.trim() ?: return null
    if (!value.startsWith("bytes ")) return null
    val range = value.removePrefix("bytes ").substringBefore('/')
    return range.substringBefore('-').trim().toLongOrNull()
}
