package com.vpsmanager.feature.whatsapp

import com.vpsmanager.core.model.WhatsAppChat

/**
 * The filters on the conversation list.
 *
 * ## Why only four, and why these
 *
 * A chip only pays for itself when it answers a question someone actually asks
 * on opening the screen. The four questions are: *everything*, *what is left
 * to read*, *which are groups* and *which are people*. A fifth chip
 * ("archived", "muted") would need data the BFF does not send today — and a
 * filter that does not really filter is worse than none.
 *
 * The order is fixed and [ALL] comes first because it is the initial state:
 * a selected chip that shows up in the middle of the row makes people hunt for
 * where it is.
 */
enum class ChatFilter(val label: String) {
    ALL("All"),
    UNREAD("Unread"),
    GROUPS("Groups"),
    PEOPLE("People"),
    ;

    /** Whether [chat] belongs in this filter. */
    fun accepts(chat: WhatsAppChat): Boolean = when (this) {
        ALL -> true
        UNREAD -> chat.unread > 0
        GROUPS -> chat.isGroup
        PEOPLE -> !chat.isGroup
    }
}

/**
 * How many conversations each filter would find — the information without
 * which the chips lie.
 *
 * ## The defect this exists not to repeat
 *
 * Without a count, an empty filter and an empty inbox produce **the same
 * screen**. This app has already paid for that confusion in a worse form: the
 * list said *"No conversations yet"* when the real problem was the WhatsApp
 * bridge being down on the server — three different states (nothing yet,
 * filter with no results, integration offline) sharing one sentence. A number
 * beside the label solves two of the three for free: with `Unread 0` on
 * screen, nobody confuses "there is nothing to read" with "the list did not
 * load".
 *
 * The counts are over ALL loaded conversations, never over the active filter's
 * result — otherwise every unselected chip would show zero, which is exactly
 * the wrong information.
 */
fun countsByFilter(chats: List<WhatsAppChat>): Map<ChatFilter, Int> =
    ChatFilter.entries.associateWith { filter -> chats.count(filter::accepts) }

/** Applies [filter], preserving the order the server sent. */
fun filterChats(chats: List<WhatsAppChat>, filter: ChatFilter): List<WhatsAppChat> =
    if (filter == ChatFilter.ALL) chats else chats.filter(filter::accepts)
