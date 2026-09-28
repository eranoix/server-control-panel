package dev.servercontrolpanel.data.files

import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientError
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ResponseType
import dev.servercontrolpanel.mobileapiclient.infrastructure.Serializer
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.infrastructure.Success
import dev.servercontrolpanel.mobileapiclient.model.UploadCompleteRequest
import dev.servercontrolpanel.mobileapiclient.model.UploadCompleteResponse
import dev.servercontrolpanel.mobileapiclient.model.UploadIncompleteResponse
import dev.servercontrolpanel.mobileapiclient.model.UploadInitRequest
import java.io.File
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOn
import kotlinx.serialization.SerializationException
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Request

sealed interface DownloadChunkResult {
    data class Bytes(val data: ByteArray, val isLast: Boolean) : DownloadChunkResult
    data class Error(val reason: String) : DownloadChunkResult
}

sealed interface UploadSessionResult {
    data class Started(val sessionId: String) : UploadSessionResult
    data class Error(val reason: String, val retryable: Boolean = true) : UploadSessionResult
}

sealed interface UploadChunkResult {
    data object Accepted : UploadChunkResult
    data class Rejected(val reason: String, val retryable: Boolean = true) : UploadChunkResult
}

sealed interface UploadCompleteResult {
    data class Success(val path: String) : UploadCompleteResult
    data class Incomplete(val receivedBytes: Long, val totalSize: Long) : UploadCompleteResult
    data class Error(val reason: String, val retryable: Boolean = true) : UploadCompleteResult
}

internal fun transferErrorFor(statusCode: Int): Pair<String, Boolean> = when (statusCode) {
    401, 403 -> "No permission to write to the destination folder on the server. Choose another folder." to false
    404 -> "The upload session no longer exists on the server. The upload starts over." to true
    413 -> "The file exceeds the 2 GB upload limit. Compress or split the file." to false
    507 -> "The server is out of disk space. Free up space and upload again." to false
    in 500..599 -> "The server is unavailable right now. The upload resumes when it is back." to true
    in 400..499 -> "The server refused the upload (error $statusCode)." to false
    else -> "Could not upload the file (error $statusCode)." to false
}

