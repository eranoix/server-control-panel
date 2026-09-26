package com.vpsmanager.feature.videocall

import android.Manifest
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.vpsmanager.feature.videocall.pip.AutoEntrarNaJanelaFlutuante
import com.vpsmanager.feature.videocall.pip.ChamadaNaJanela
import com.vpsmanager.feature.videocall.pip.JanelaFlutuante
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory

/** Runtime permissions [CallScreen] requests before [CallViewModel.joinRoom] can proceed. */
private val CALL_PERMISSIONS = arrayOf(Manifest.permission.RECORD_AUDIO, Manifest.permission.CAMERA)

/**
 * The active-call screen: own camera preview, one tile per remote peer, and the
 * bottom control bar. [roomId] is joined once per screen instance via `LaunchedEffect(roomId)`;
 * [CallViewModel] is scoped to THIS back-stack entry (`createCallViewModel(context)`, no
 * `SavedStateHandle` dependency — see the ViewModel's own doc comment on why a fresh instance per
 * room visit is correct here, unlike `TerminalViewModel`'s session-name-from-back-stack case).
 *
 * Applies neither `imePadding()` nor a second `consumeWindowInsets` call — `AppNavHost`'s
 * `NavHost` modifier already applies both exactly once for every destination, this one included.
 */
@Composable
fun CallScreen(
    roomId: String,
    onLeaveCall: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: CallViewModel = defaultCallViewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    val permissionLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.RequestMultiplePermissions(),
    ) {
        // Re-attempt regardless of the exact grant map — abrirAntessala re-checks every
        // permission itself and re-emits PermissionRequired for whatever is still missing.
        viewModel.abrirAntessala()
    }

    // THE GREEN ROOM, not joining straight away. Joining a call is the only
    // action in this app that is public and irreversible: by the time the
    // person finds out they were muted, the others have already seen. See
    // [Antessala].
    LaunchedEffect(roomId) {
        viewModel.abrirAntessala()
    }

    val naJanela by JanelaFlutuante.naJanela.collectAsStateWithLifecycle()

    // Auto-enter only applies INSIDE the call. Enabling it while the screen is
    // still asking for permission would make leaving the app open a little
    // window for a call that never started.
    AutoEntrarNaJanelaFlutuante(habilitado = state is CallUiState.InCall)

    Box(modifier = modifier.fillMaxSize()) {
        when (val current = state) {
            CallUiState.Loading -> CallLoadingContent()
            is CallUiState.PermissionRequired -> PermissionRequiredContent(
                onRequestPermissions = { permissionLauncher.launch(CALL_PERMISSIONS) },
            )
            is CallUiState.Error -> CallErrorContent(message = current.message, onLeave = onLeaveCall)
            is CallUiState.Lobby -> Antessala(
                state = current,
                eglBaseContext = viewModel.eglBaseContext,
                onToggleMic = viewModel::onToggleMic,
                onToggleCamera = viewModel::onToggleCamera,
                onSwitchCamera = viewModel::onSwitchCamera,
                onEntrar = { viewModel.joinRoom(roomId) },
                onDesistir = {
                    // Backing out of the green room has to RELEASE the
                    // camera. Without the onLeave, the device's light would
                    // stay on after the person went back to the list — and the
                    // next call would find the camera busy with this very app.
                    viewModel.onLeave()
                    onLeaveCall()
                },
            )
            is CallUiState.InCall -> if (naJanela) {
                // IN THE LITTLE WINDOW: video and one line of state, nothing
                // more.
                //
                // It is not a "reduced" version of the full screen — it is
                // different content. In a window of ~200 dp, a 48 dp button
                // covers a quarter of the area and nobody hits it; participant
                // names and the grid turn to mush. And the state has to fit
                // there because the little window is the ONLY place where a
                // drop can be seen while the app is not in the foreground.
                ChamadaNaJanela(state = current, eglBaseContext = viewModel.eglBaseContext)
            } else {
                InCallContent(
                    state = current,
                    eglBaseContext = viewModel.eglBaseContext,
                    onToggleMic = viewModel::onToggleMic,
                    onToggleCamera = viewModel::onToggleCamera,
                    onSwitchCamera = viewModel::onSwitchCamera,
                    onHangUp = {
                        viewModel.onLeave()
                        onLeaveCall()
                    },
                )
            }
        }
    }
}

/** Builds the real, production [CallViewModel] via [createCallViewModel] — the injectable `viewModel` param on [CallScreen] exists so a test can supply one built from fakes instead (mirrors `PasskeyRegisterFlow`'s `passkeyRegisterViewModel()`). */
@Composable
private fun defaultCallViewModel(): CallViewModel {
    val context = LocalContext.current
    return viewModel(factory = viewModelFactory { initializer { createCallViewModel(context) } })
}

@Composable
private fun CallLoadingContent() {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
            CircularProgressIndicator()
            Text(text = "Joining the call…")
        }
    }
}

@Composable
private fun PermissionRequiredContent(onRequestPermissions: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Card {
            Column(modifier = Modifier.padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                Text(text = "Permissions required", style = MaterialTheme.typography.titleLarge)
                Text(text = "The video call needs access to the microphone and camera.")
                Button(onClick = onRequestPermissions) {
                    Text(text = "Grant permissions")
                }
            }
        }
    }
}

@Composable
private fun CallErrorContent(message: String, onLeave: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Card {
            Column(modifier = Modifier.padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                Text(text = "Could not continue", style = MaterialTheme.typography.titleLarge)
                Text(text = message)
                Button(onClick = onLeave) {
                    Text(text = "Leave")
                }
            }
        }
    }
}

@Composable
private fun InCallContent(
    state: CallUiState.InCall,
    eglBaseContext: org.webrtc.EglBase.Context,
    onToggleMic: () -> Unit,
    onToggleCamera: () -> Unit,
    onSwitchCamera: () -> Unit,
    onHangUp: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxSize()) {
        LazyVerticalGrid(
            columns = GridCells.Fixed(2),
            modifier = Modifier.fillMaxSize().weight(1f),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(4.dp),
        ) {
            item {
                VideoTile(
                    track = state.localTrack,
                    eglBaseContext = eglBaseContext,
                    modifier = Modifier.aspectRatio(3f / 4f).padding(4.dp),
                )
            }
            items(state.remoteTracks.entries.toList(), key = { it.key }) { entry ->
                VideoTile(
                    track = entry.value,
                    eglBaseContext = eglBaseContext,
                    modifier = Modifier.aspectRatio(3f / 4f).padding(4.dp),
                )
            }
        }
        CallControls(
            micEnabled = state.micEnabled,
            cameraEnabled = state.cameraEnabled,
            onToggleMic = onToggleMic,
            onToggleCamera = onToggleCamera,
            onSwitchCamera = onSwitchCamera,
            onHangUp = onHangUp,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}
