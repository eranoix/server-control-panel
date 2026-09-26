package com.vpsmanager.feature.videocall

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import com.vpsmanager.data.videocall.VideocallRoom
import com.vpsmanager.data.videocall.VideocallRoomsResult
import com.vpsmanager.data.videocall.VideocallRoomsSource
import com.vpsmanager.data.videocall.VideocallSignalingRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * Mirrors `HomeUiState`'s established Loading/Success/Empty/Error shape. Rooms come from
 * [VideocallRoomsSource] (`:data`) — `:feature-videocall` never references the generated mobile
 * API client or its model types directly.
 */
sealed interface RoomLobbyUiState {
    data object Loading : RoomLobbyUiState
    data class Success(val rooms: List<VideocallRoom>) : RoomLobbyUiState
    data object Empty : RoomLobbyUiState
    data class Error(val message: String) : RoomLobbyUiState
}

class RoomLobbyViewModel(
    private val roomSource: VideocallRoomsSource = VideocallSignalingRepository(),
) : ViewModel() {
    private val _uiState = MutableStateFlow<RoomLobbyUiState>(RoomLobbyUiState.Loading)
    val uiState: StateFlow<RoomLobbyUiState> = _uiState.asStateFlow()

    init {
        loadRooms()
    }

    fun loadRooms() {
        _uiState.value = RoomLobbyUiState.Loading
        viewModelScope.launch {
            _uiState.value = when (val result = roomSource.rooms()) {
                is VideocallRoomsResult.Success -> RoomLobbyUiState.Success(result.rooms)
                VideocallRoomsResult.Empty -> RoomLobbyUiState.Empty
                is VideocallRoomsResult.Error -> RoomLobbyUiState.Error(result.reason)
            }
        }
    }
}

/**
 * The real content of the Call destination: lists the video-call rooms and
 * navigates to [CallScreen] on tap — which today opens in the green room, not
 * straight into the call.
 *
 * (The reference to the old `ComingSoonScreen` went out along with the file
 * itself, which nothing was using any more.)
 */
@Composable
fun RoomLobbyScreen(
    onRoomSelected: (roomId: String) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: RoomLobbyViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    Box(modifier = modifier.fillMaxSize()) {
        when (val current = state) {
            RoomLobbyUiState.Loading -> LoadingContent()
            is RoomLobbyUiState.Success -> RoomListContent(rooms = current.rooms, onRoomSelected = onRoomSelected)
            RoomLobbyUiState.Empty -> EmptyRoomsContent()
            is RoomLobbyUiState.Error -> LobbyErrorContent(message = current.message, onRetry = viewModel::loadRooms)
        }
    }
}

@Composable
private fun LoadingContent() {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
            CircularProgressIndicator()
            Text(text = "Loading rooms…")
        }
    }
}

@Composable
private fun EmptyRoomsContent() {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Card {
            Column(modifier = Modifier.padding(24.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(text = "No rooms yet", style = MaterialTheme.typography.titleLarge)
                Text(text = "Create a video call room in the panel to get started.")
            }
        }
    }
}

@Composable
private fun LobbyErrorContent(message: String, onRetry: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Card {
            Column(modifier = Modifier.padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                Text(text = "Could not load", style = MaterialTheme.typography.titleLarge)
                Text(text = message)
                Button(onClick = onRetry) {
                    Text(text = "Try again")
                }
            }
        }
    }
}

@Composable
private fun RoomListContent(rooms: List<VideocallRoom>, onRoomSelected: (String) -> Unit) {
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        items(rooms, key = { it.id }) { room ->
            Card(modifier = Modifier.fillMaxSize().clickable { onRoomSelected(room.id) }) {
                Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(text = room.name, style = MaterialTheme.typography.titleMedium)
                    Text(
                        text = "${room.memberCount} participant(s)",
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
            }
        }
    }
}
