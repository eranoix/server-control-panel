package com.vpsmanager.patchengine

import java.io.File

/**
 * Outcome of [ApkPatcher.apply]. A typed result, and not a bare exception, for
 * the same reason as `LoginResult` (:data) and `SduiScreenResult` (:core):
 * applying a patch fails in several *expected* ways (corrupt patch, wrong
 * base, full disk) and each one calls for a different reaction on screen —
 * "download it again", "download the whole APK", "free up space". A generic
 * `try/catch` would collapse them all into "something went wrong".
 *
 * The hierarchy is sealed on purpose: whoever consumes it is forced by the
 * compiler to decide what to do when a new case shows up.
 */
sealed interface PatchResult {

    /**
     * Patch applied AND checked: [newFile] exists and its SHA-256 is exactly
     * [sha256], the value the caller declared it expected. This is the ONLY
     * state in which the file may be handed to the installer.
     */
    data class Applied(val newFile: File, val sha256: String) : PatchResult

    /** One of the input files does not exist, is not a regular file, or is not readable. */
    data class InputMissing(val role: InputRole, val path: String) : PatchResult

    /**
     * The destination volume has no room for the rebuilt APK. Note that
     * patching is not an "in-place" update: the old file and the new one exist
     * on disk at the same time.
     *
     * [requiredBytes] is the declared size of the new file; [usableBytes] is
     * what the system says is available to this process on that volume.
     */
    data class InsufficientStorage(
        val requiredBytes: Long,
        val usableBytes: Long,
    ) : PatchResult

    /**
     * `libhpatchz.so` could not be loaded. In practice it means an APK
     * installed without this device's ABI — unrecoverable at runtime; the way
     * out is downloading the full APK.
     */
    data class EngineUnavailable(val detail: String) : PatchResult

    /**
     * Native `hpatchz` returned a non-zero code. [code] is the readable
     * translation; [rawCode] is the raw integer, preserved because the enum
     * may not cover a future upstream version and the number is what you can
     * search for in its source.
     */
    data class NativeFailure(val code: HPatchCode, val rawCode: Int) : PatchResult

    /**
     * The native side said "ok" but the bytes produced do not match what was
     * expected.
     *
     * This is the case that justifies verification living INSIDE this module: a
     * patch applied over the wrong base can finish successfully and produce a
     * plausible, wrong APK. Finding that out at the installer is too late —
     * there the error turns into "app not installed", without saying why. The
     * file produced is deleted before this result is returned.
     */
    data class IntegrityMismatch(
        val expectedSha256: String,
        val actualSha256: String,
    ) : PatchResult

    /** The output path could not be created or opened for writing. */
    data class OutputUnwritable(val path: String, val detail: String) : PatchResult

    /** Which of the two input files was missing. */
    enum class InputRole { BASE_APK, PATCH }
}

/**
 * Translation of upstream's `THPatchResult` (`hpatchz.c`, commit pinned in
 * `toolchain.properties`). The numbers are NOT sequential there — there are
 * deliberate jumps at 20 and at 103 — so the translation is by explicit value,
 * never by `ordinal`.
 *
 * Only the codes reachable by THIS build are named: the VCDIFF, bsdiff,
 * directory-patch and SFX ones are left out because `src/main/cpp/Android.mk`
 * turns those features off and the `.so` has no way to return them. Any other
 * value falls into [UNKNOWN], with the raw integer preserved in
 * [PatchResult.NativeFailure.rawCode].
 */
enum class HPatchCode(val rawCode: Int) {
    /** Invalid argument arriving at the JNI (a null string, for example). */
    OPTIONS_ERROR(1),
    OPEN_READ_ERROR(2),
    OPEN_WRITE_ERROR(3),
    FILE_READ_ERROR(4),
    FILE_WRITE_ERROR(5),

    /** The diff file's content makes no sense — patch corrupted during download. */
    FILE_DATA_ERROR(6),
    FILE_CLOSE_ERROR(7),

    /** Out of memory on the native side. See `ApkPatcher.DEFAULT_CACHE_MEMORY_BYTES`. */
    MEM_ERROR(8),

    /** Unreadable diff header, or a base whose size differs from what the diff declares. */
    HDIFF_INFO_ERROR(9),

    /**
     * The diff was compressed with a plugin this `.so` does not have. A sign of
     * drift between the server's generator and this module — see the list of
     * switches in `src/main/cpp/Android.mk`.
     */
    COMPRESS_TYPE_ERROR(10),

    /** Applying the diff itself failed (a wrong base is the common cause). */
    HPATCH_ERROR(11),
    PATH_TYPE_ERROR(12),
    TEMP_PATH_ERROR(13),
    DELETE_PATH_ERROR(14),
    RENAME_PATH_ERROR(15),
    SPATCH_ERROR(16),
    WIN_PATCH_ERROR(19),

    DECOMPRESSER_OPEN_ERROR(20),
    DECOMPRESSER_CLOSE_ERROR(21),
    DECOMPRESSER_MEM_ERROR(22),
    DECOMPRESSER_DECOMPRESS_ERROR(23),

    /** The disk filled up mid-write — becomes [PatchResult.InsufficientStorage]. */
    FILE_WRITE_NO_SPACE_ERROR(24),
    MULTITHREAD_ERROR(25),

    /** The diff asks for a checksum this `.so` did not link in. */
    CHECKSUM_SET_ERROR(103),
    CHECKSUM_DIFFDATA_ERROR(104),
    CHECKSUM_OLDDATA_ERROR(105),
    CHECKSUM_NEWDATA_ERROR(106),

    /** A code this enum does not know. The real value stays in `NativeFailure.rawCode`. */
    UNKNOWN(-1),
    ;

    companion object {
        private val byRaw: Map<Int, HPatchCode> = entries.associateBy { it.rawCode }

        fun fromRaw(raw: Int): HPatchCode = byRaw[raw] ?: UNKNOWN
    }
}
