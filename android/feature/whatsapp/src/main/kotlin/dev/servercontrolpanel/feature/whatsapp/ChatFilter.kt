package dev.servercontrolpanel.feature.whatsapp

import dev.servercontrolpanel.core.model.WhatsAppChat

enum class ChatFilter(val label: String) {
    ALL("All"),
    UNREAD("Unread"),
    GROUPS("Groups"),
    PEOPLE("People"),
    ;

    fun accepts(chat: WhatsAppChat): Boolean = when (this) {
        ALL -> true
        UNREAD -> chat.unread > 0
        GROUPS -> chat.isGroup
        PEOPLE -> !chat.isGroup
    }
}

fun countsByFilter(chats: List<WhatsAppChat>): Map<ChatFilter, Int> =
    ChatFilter.entries.associateWith { filter -> chats.count(filter::accepts) }

fun filterChats(chats: List<WhatsAppChat>, filter: ChatFilter): List<WhatsAppChat> =
    if (filter == ChatFilter.ALL) chats else chats.filter(filter::accepts)
