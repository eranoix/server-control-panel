package com.vpsmanager.core.model

/**
 * A single row in the WhatsApp chat list. Pure domain shape -- the generated
 * OpenAPI client's `ChatSummary` DTO never crosses the `:data` boundary;
 * `com.vpsmanager.data.whatsapp.WhatsAppRepository` maps one into the other.
 * Ordering (pinned chats first, then recency) is server-authoritative --
 * callers preserve list order as received, never re-sort client-side.
 */
data class WhatsAppChat(
    val jid: String,
    val name: String,
    val isGroup: Boolean,
    val unread: Long,
    val avatarUrl: String?,
    val lastMessageAt: Long?,
    val lastMessagePreview: String?,
)
