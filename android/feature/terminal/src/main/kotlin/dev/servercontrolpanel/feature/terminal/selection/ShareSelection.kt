package dev.servercontrolpanel.feature.terminal.selection

import android.content.Context
import android.content.Intent

const val SHARE_SELECTION_TITLE = "Share selection"

fun shareTextIntent(text: String): Intent = Intent.createChooser(
    Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, text)
    },
    SHARE_SELECTION_TITLE,
)

fun shareText(context: Context, text: String) {
    context.startActivity(shareTextIntent(text))
}
