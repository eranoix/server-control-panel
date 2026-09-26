package dev.servercontrolpanel.app.share

import android.content.ContentResolver
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.provider.OpenableColumns
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.servercontrolpanel.app.MainActivity
import dev.servercontrolpanel.app.PanelApplication
import dev.servercontrolpanel.designsystem.ThemePreference
import dev.servercontrolpanel.designsystem.PanelTheme
import dev.servercontrolpanel.feature.files.share.ShareDestinationScreen
import dev.servercontrolpanel.feature.files.share.SharedItem
import java.io.File
import java.io.FileOutputStream

/**
 * Entry point when another Android app shares content into server-control-panel
 * via `ACTION_SEND`/`ACTION_SEND_MULTIPLE`. A dedicated activity
 * (not [MainActivity]) so the share flow gets its own clean task/back-stack,
 * independent of wherever the main app happens to be navigated at the time,
 * and so it can be launched cold -- by the share sheet, with no prior
 * [MainActivity] instance -- without disturbing any existing one.
 *
 * A single-screen `ComponentActivity` wrapping one Composable, following the
 * same `enableEdgeToEdge()` + theme pattern [MainActivity] established.
 */
class ShareTargetActivity : ComponentActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        // Extraction (and the persistable-permission grab inside it) happens
        // exactly once here, in onCreate -- a shared content Uri's read grant
        // is otherwise transient and tied to this activity's lifetime, but
        // UploadWorker (Plan 10-05) may run the actual upload well after this
        // activity, and the app that shared the content, are both gone.
        val sharedItems = extractSharedItems(intent)

        val serverConfigRepository = (application as PanelApplication).serverConfigRepository
        val alreadyConfigured = serverConfigRepository.currentBaseUrl() != null

        // The same appearance preference as MainActivity (it is a process
        // singleton): sharing a file must not open a window in the opposite
        // theme to the rest of the app.
        val themePreference = ThemePreference.get(applicationContext)

        setContent {
            val themeMode by themePreference.mode.collectAsStateWithLifecycle()
            PanelTheme(themeMode = themeMode) {
                when {
                    !alreadyConfigured -> UnconfiguredContent(
                        onOpenApp = {
                            startActivity(Intent(this, MainActivity::class.java))
                            finish()
                        },
                    )
                    sharedItems.isEmpty() -> NothingSharedContent(onDone = ::finish)
                    else -> ShareDestinationScreen(sharedItems = sharedItems, onDone = ::finish)
                }
            }
        }
    }

    /**
     * Handles the three real share shapes: a single-file `ACTION_SEND`, a
     * multi-file `ACTION_SEND_MULTIPLE`, and a text-only `ACTION_SEND` (no
     * `EXTRA_STREAM`, just `EXTRA_TEXT` -- a shared URL or note from a
     * browser/notes app). Anything else (an action this activity's manifest
     * entry does not advertise, or a `SEND`/`SEND_MULTIPLE` with neither a
     * stream nor usable text) yields an empty list, handled explicitly by
     * [NothingSharedContent] rather than silently dropping the share.
     */
    private fun extractSharedItems(intent: Intent): List<SharedItem> = when (intent.action) {
        Intent.ACTION_SEND -> {
            val streamUri = intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
            if (streamUri != null) {
                listOfNotNull(resolveSharedUri(streamUri))
            } else {
                val text = intent.getStringExtra(Intent.EXTRA_TEXT)
                if (!text.isNullOrBlank()) listOf(writeSharedTextToFile(text)) else emptyList()
            }
        }
        Intent.ACTION_SEND_MULTIPLE -> {
            intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java)
                .orEmpty()
                .map(::resolveSharedUri)
        }
        else -> emptyList()
    }

    /**
     * Claims a persistable read grant immediately (the sharing
     * app already granted `FLAG_GRANT_READ_URI_PERMISSION` implicitly via the
     * share-sheet mechanism, but only for this transient delivery) and
     * resolves the display name/size the destination screen shows before
     * committing to an upload. A provider that refuses a persistable grant
     * (rare, but not contractually guaranteed by every content provider) is
     * still forwarded as-is -- if the transient grant does not survive long
     * enough for `UploadWorker` to run, the upload fails visibly through its
     * normal retry/`Result.failure` path rather than being silently dropped
     * here before the admin ever sees it.
     */
    private fun resolveSharedUri(uri: Uri): SharedItem {
        try {
            contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
        } catch (e: SecurityException) {
            // See doc above -- proceed with the transient grant regardless.
        }
        val (name, size) = queryDisplayNameAndSize(contentResolver, uri)
        return SharedItem(uri = uri.toString(), displayName = name ?: uri.lastPathSegment ?: "file", sizeBytes = size)
    }

    /**
     * A plain-text share (`EXTRA_TEXT`, e.g. a URL selected in a browser) has
     * no `Uri` at all. Writing it to a small generated `.txt` file in
     * app-private cache storage first lets it converge onto the exact same
     * upload path as a real file share -- [ShareDestinationScreen] never
     * needs a second code path for text. This file's `Uri` is never placed
     * in an outgoing `Intent` (it stays purely internal, read back by this
     * same app's `UploadWorker` via `ContentResolver`), so it needs no
     * `FileProvider`/persistable-grant machinery, unlike [resolveSharedUri]'s
     * content Uris from another app.
     */
    private fun writeSharedTextToFile(text: String): SharedItem {
        val filename = "shared-${System.currentTimeMillis()}.txt"
        val file = File(cacheDir, filename)
        FileOutputStream(file).use { it.write(text.toByteArray(Charsets.UTF_8)) }
        return SharedItem(uri = Uri.fromFile(file).toString(), displayName = filename, sizeBytes = file.length())
    }
}

/** `null` name/zero size are both legitimate misses (a provider that doesn't answer these columns). */
private fun queryDisplayNameAndSize(contentResolver: ContentResolver, uri: Uri): Pair<String?, Long> {
    var name: String? = null
    var size = 0L
    contentResolver.query(uri, null, null, null, null)?.use { cursor ->
        val nameIndex = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
        val sizeIndex = cursor.getColumnIndex(OpenableColumns.SIZE)
        if (cursor.moveToFirst()) {
            if (nameIndex >= 0) name = cursor.getString(nameIndex)
            if (sizeIndex >= 0 && !cursor.isNull(sizeIndex)) size = cursor.getLong(sizeIndex)
        }
    }
    return name to size
}

/**
 * Shown when the app is launched cold, by the share sheet, on a device that
 * has never been paired with a server (`MainActivity`'s own first-run gate
 * would show `PasskeyRegisterFlow` for the exact same reason). There is
 * nowhere for the shared content to land yet -- the admin is sent to
 * [MainActivity] to pair first, rather than the share silently failing or
 * this activity crashing on a null base URL.
 */
@Composable
private fun UnconfiguredContent(onOpenApp: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(text = "Server Control Panel is not set up yet", style = MaterialTheme.typography.titleLarge)
        Text(text = "Pair this device with a server before sharing files or text.")
        Button(onClick = onOpenApp) { Text(text = "Open Server Control Panel") }
    }
}

/** Shown when the incoming Intent carries no recognizable content -- never a silent no-op. */
@Composable
private fun NothingSharedContent(onDone: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(text = "Nothing to share", style = MaterialTheme.typography.titleLarge)
        Text(text = "Could not recognize the shared content.")
        Button(onClick = onDone) { Text(text = "Close") }
    }
}
