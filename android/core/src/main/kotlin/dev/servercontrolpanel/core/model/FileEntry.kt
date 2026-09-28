package dev.servercontrolpanel.core.model

data class FileEntry(
    val name: String,
    val size: Long,
    val isDir: Boolean,
    val modifiedEpochSeconds: Long,
)
