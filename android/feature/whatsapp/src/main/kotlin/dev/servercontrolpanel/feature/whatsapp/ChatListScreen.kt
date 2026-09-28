package dev.servercontrolpanel.feature.whatsapp

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import coil.compose.AsyncImage
import dev.servercontrolpanel.core.model.WhatsAppChat

@Composable
fun ChatListScreen(
    modifier: Modifier = Modifier,
    onOpenChat: (WhatsAppChat) -> Unit = {},
    viewModel: ChatListViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    var filter by rememberSaveable { mutableStateOf(ChatFilter.ALL) }

    val all = (state as? ChatListUiState.Success)?.chats
    val counts = all?.let(::countsByFilter)

    Column(modifier = modifier.fillMaxSize()) {
        FilterChips(
            selected = filter,
            counts = counts,
            onSelect = { filter = it },
        )
        Box(modifier = Modifier.fillMaxSize()) {
            when (val current = state) {
                is ChatListUiState.Loading -> LoadingContent()
                is ChatListUiState.Error -> ErrorContent(message = current.message, onRetry = viewModel::retry)
                is ChatListUiState.Empty -> EmptyContent()
                is ChatListUiState.Success -> {
                    val visible = filterChats(current.chats, filter)
                    if (visible.isEmpty()) {
                        FilterWithoutResults(filter = filter, onShowAll = { filter = ChatFilter.ALL })
                    } else {
                        ChatListContent(chats = visible, onOpenChat = onOpenChat)
                    }
                }
            }
        }
    }
}

@Composable
private fun FilterWithoutResults(filter: ChatFilter, onShowAll: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(
                text = "No chats in “${filter.label}”",
                style = MaterialTheme.typography.titleMedium,
            )
            Text(
                text = "Your chats are still there — it is just this filter that is empty.",
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = androidx.compose.ui.text.style.TextAlign.Center,
            )
            Button(onClick = onShowAll) { Text(text = "Show all") }
        }
    }
}

@Composable
private fun LoadingContent() {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
private fun ErrorContent(message: String, onRetry: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(text = message, textAlign = androidx.compose.ui.text.style.TextAlign.Center)
            Button(onClick = onRetry, modifier = Modifier.padding(top = 16.dp)) {
                Text("Try again")
            }
        }
    }
}

@Composable
private fun EmptyContent() {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(text = "No chats", style = MaterialTheme.typography.titleMedium)
            Text(
                text = "The server returned no chats. If you expected " +
                    "to see chats here, check that the WhatsApp integration " +
                    "is connected in the panel.",
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = androidx.compose.ui.text.style.TextAlign.Center,
            )
        }
    }
}

@Composable
private fun ChatListContent(chats: List<WhatsAppChat>, onOpenChat: (WhatsAppChat) -> Unit) {
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        items(chats, key = { it.jid }) { chat ->
            ChatRow(chat = chat, onClick = { onOpenChat(chat) })
            HorizontalDivider()
        }
    }
}

@Composable
private fun ChatRow(chat: WhatsAppChat, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        AsyncImage(
            model = chat.avatarUrl,
            contentDescription = null,
            modifier = Modifier
                .size(48.dp)
                .background(MaterialTheme.colorScheme.surfaceVariant, CircleShape),
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = chat.name,
                fontWeight = FontWeight.Bold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            chat.lastMessagePreview?.let { preview ->
                Text(
                    text = preview,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (chat.unread > 0) {
            Surface(
                shape = CircleShape,
                color = MaterialTheme.colorScheme.primary,
            ) {
                Text(
                    text = chat.unread.toString(),
                    color = MaterialTheme.colorScheme.onPrimary,
                    modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp),
                )
            }
        }
    }
}
