package com.vpsmanager.feature.whatsapp

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.vpsmanager.data.whatsapp.OkHttpWhatsAppWsFactory
import com.vpsmanager.data.whatsapp.resolveWhatsAppWsUrl
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob

private const val ROUTE_CHAT_LIST = "chatList"
private const val ROUTE_CONVERSATION = "conversation/{jid}"
private const val ARG_JID = "jid"

/**
 * The `Whatsapp` bottom-nav destination's own internal navigation: chat list
 * to conversation-by-jid, and back. This mirrors every other feature module
 * (a single composable per [com.vpsmanager.app.nav.AppNavHost] destination,
 * with no separate top-level route) while still giving the chat list ->
 * conversation transition its own nested [NavHost] and back stack.
 *
 * Window insets are handled exactly once, in [com.vpsmanager.app.nav.AppNavHost]'s
 * own [NavHost] modifier -- this nested host applies none of its own.
 */
@Composable
fun WhatsAppRoute(modifier: Modifier = Modifier) {
    val navController = rememberNavController()

    NavHost(navController = navController, startDestination = ROUTE_CHAT_LIST, modifier = modifier) {
        composable(ROUTE_CHAT_LIST) {
            ChatListScreen(
                onOpenChat = { chat ->
                    navController.navigate("conversation/${chat.jid}")
                },
            )
        }
        composable(
            route = ROUTE_CONVERSATION,
            arguments = listOf(navArgument(ARG_JID) { type = NavType.StringType }),
        ) { backStackEntry ->
            val jid = backStackEntry.arguments?.getString(ARG_JID).orEmpty()
            val viewModel: ConversationViewModel = viewModel(
                key = jid,
                factory = conversationViewModelFactory(jid),
            )
            ConversationScreen(viewModel = viewModel)
        }
    }
}

/**
 * Builds a [ConversationViewModel] wired to a real [WhatsAppWsClient] over
 * `/ws/whatsapp`. [resolveWhatsAppWsUrl] returns null until some future
 * login/config screen sets an absolute base URL on the shared `ApiClient` --
 * today's actual default (a relative `/api/mobile/v1`) means the WS client
 * is constructed but never manages to open a real connection, exactly like
 * every other network call in the app right now.
 */
private fun conversationViewModelFactory(jid: String) = viewModelFactory {
    initializer {
        val wsBaseUrl = resolveWhatsAppWsUrl().orEmpty()
        val scope = CoroutineScope(SupervisorJob())
        val eventSource = WhatsAppWsClient(
            webSocketFactory = OkHttpWhatsAppWsFactory(),
            wsBaseUrl = wsBaseUrl,
            scope = scope,
        )
        ConversationViewModel(jid = jid, eventSource = eventSource)
    }
}
