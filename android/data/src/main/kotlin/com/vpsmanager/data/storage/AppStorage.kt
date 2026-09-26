package com.vpsmanager.data.storage

import java.io.File

/**
 * What the app occupies on the device, and what can be given back.
 *
 * ## Why this exists
 *
 * The owner asked for maintenance done "properly and automatically", and he
 * asked after a night when the terminal only came right once he had wiped the
 * app's storage by hand. That gesture is the symptom of an absence: when the
 * only maintenance tool is the operating system's button, any defect becomes
 * "wipe everything and hope", and the preferences, the configured server and
 * the session go out with the rubbish.
 *
 * ## The principle: each store declares its own policy
 *
 * There is no such thing as "clear the app". There are stores of different
 * natures, and treating them all alike is what makes a cleanup dangerous:
 *
 * - **Rebuildable and self-bounded** (the HTTP cache, media): it has its own
 *   ceiling and prunes itself. Maintenance need not touch it — only measure.
 *   Deleting it unprompted costs network on the next launch and gives back no
 *   space that was not already bounded.
 * - **Rebuildable and UNBOUNDED** (attachment and transfer temporaries): they
 *   grow forever if nobody sweeps. This is where maintenance earns its keep.
 * - **In transit** (an update downloaded and not yet installed): it is NOT
 *   rubbish until the install happens — deleting it undoes a download the
 *   person has already paid for. Only what is left over from past versions
 *   enters the sweep.
 * - **Irreplaceable** (preferences, credentials, the offline write queue):
 *   maintenance NEVER touches it. What is lost here does not come back with a
 *   network.
 *
 * This class is pure filesystem, with no `Context`, so it can be exercised on
 * the JVM — the policy is the part that needs testing, and it depends on no
 * Android at all.
 */
object AppStorage {

    /**
     * The age at which a temporary becomes rubbish.
     *
     * Three days, and the number comes from what these files are: local copies
     * of something being uploaded. An upload that has not finished in three
     * days is not going to — either the file already went up, or the person
     * gave up. Shorter than that would risk deleting an upload paused by a trip
     * without a network.
     */
    const val DAYS_UNTIL_TEMP_IS_JUNK = 3L

    private const val ONE_DAY_MS = 24L * 60 * 60 * 1000

    /** The store's nature — it is what decides what maintenance may do. */
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

    /** The recursive sum of what a directory occupies. A missing directory = 0, never an error. */
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
            // "Can be cleared" is about the BUTTON, not about the routine: a
            // self-bounded store needs no sweeping, but whoever is out of space
            // today has the right to say wipe it anyway.
            clearable = true,
        )
    }

    /**
     * The routine. It only touches what the store's nature authorises.
     *
     * @param nowMs an injectable clock — the age rule is the thing to test.
     * @param inUse files an operation in flight still needs (the update being
     *   downloaded). Never removed.
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
            // AUTO_BOUNDED is deliberately left out — see the class KDoc.
            if (area.kind == Kind.AUTO_BOUNDED) continue
            if (!area.dir.isDirectory) continue

            for (file in area.dir.walkBottomUp()) {
                if (!file.isFile) continue
                if (file in inUse) continue
                val old = file.lastModified() in 1 until cutoff
                val junk = when (area.kind) {
                    Kind.TEMPORARY -> old
                    // In transit: the criterion is not age, it is NOT BEING IN
                    // USE. An artifact from an already-installed version does
                    // not appear in `inUse` and goes on the first pass, without
                    // waiting three days while occupying tens of MB.
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

    /** "12.4 MB" — for the screen. Base 1000, which is what Android uses on its own screens. */
    fun formatBytes(bytes: Long): String = when {
        bytes < 1_000 -> "$bytes B"
        bytes < 1_000_000 -> String.format("%.1f kB", bytes / 1_000.0)
        bytes < 1_000_000_000 -> String.format("%.1f MB", bytes / 1_000_000.0)
        else -> String.format("%.2f GB", bytes / 1_000_000_000.0)
    }
}