open class TransferRepository(
    private val mobileApi: MobileApi = MobileApi(),
    private val chunkStagingDir: File = File(System.getProperty("java.io.tmpdir") ?: "."),
) {

    open fun downloadRange(path: String, fromByte: Long): Flow<DownloadChunkResult> = flow {
        val requestConfig = mobileApi.downloadFileRequestConfig(path = path)
        val httpUrl = mobileApi.baseUrl.toHttpUrlOrNull()
        if (httpUrl == null) {
            emit(DownloadChunkResult.Error("Configuration error while downloading the file."))
            return@flow
        }
        val url = httpUrl.newBuilder()
            .addEncodedPathSegments(requestConfig.path.trimStart('/'))
            .apply {
                requestConfig.query.forEach { entry ->
                    entry.value.forEach { value -> addQueryParameter(entry.key, value) }
                }
            }
            .build()
        val requestBuilder = Request.Builder().url(url)
        if (fromByte > 0) {
            requestBuilder.header("Range", "bytes=$fromByte-")
        }
        try {
            mobileApi.client.newCall(requestBuilder.build()).execute().use { response ->
                if (!response.isSuccessful) {
                    emit(DownloadChunkResult.Error("Could not download the file (error ${response.code})."))
                    return@flow
                }
                val body = response.body
                if (body == null) {
                    emit(DownloadChunkResult.Error("Empty response from the server."))
                    return@flow
                }
                body.byteStream().use { input ->
                    val buffer = ByteArray(DOWNLOAD_CHUNK_SIZE_BYTES)
                    while (true) {
                        val read = input.read(buffer)
                        if (read == -1) break
                        emit(DownloadChunkResult.Bytes(data = buffer.copyOf(read), isLast = false))
                    }
                }
                emit(DownloadChunkResult.Bytes(data = ByteArray(0), isLast = true))
            }
        } catch (e: IOException) {
            emit(DownloadChunkResult.Error("Connection failed. Check your network and try again."))
        }
    }.flowOn(Dispatchers.IO)

    open suspend fun startUpload(destDir: String, filename: String, totalSize: Long): UploadSessionResult = try {
        val response = mobileApi.initUpload(
            UploadInitRequest(destDir = destDir, filename = filename, totalSize = totalSize),
        )
        UploadSessionResult.Started(sessionId = response.sessionId)
    } catch (e: ClientException) {
        val (reason, retryable) = transferErrorFor(e.statusCode)
        UploadSessionResult.Error(reason, retryable = retryable)
    } catch (e: ServerException) {
        UploadSessionResult.Error("The server is unavailable right now.", retryable = true)
    } catch (e: IOException) {
        UploadSessionResult.Error("Connection failed. The upload resumes when the network is back.", retryable = true)
    } catch (e: IllegalStateException) {
        UploadSessionResult.Error("Configuration error while starting the upload.", retryable = false)
    } catch (e: UnsupportedOperationException) {
        UploadSessionResult.Error("Unexpected response from the server.", retryable = false)
    } catch (e: Exception) {
        UploadSessionResult.Error("Could not start the upload.", retryable = false)
    }

    open suspend fun uploadChunk(sessionId: String, offset: Long, data: ByteArray): UploadChunkResult {
        val stagedChunk = File.createTempFile("upload-chunk-", ".bin", chunkStagingDir)
        return try {
            stagedChunk.writeBytes(data)
            val response = mobileApi.uploadChunkWithHttpInfo(sessionId = sessionId, offset = offset, body = stagedChunk)
            if (response.responseType == ResponseType.Success) {
                UploadChunkResult.Accepted
            } else {
                val (reason, retryable) = transferErrorFor(response.statusCode)
                UploadChunkResult.Rejected(reason, retryable = retryable)
            }
        } catch (e: IOException) {
            UploadChunkResult.Rejected("Connection failed. The upload resumes when the network is back.", retryable = true)
        } catch (e: IllegalStateException) {
            UploadChunkResult.Rejected("Configuration error while uploading the file.", retryable = false)
        } catch (e: Exception) {
            UploadChunkResult.Rejected("Could not upload this part of the file.", retryable = true)
        } finally {
            stagedChunk.delete()
        }
    }

    open suspend fun completeUpload(sessionId: String): UploadCompleteResult = try {
        val response = mobileApi.completeUploadWithHttpInfo(UploadCompleteRequest(sessionId = sessionId))
        when (response.responseType) {
            ResponseType.Success -> {
                val body = (response as Success<*>).data as? UploadCompleteResponse
                if (body != null) {
                    UploadCompleteResult.Success(path = body.path)
                } else {
                    UploadCompleteResult.Error("Unexpected response from the server.", retryable = false)
                }
            }
            ResponseType.ClientError -> {
                val err = response as ClientError<*>
                if (err.statusCode == 409) {
                    parseIncomplete(err.body as? String)
                        ?: UploadCompleteResult.Error(
                            "The upload is incomplete, but its progress could not be determined.",
                            retryable = true,
                        )
                } else {
                    val (reason, retryable) = transferErrorFor(err.statusCode)
                    UploadCompleteResult.Error(reason, retryable = retryable)
                }
            }
            ResponseType.ServerError -> UploadCompleteResult.Error(
                "The server is unavailable right now. The upload resumes when it is back.",
                retryable = true,
            )
            else -> UploadCompleteResult.Error("Unexpected response from the server.", retryable = false)
        }
    } catch (e: IOException) {
        UploadCompleteResult.Error("Connection failed. The upload resumes when the network is back.", retryable = true)
    } catch (e: IllegalStateException) {
        UploadCompleteResult.Error("Configuration error while finishing the upload.", retryable = false)
    } catch (e: Exception) {
        UploadCompleteResult.Error("Could not finish the upload.", retryable = false)
    }
}

private const val DOWNLOAD_CHUNK_SIZE_BYTES = 64 * 1024

private fun parseIncomplete(rawBody: String?): UploadCompleteResult.Incomplete? {
    if (rawBody.isNullOrBlank()) return null
    return try {
        val body = Serializer.kotlinxSerializationJson.decodeFromString(UploadIncompleteResponse.serializer(), rawBody)
        UploadCompleteResult.Incomplete(receivedBytes = body.receivedBytes, totalSize = body.totalSize)
    } catch (e: SerializationException) {
        null
    }
}
