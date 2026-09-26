package com.vpsmanager.core.model

/**
 * A single entry (file or directory) inside a server directory listing.
 * Pure domain shape -- the generated OpenAPI client's `FileEntry` DTO never
 * crosses the `:data` boundary; [com.vpsmanager.data.files.FilesRepository]
 * maps one into the other.
 */
data class FileEntry(
    val name: String,
    val size: Long,
    val isDir: Boolean,
    val modifiedEpochSeconds: Long,
)
