package dev.servercontrolpanel.app.update

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
import dev.servercontrolpanel.data.update.UpdateRecovery
import dev.servercontrolpanel.data.update.UpdateState
import dev.servercontrolpanel.data.update.formatDownloadSize
import dev.servercontrolpanel.designsystem.panelStatusColors

@Composable
fun UpdateBanner(
    state: UpdateState,
    onUpdateClick: () -> Unit,
    onCancelClick: () -> Unit,
    onRecoveryClick: (UpdateRecovery) -> Unit,
    modifier: Modifier = Modifier,
) {
    val warning = panelStatusColors.warning
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

internal data class UpdateBannerContent(
    val text: String,
    val actions: List<UpdateBannerAction> = emptyList(),
    val progress: Float? = null,
    val spinner: Boolean = false,
)

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

    data class LabeledCancel(override val label: String) : UpdateBannerAction

    data class Recover(override val label: String, val recovery: UpdateRecovery) : UpdateBannerAction
}

internal fun UpdateBannerAction.Cancel.withLabel(label: String): UpdateBannerAction =
    UpdateBannerAction.LabeledCancel(label)

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
            if (state.canRetry && state.recovery != UpdateRecovery.USE_BROWSER) {
                add(UpdateBannerAction.Retry)
            }
        },
    )
}
