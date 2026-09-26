package com.vpsmanager.app.update

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.vpsmanager.data.update.UpdateRecovery
import com.vpsmanager.data.update.UpdateState
import com.vpsmanager.data.update.formatDownloadSize
import com.vpsmanager.designsystem.vpsmStatusColors

/**
 * The update banner, right below the `TopAppBar`.
 *
 * ### Why the size appears in the main text
 * "Version 0.1.7 available — 1.4 MB". On the owner's connection, that
 * number is the most important information on the screen: it is the
 * difference between tapping now and waiting until you get home. It is the
 * size of what TRAVELS (the patch, when there is one), never that of the
 * rebuilt APK — saying 31 MB when 1.4 will go over the wire would be lying
 * in the direction that makes the owner put it off.
 *
 * ### Why it imitates `ConnectionBanner`
 * There is no banner component in the design system; the nearest is
 * `ConnectionBanner` (`:feature-terminal`), and its shape is followed here
 * on purpose — the same `AnimatedVisibility`, the same `Row` with a flat
 * background instead of a `Card`, the same absence of a shadow — so that
 * the app's two banners read as the same thing. The colour is
 * `vpsmStatusColors.warning`: it is an actionable notice, not an error.
 *
 * ### Why a failure can have two buttons
 * On a failure, "what to do now" and "try again" are almost never the same
 * thing: freeing space is not retrying, looking at the diagnostics is not
 * retrying. A single button would force a choice between hiding the way
 * out and hiding the retry.
 *
 * Stateless throughout: it takes [state] and hands back taps. That is what
 * makes it possible to pin every rung of the ladder in a Compose test with
 * no network, no `PackageInstaller` and no emulator.
 */
@Composable
fun UpdateBanner(
    state: UpdateState,
    onUpdateClick: () -> Unit,
    onCancelClick: () -> Unit,
    onRecoveryClick: (UpdateRecovery) -> Unit,
    modifier: Modifier = Modifier,
) {
    val warning = vpsmStatusColors.warning
    val content = bannerContentFor(state)

    AnimatedVisibility(visible = content != null, modifier = modifier) {
        val shown = content ?: return@AnimatedVisibility
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .background(warning.container)
                .padding(horizontal = 16.dp, vertical = 8.dp),
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                if (shown.spinner) {
                    CircularProgressIndicator(
                        modifier = Modifier.padding(end = 4.dp),
                        color = warning.accent,
                    )
                }
                Text(
                    text = shown.text,
                    color = warning.content,
                    style = MaterialTheme.typography.bodyMedium,
                    maxLines = 4,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                shown.actions.forEach { action ->
                    TextButton(
                        onClick = {
                            when (action) {
                                // "Update" and "Try again" are the SAME path:
                                // the coordinator resumes the partial download
                                // left on disk instead of starting over. Two
                                // labels because the two situations are
                                // different for whoever reads them.
                                is UpdateBannerAction.Update, is UpdateBannerAction.Retry -> onUpdateClick()
                                is UpdateBannerAction.Cancel,
                                is UpdateBannerAction.LabeledCancel,
                                -> onCancelClick()
                                is UpdateBannerAction.Recover -> onRecoveryClick(action.recovery)
                            }
                        },
                    ) {
                        Text(text = action.label, color = warning.accent)
                    }
                }
            }
            shown.progress?.let { fraction ->
                LinearProgressIndicator(
                    progress = { fraction },
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = 6.dp),
                    color = warning.accent,
                )
            }
        }
    }
}

/** What the banner shows in a given state. `null` means "show nothing". */
internal data class UpdateBannerContent(
    val text: String,
    val actions: List<UpdateBannerAction> = emptyList(),
    val progress: Float? = null,
    val spinner: Boolean = false,
)

/** The banner's possible buttons. */
internal sealed interface UpdateBannerAction {
    val label: String

    data object Update : UpdateBannerAction {
        override val label = "Update"
    }

    data object Retry : UpdateBannerAction {
        override val label = "Try again"
    }

