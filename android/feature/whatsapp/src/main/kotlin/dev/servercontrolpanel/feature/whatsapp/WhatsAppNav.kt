package dev.servercontrolpanel.feature.whatsapp

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
import dev.servercontrolpanel.data.whatsapp.OkHttpWhatsAppWsFactory
import dev.servercontrolpanel.data.whatsapp.resolveWhatsAppWsUrl
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob

private const val ROUTE_CHAT_LIST = "chatList"
private const val ROUTE_CONVERSATION = "conversation/{jid}"
private const val ARG_JID = "jid"

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
