package com.vpsmanager.feature.terminal.attach

import android.widget.Toast
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel

/**
 * What appears when a path is inserted while the session is down. It has to
 * say the attachment was NOT lost — it is still on the bar, ready to be
 * inserted once the connection comes back.
 */
const val NOTICE_TERMINAL_OFFLINE =
    "The session is not connected right now. The attachment is still here — tap Insert when it comes back."

/**
 * All of the terminal's attachment handling behind ONE function — the progress
 * bar, the source sheet and the ViewModel that ties them together.
 *
 * **Why a single wrapper, and not three calls in `TerminalRoute`.** Two
 * reasons, and both matter:
 *
 * 1. `TerminalRoute` has a doctrine stated in its own doc: *"this screen's
 *    column is deliberately very short: top bar, grid, key row. Nothing
 *    else."* An attachment feature that scattered a ViewModel, sheet state and
 *    a progress bar through that function would unpick it bit by bit — which
 *    is exactly how it once reached 224.8 dp of chrome.
 * 2. `TerminalRoute` is a file several sessions edit at the same time. The
 *    smaller the hook in there, the smaller the chance two concurrent edits
 *    collide — here it is three lines.
 *
 * [onInsertText] receives text ALREADY shell-ready (quoted paths, separated
 * by spaces, ending in a space and never in a newline) and is wired to the
 * terminal's `sendPaste`, which decides on its own whether to wrap it in
 * bracketed paste according to the mode the remote program turned on. In other
 * words: inserting the path goes through the SAME proven code path as pasting
 * — no second way of writing to the PTY was invented for this.
 */
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
            // **Do not insert into a terminal that is not up.**
            // `TerminalSocketClient.send` is `socket?.sendBytes(bytes)`: with
            // the connection down (or still reconnecting after the app comes
            // back from the background), the bytes are silently DISCARDED.
            // Without this guard the path vanished without a trace — the line
            // disappeared from the bar, nothing appeared on the command line,
            // and there was nothing on screen to explain what had happened.
            // That is exactly what happened on the emulator right after
            // reinstalling the APK. Here the attachment STAYS on the bar and
            // the operator knows why: one tap later, with the session back, it
            // goes in.
            if (!terminalReady) {
                Toast.makeText(context, NOTICE_TERMINAL_OFFLINE, Toast.LENGTH_LONG).show()
                return@AttachmentBar
            }
            onInsertText(text)
            // Once inserted, it leaves the bar: leaving it there would invite
            // inserting the same path twice without noticing. The file stays
            // on the server — what goes is only the tracking line.
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
