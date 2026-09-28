package dev.servercontrolpanel.patchengine

import java.io.File

sealed interface PatchResult {

    data class Applied(val newFile: File, val sha256: String) : PatchResult

    data class InputMissing(val role: InputRole, val path: String) : PatchResult

    data class InsufficientStorage(
        val requiredBytes: Long,
        val usableBytes: Long,
    ) : PatchResult

    data class EngineUnavailable(val detail: String) : PatchResult

    data class NativeFailure(val code: HPatchCode, val rawCode: Int) : PatchResult

    data class IntegrityMismatch(
        val expectedSha256: String,
        val actualSha256: String,
    ) : PatchResult

    data class OutputUnwritable(val path: String, val detail: String) : PatchResult

    enum class InputRole { BASE_APK, PATCH }
}

enum class HPatchCode(val rawCode: Int) {
    OPTIONS_ERROR(1),
    OPEN_READ_ERROR(2),
    OPEN_WRITE_ERROR(3),
    FILE_READ_ERROR(4),
    FILE_WRITE_ERROR(5),

    FILE_DATA_ERROR(6),
    FILE_CLOSE_ERROR(7),

    MEM_ERROR(8),

    HDIFF_INFO_ERROR(9),

    COMPRESS_TYPE_ERROR(10),

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

    FILE_WRITE_NO_SPACE_ERROR(24),
    MULTITHREAD_ERROR(25),

    CHECKSUM_SET_ERROR(103),
    CHECKSUM_DIFFDATA_ERROR(104),
    CHECKSUM_OLDDATA_ERROR(105),
    CHECKSUM_NEWDATA_ERROR(106),

    UNKNOWN(-1),
    ;

    companion object {
        private val byRaw: Map<Int, HPatchCode> = entries.associateBy { it.rawCode }

        fun fromRaw(raw: Int): HPatchCode = byRaw[raw] ?: UNKNOWN
    }
}
