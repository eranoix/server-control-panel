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

class ShareTargetActivity : ComponentActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        val sharedItems = extractSharedItems(intent)

        val serverConfigRepository = (application as PanelApplication).serverConfigRepository
        val alreadyConfigured = serverConfigRepository.currentBaseUrl() != null

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

    private fun resolveSharedUri(uri: Uri): SharedItem {
        try {
            contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
        } catch (e: SecurityException) {
        }
        val (name, size) = queryDisplayNameAndSize(contentResolver, uri)
        return SharedItem(uri = uri.toString(), displayName = name ?: uri.lastPathSegment ?: "file", sizeBytes = size)
    }

    private fun writeSharedTextToFile(text: String): SharedItem {
        val filename = "shared-${System.currentTimeMillis()}.txt"
        val file = File(cacheDir, filename)
        FileOutputStream(file).use { it.write(text.toByteArray(Charsets.UTF_8)) }
        return SharedItem(uri = Uri.fromFile(file).toString(), displayName = filename, sizeBytes = file.length())
    }
}

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
