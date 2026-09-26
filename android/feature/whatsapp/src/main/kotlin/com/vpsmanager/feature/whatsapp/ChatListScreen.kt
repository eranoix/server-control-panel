package com.vpsmanager.feature.whatsapp

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
import com.vpsmanager.core.model.WhatsAppChat

/**
 * Renders [ChatListUiState] as it comes back from [ChatListViewModel], which
 * in turn calls the generated mobile BFF client via
 * `WhatsAppRepository.chats()`. Every state below is real, distinct UI -- a
 * failed load, an empty inbox and a loading spinner never look the same as
 * each other or as a blank screen.
 */
@Composable
fun ChatListScreen(
    modifier: Modifier = Modifier,
    onOpenChat: (WhatsAppChat) -> Unit = {},
    viewModel: ChatListViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    var filtro by rememberSaveable { mutableStateOf(FiltroDeConversas.TODAS) }

    val todas = (state as? ChatListUiState.Success)?.chats
    val contagens = todas?.let(::contagensPorFiltro)

    Column(modifier = modifier.fillMaxSize()) {
        // The chips live OUTSIDE the `when`: they exist in every state, with a
        // dash in place of the number while it is not yet known. Making them
        // vanish on error would make the bar appear and disappear on every
        // reload, and a bar that flickers is a bar nobody trusts.
        ChipsDeFiltro(
            selecionado = filtro,
            contagens = contagens,
            aoSelecionar = { filtro = it },
        )
        Box(modifier = Modifier.fillMaxSize()) {
            when (val current = state) {
                is ChatListUiState.Loading -> LoadingContent()
                is ChatListUiState.Error -> ErrorContent(message = current.message, onRetry = viewModel::retry)
                is ChatListUiState.Empty -> EmptyContent()
                is ChatListUiState.Success -> {
                    val visiveis = filtrarConversas(current.chats, filtro)
                    if (visiveis.isEmpty()) {
                        // THREE different empty states, three different
                        // screens. This is the FILTER one: there are chats, it
                        // is the slice that has none. Showing the same "no chats
                        // yet" sentence here would repeat this screen's most
                        // expensive defect.
                        FiltroSemResultado(filtro = filtro, aoVerTodas = { filtro = FiltroDeConversas.TODAS })
                    } else {
                        ChatListContent(chats = visiveis, onOpenChat = onOpenChat)
                    }
                }
            }
        }
    }
}

/**
 * The emptiness of a FILTER — never confused with the emptiness of the inbox.
 *
 * The sentence names the filter that hid everything and the button undoes the
 * slice. Without that, whoever tapped "Unread" on a fully read inbox finds a
 * blank screen and concludes the list is broken.
 */
@Composable
private fun FiltroSemResultado(filtro: FiltroDeConversas, aoVerTodas: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(
                text = "No chats in “${filtro.rotulo}”",
                style = MaterialTheme.typography.titleMedium,
            )
            Text(
                text = "Your chats are still there — it is just this filter that is empty.",
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = androidx.compose.ui.text.style.TextAlign.Center,
            )
            Button(onClick = aoVerTodas) { Text(text = "Show all") }
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
            // THE SECOND SENTENCE IS THE FIX FOR A REAL DEFECT. This screen
            // has said "No chats yet" with the WhatsApp bridge DOWN on the
            // server: an assertion about the inbox when what there really was
            // was the absence of any answer about it. The server replied with
            // an empty list; from here the app has no way to tell "there are no
            // chats" from "the integration is not up" — so it says both, rather
            // than picking the wrong one.
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
                    // `onPrimary`, never a hard-coded white: in the DARK theme
                    // Material 3's `primary` is a LIGHT lilac, and the number in
                    // white over it all but disappeared. The right partner for a
                    // background is the colour the scheme itself names for it.
                    color = MaterialTheme.colorScheme.onPrimary,
                    modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp),
                )
            }
        }
    }
}