    data object Cancel : UpdateBannerAction {
        override val label = "Cancel"
    }

    /**
     * The same Cancel with a different label. "Cancel" fits a download in
     * progress; in a sentence that only informs, "Cancel" asks the reader
     * what exactly they would be cancelling.
     */
    data class LabeledCancel(override val label: String) : UpdateBannerAction

    data class Recover(override val label: String, val recovery: UpdateRecovery) : UpdateBannerAction
}

/** Reading sugar for the two answer states. */
internal fun UpdateBannerAction.Cancel.withLabel(label: String): UpdateBannerAction =
    UpdateBannerAction.LabeledCancel(label)

/**
 * The state-to-banner translation, split from the drawing so it can be
 * pinned by a test with no Compose tree.
 *
 * [UpdateState.Checking] does NOT appear: checking the manifest is
 * background routine, and a banner that blinks "checking…" on every launch
 * becomes noise the owner learns to ignore — including when it finally has
 * something to say.
 */
internal fun bannerContentFor(state: UpdateState): UpdateBannerContent? = when (state) {
    is UpdateState.Idle, is UpdateState.Checking -> null

    is UpdateState.Available -> UpdateBannerContent(
        text = "Version ${state.versionName} available — ${formatDownloadSize(state.downloadBytes)}",
        actions = listOf(UpdateBannerAction.Update),
    )

    is UpdateState.Downloading -> UpdateBannerContent(
        text = "Downloading ${state.versionName} — " +
            "${formatDownloadSize(state.downloadedBytes)} of ${formatDownloadSize(state.totalBytes)}",
        actions = listOf(UpdateBannerAction.Cancel),
        progress = if (state.totalBytes > 0) {
            (state.downloadedBytes.toFloat() / state.totalBytes.toFloat()).coerceIn(0f, 1f)
        } else {
            null
        },
    )

    is UpdateState.Applying -> UpdateBannerContent(
        text = "Preparing version ${state.versionName}…",
        spinner = true,
    )

    is UpdateState.Installing -> UpdateBannerContent(
        text = "Installing version ${state.versionName}…",
        spinner = true,
    )

    // The two answers to a REQUESTED check. They carry "Close" because
    // whoever asked may want the answer out of the way before the 6 s are up
    // — and "Close" here is the same old `cancel()`, which returns the banner
    // to the state the manifest describes (none, when there is no news).
    is UpdateState.UpToDate -> UpdateBannerContent(
        text = "You already have the latest version (${state.versionName}).",
        actions = listOf(UpdateBannerAction.Cancel.withLabel("Close")),
    )

    is UpdateState.CheckFailed -> UpdateBannerContent(
        text = state.message,
        actions = listOf(UpdateBannerAction.Cancel.withLabel("Close")),
    )

    is UpdateState.Failed -> UpdateBannerContent(
        text = state.message,
        actions = buildList {
            when (state.recovery) {
                UpdateRecovery.ALLOW_UNKNOWN_SOURCES ->
                    add(UpdateBannerAction.Recover("Allow", UpdateRecovery.ALLOW_UNKNOWN_SOURCES))
                UpdateRecovery.USE_BROWSER ->
                    add(UpdateBannerAction.Recover("How to install", UpdateRecovery.USE_BROWSER))
                UpdateRecovery.FREE_SPACE ->
                    add(UpdateBannerAction.Recover("Free up space", UpdateRecovery.FREE_SPACE))
                UpdateRecovery.SHOW_DIAGNOSTICS ->
                    add(UpdateBannerAction.Recover("Diagnostics", UpdateRecovery.SHOW_DIAGNOSTICS))
                UpdateRecovery.NONE -> Unit
            }
            // "How to install" is the end of the line: the device refused
            // this path, and offering "try again" there would push the owner
            // into the same wall all over again.
            if (state.canRetry && state.recovery != UpdateRecovery.USE_BROWSER) {
                add(UpdateBannerAction.Retry)
            }
        },
    )
}
