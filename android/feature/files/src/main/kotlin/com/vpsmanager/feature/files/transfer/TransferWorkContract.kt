package com.vpsmanager.feature.files.transfer

/**
 * `Data` keys shared between [TransferViewModel] (which enqueues
 * [DownloadWorker]/[UploadWorker]) and the workers themselves -- one place
 * for every key so the two sides can never drift out of sync on a string
 * literal.
 */
internal const val KEY_WORK_NAME = "work_name"
internal const val KEY_SERVER_PATH = "server_path"
internal const val KEY_FILENAME = "filename"
internal const val KEY_MIME_TYPE = "mime_type"
internal const val KEY_TOTAL_BYTES = "total_bytes"
internal const val KEY_SOURCE_URI = "source_uri"
internal const val KEY_DEST_DIR = "dest_dir"
internal const val KEY_PERCENT = "percent"
internal const val KEY_RESULT_PATH = "result_path"
internal const val KEY_ERROR_REASON = "error_reason"

// The chunk size has moved out of here: it is now
// DEFAULT_UPLOAD_CHUNK_SIZE_BYTES in :data, alongside the upload loop
// (ChunkedUploadPump) that is its only consumer — keeping it here would leave
// two constants free to drift apart.

/** `-1` is this package's convention for "percent unknown" (indeterminate progress). */
internal const val PERCENT_UNKNOWN = -1

internal fun percentOf(written: Long, total: Long?): Int {
    if (total == null || total <= 0) return PERCENT_UNKNOWN
    return ((written * 100) / total).toInt().coerceIn(0, 100)
}
