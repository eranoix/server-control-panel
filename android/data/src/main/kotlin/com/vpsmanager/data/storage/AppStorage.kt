package com.vpsmanager.data.storage

import java.io.File

/**
 * What the app occupies on the device and what can be given back. Each store
 * declares its nature, which decides what maintenance may do:
 *
 * - Rebuildable and self-bounded (HTTP cache, media): has its own ceiling;
 *   maintenance only measures it.
 * - Rebuildable and unbounded (attachment and transfer temporaries): swept by age.
 * - In transit (a downloaded, not yet installed update): only leftovers from past
 *   versions are swept.
 * - Irreplaceable (preferences, credentials, the offline write queue): never touched.
 *
 * Pure filesystem with no `Context`, so the policy can be tested on the JVM.
 */
object AppStorage {

    /**
     * Age at which a temporary becomes junk: three days. These are local copies of
     * uploads, and an upload not finished in three days is done or abandoned;
     * shorter could delete an upload paused by a trip without network.
     */
    const val DAYS_UNTIL_TEMP_IS_JUNK = 3L

    private const val ONE_DAY_MS = 24L * 60 * 60 * 1000

    /** The store's nature, which decides what maintenance may do. */
    enum class Kind {
        /** It has its own ceiling and prunes itself. Maintenance only measures. */
        AUTO_BOUNDED,

        /** It grows forever if nobody sweeps. Maintenance sweeps by age. */
        TEMPORARY,

        /** Only what is left over from past versions is rubbish. */
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

    /** Recursive size of a directory. A missing directory is 0, never an error. */
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
            // "Clearable" is about the button, not the routine: the user may wipe
            // even a self-bounded store when out of space.
            clearable = true,
        )
    }

    /**
     * The routine sweep; only touches what each store's nature allows.
     *
     * @param nowMs injectable clock, so the age rule can be tested.
     * @param inUse files an operation in flight still needs. Never removed.
     */
    fun maintenance(
        areas: List<StorageArea>,
        nowMs: Long,
        inUse: Set<File> = emptySet(),
    ): CleanupResult {
        var freed = 0L
        var removed = 0
        val cutoff = nowMs - DAYS_UNTIL_TEMP_IS_JUNK * ONE_DAY_MS

        for (area in areas) {
            // AUTO_BOUNDED is deliberately skipped (see the class KDoc).
            if (area.kind == Kind.AUTO_BOUNDED) continue
            if (!area.dir.isDirectory) continue

            for (file in area.dir.walkBottomUp()) {
                if (!file.isFile) continue
                if (file in inUse) continue
                val old = file.lastModified() in 1 until cutoff
                val junk = when (area.kind) {
                    Kind.TEMPORARY -> old
                    // In transit: anything not in `inUse` is a leftover from an
                    // installed version and goes on the first pass.
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

    /** "12.4 MB" for the screen, base 1000 like Android's own screens. */
    fun formatBytes(bytes: Long): String = when {
        bytes < 1_000 -> "$bytes B"
        bytes < 1_000_000 -> String.format("%.1f kB", bytes / 1_000.0)
        bytes < 1_000_000_000 -> String.format("%.1f MB", bytes / 1_000_000.0)
        else -> String.format("%.2f GB", bytes / 1_000_000_000.0)
    }
}
