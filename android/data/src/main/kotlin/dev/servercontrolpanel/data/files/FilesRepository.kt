package dev.servercontrolpanel.data.files

import dev.servercontrolpanel.core.model.FileEntry
import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientError
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ResponseType
import dev.servercontrolpanel.mobileapiclient.infrastructure.Serializer
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.infrastructure.Success
import dev.servercontrolpanel.mobileapiclient.model.FileConflictResponse
import dev.servercontrolpanel.mobileapiclient.model.FileWriteRequest
import dev.servercontrolpanel.mobileapiclient.model.FileWriteResponse
import java.io.IOException
import kotlinx.serialization.SerializationException
import dev.servercontrolpanel.mobileapiclient.model.FileEntry as GeneratedFileEntry

sealed interface FileListResult {
    data class Success(val path: String, val parent: String?, val entries: List<FileEntry>) : FileListResult
    data object Empty : FileListResult
    data class Error(val reason: String) : FileListResult
}

sealed interface FileReadResult {
    data class Success(val content: String, val mtime: Long, val language: String) : FileReadResult
    data class Error(val reason: String) : FileReadResult
}

sealed interface FileWriteResult {
    data class Success(val mtime: Long) : FileWriteResult
    data class Conflict(val serverContent: String, val serverMtime: Long) : FileWriteResult
    data class Error(val reason: String) : FileWriteResult
}

sealed interface InboxDirResult {
    data class Success(val path: String) : InboxDirResult
    data class Error(val reason: String) : InboxDirResult
}

open class FilesRepository(
    private val mobileApi: MobileApi = MobileApi(),
) {
    open suspend fun list(path: String): FileListResult = try {
        val response = mobileApi.listFiles(path = path)
        val entries = response.propertyEntries.orEmpty().map { it.toDomain() }
        if (entries.isEmpty()) {
            FileListResult.Empty
        } else {
            FileListResult.Success(path = response.path, parent = response.parent, entries = entries)
        }
    } catch (e: ClientException) {
        FileListResult.Error("Could not list the directory (error ${e.statusCode}).")
    } catch (e: ServerException) {
        FileListResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        FileListResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        FileListResult.Error("Configuration error while listing the directory.")
    } catch (e: UnsupportedOperationException) {
        FileListResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        FileListResult.Error("Could not list the directory.")
    }

    open suspend fun read(path: String): FileReadResult = try {
        val response = mobileApi.readFile(path = path)
        FileReadResult.Success(content = response.content, mtime = response.mtime, language = response.language)
    } catch (e: ClientException) {
        FileReadResult.Error(
            when (e.statusCode) {
                413 -> "File too large to open on the phone (2 MB limit)."
                415 -> "This file is not text -- it cannot be shown in the editor."
                404 -> "File not found."
                else -> "Could not open the file (error ${e.statusCode})."
            },
        )
    } catch (e: ServerException) {
        FileReadResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        FileReadResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        FileReadResult.Error("Configuration error while opening the file.")
    } catch (e: UnsupportedOperationException) {
        FileReadResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        FileReadResult.Error("Could not open the file.")
    }

    open suspend fun write(path: String, content: String, expectedMtime: Long): FileWriteResult = try {
        val request = FileWriteRequest(
            path = path,
            content = content,
            expectedMtime = expectedMtime.takeIf { it != 0L },
        )
        val response = mobileApi.writeFileWithHttpInfo(fileWriteRequest = request)
        when (response.responseType) {
            ResponseType.Success -> {
                val body = (response as Success<*>).data as? FileWriteResponse
                FileWriteResult.Success(mtime = body?.mtime ?: expectedMtime)
            }
            ResponseType.ClientError -> {
                val err = response as ClientError<*>
                if (err.statusCode == 409) {
                    parseConflict(err.body as? String)
                        ?: FileWriteResult.Error(
                            "The file changed on the server, but its current content could not be read.",
                        )
                } else {
                    FileWriteResult.Error("Could not save the file (error ${err.statusCode}).")
                }
            }
            ResponseType.ServerError -> FileWriteResult.Error("The server is unavailable right now.")
            else -> FileWriteResult.Error("Unexpected response from the server.")
        }
    } catch (e: IOException) {
        FileWriteResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        FileWriteResult.Error("Configuration error while saving the file.")
    } catch (e: Exception) {
        FileWriteResult.Error("Could not save the file.")
    }

    open suspend fun inboxPath(): InboxDirResult = try {
        InboxDirResult.Success(path = mobileApi.filesInbox().path)
    } catch (e: ClientException) {
        InboxDirResult.Error("Could not get the default folder (error ${e.statusCode}).")
    } catch (e: ServerException) {
        InboxDirResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        InboxDirResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        InboxDirResult.Error("Configuration error while getting the default folder.")
    } catch (e: UnsupportedOperationException) {
        InboxDirResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        InboxDirResult.Error("Could not get the default folder.")
    }
}

private fun parseConflict(rawBody: String?): FileWriteResult.Conflict? {
    if (rawBody.isNullOrBlank()) return null
    return try {
        val body = Serializer.kotlinxSerializationJson.decodeFromString(FileConflictResponse.serializer(), rawBody)
        FileWriteResult.Conflict(serverContent = body.serverContent, serverMtime = body.serverMtime)
    } catch (e: SerializationException) {
        null
    }
}

private fun GeneratedFileEntry.toDomain() = FileEntry(
    name = name,
    size = propertySize,
    isDir = isDir,
    modifiedEpochSeconds = modified,
)
