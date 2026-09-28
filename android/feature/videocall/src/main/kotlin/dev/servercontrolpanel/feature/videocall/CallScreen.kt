package dev.servercontrolpanel.feature.videocall

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
import dev.servercontrolpanel.feature.videocall.pip.AutoEnterFloatingWindow
import dev.servercontrolpanel.feature.videocall.pip.CallInWindow
import dev.servercontrolpanel.feature.videocall.pip.FloatingWindow
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory

private val CALL_PERMISSIONS = arrayOf(Manifest.permission.RECORD_AUDIO, Manifest.permission.CAMERA)

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
        viewModel.openLobby()
    }

    LaunchedEffect(roomId) {
        viewModel.openLobby()
    }

    val inWindow by FloatingWindow.inWindow.collectAsStateWithLifecycle()

    AutoEnterFloatingWindow(enabled = state is CallUiState.InCall)

    Box(modifier = modifier.fillMaxSize()) {
        when (val current = state) {
            CallUiState.Loading -> CallLoadingContent()
            is CallUiState.PermissionRequired -> PermissionRequiredContent(
                onRequestPermissions = { permissionLauncher.launch(CALL_PERMISSIONS) },
            )
            is CallUiState.Error -> CallErrorContent(message = current.message, onLeave = onLeaveCall)
            is CallUiState.Lobby -> LobbyScreen(
                state = current,
                eglBaseContext = viewModel.eglBaseContext,
                onToggleMic = viewModel::onToggleMic,
                onToggleCamera = viewModel::onToggleCamera,
                onSwitchCamera = viewModel::onSwitchCamera,
                onEnter = { viewModel.joinRoom(roomId) },
                onGiveUp = {
                    viewModel.onLeave()
                    onLeaveCall()
                },
            )
            is CallUiState.InCall -> if (inWindow) {
                CallInWindow(state = current, eglBaseContext = viewModel.eglBaseContext)
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
