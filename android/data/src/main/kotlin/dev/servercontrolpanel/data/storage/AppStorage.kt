package dev.servercontrolpanel.data.storage

import java.io.File

object AppStorage {

    const val DAYS_UNTIL_TEMP_IS_JUNK = 3L

    private const val ONE_DAY_MS = 24L * 60 * 60 * 1000

    enum class Kind {
        AUTO_BOUNDED,

        TEMPORARY,

        IN_TRANSIT,
    }

    data class StorageArea(
        val name: String,
        val explanation: String,
        val dir: File,
        val kind: Kind,
    )

    data class Usage(val name: String, val explanation: String, val bytes: Long, val clearable: Boolean)

    data class CleanupResult(val freedBytes: Long, val filesRemoved: Int)

    fun size(dir: File): Long {
        if (!dir.exists()) return 0
        if (dir.isFile) return dir.length()
        return dir.walkBottomUp().filter { it.isFile }.sumOf { it.length() }
    }

    fun measure(areas: List<StorageArea>): List<Usage> = areas.map {
        Usage(
            name = it.name,
            explanation = it.explanation,
            bytes = size(it.dir),
            clearable = true,
        )
    }

    fun maintenance(
        areas: List<StorageArea>,
        nowMs: Long,
        inUse: Set<File> = emptySet(),
    ): CleanupResult {
        var freed = 0L
        var removed = 0
        val cutoff = nowMs - DAYS_UNTIL_TEMP_IS_JUNK * ONE_DAY_MS

        for (area in areas) {
            if (area.kind == Kind.AUTO_BOUNDED) continue
            if (!area.dir.isDirectory) continue

            for (file in area.dir.walkBottomUp()) {
                if (!file.isFile) continue
                if (file in inUse) continue
                val old = file.lastModified() in 1 until cutoff
                val junk = when (area.kind) {
                    Kind.TEMPORARY -> old
                    Kind.IN_TRANSIT -> true
                    Kind.AUTO_BOUNDED -> false
                }
                if (!junk) continue
                val size = file.length()
                if (file.delete()) {
                    freed += size
                    removed++
                }
            }
        }
        return CleanupResult(freedBytes = freed, filesRemoved = removed)
    }

    fun formatBytes(bytes: Long): String = when {
        bytes < 1_000 -> "$bytes B"
        bytes < 1_000_000 -> String.format("%.1f kB", bytes / 1_000.0)
        bytes < 1_000_000_000 -> String.format("%.1f MB", bytes / 1_000_000.0)
        else -> String.format("%.2f GB", bytes / 1_000_000_000.0)
    }
}
