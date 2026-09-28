package dev.servercontrolpanel.feature.whatsapp

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.core.model.WhatsAppChat
import dev.servercontrolpanel.data.whatsapp.ChatsResult
import dev.servercontrolpanel.data.whatsapp.WhatsAppRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface ChatListUiState {
    data object Loading : ChatListUiState
    data class Error(val message: String) : ChatListUiState
    data object Empty : ChatListUiState
    data class Success(val chats: List<WhatsAppChat>) : ChatListUiState
}

open class ChatListViewModel(
    private val repository: WhatsAppRepository = WhatsAppRepository(),
) : ViewModel() {

    private val _uiState = MutableStateFlow<ChatListUiState>(ChatListUiState.Loading)
    val uiState: StateFlow<ChatListUiState> = _uiState.asStateFlow()

    init {
        load()
    }

    fun retry() = load()

    private fun load() {
        _uiState.value = ChatListUiState.Loading
        viewModelScope.launch {
            _uiState.value = when (val result = repository.chats()) {
                is ChatsResult.Success -> ChatListUiState.Success(result.chats)
                is ChatsResult.Empty -> ChatListUiState.Empty
                is ChatsResult.Error -> ChatListUiState.Error(result.reason)
            }
        }
    }
}
