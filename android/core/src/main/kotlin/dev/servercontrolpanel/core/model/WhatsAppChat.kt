package dev.servercontrolpanel.core.model

data class WhatsAppChat(
    val jid: String,
    val name: String,
    val isGroup: Boolean,
    val unread: Long,
    val avatarUrl: String?,
    val lastMessageAt: Long?,
    val lastMessagePreview: String?,
)
