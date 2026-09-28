package dev.servercontrolpanel.feature.terminal.attach

import android.widget.Toast
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel

const val NOTICE_TERMINAL_OFFLINE =
    "The session is not connected right now. The attachment is still here — tap Insert when it comes back."

@Composable
fun TerminalAttachment(
    sheetOpen: Boolean,
    onCloseSheet: () -> Unit,
    onInsertText: (String) -> Unit,
    terminalReady: Boolean,
    modifier: Modifier = Modifier,
) {
    val viewModel: TerminalAttachmentViewModel = viewModel()
    val attachments by viewModel.attachments.collectAsStateWithLifecycle()
    val context = LocalContext.current

    AttachmentBar(
        attachments = attachments,
        onInsert = { ids ->
            val text = viewModel.insertionText(ids)
            if (text.isEmpty()) {
                return@AttachmentBar
            }
            if (!terminalReady) {
                Toast.makeText(context, NOTICE_TERMINAL_OFFLINE, Toast.LENGTH_LONG).show()
                return@AttachmentBar
            }
            onInsertText(text)
            ids.forEach(viewModel::discard)
        },
        onCancel = viewModel::cancel,
        onDiscard = viewModel::discard,
        modifier = modifier,
    )

    if (sheetOpen) {
        AttachmentSourceSheet(
            onChoose = viewModel::attach,
            onClose = onCloseSheet,
        )
    }
}
