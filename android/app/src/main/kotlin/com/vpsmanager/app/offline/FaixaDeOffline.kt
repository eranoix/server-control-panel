package com.vpsmanager.app.offline

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.vpsmanager.data.offline.EstadoDaRede
import com.vpsmanager.data.offline.FilaDeEnvio
import com.vpsmanager.data.offline.IdadeDoDado
import com.vpsmanager.data.offline.RedeDoAparelho
import com.vpsmanager.designsystem.vpsmStatusColors
import java.util.Locale

/** Text of the banner when there has been no contact at all in this run. */
internal const val OFFLINE_SEM_CONTATO = "Offline — nothing has loaded yet"

/**
 * The banner that stops the cache from lying.
 *
 * ## Why it is mandatory, and not decoration
 *
 * The read cache keeps the app opening without internet. On its own, it
 * creates a new problem worse than the one it solves: the screen starts
 * claiming, with its usual face, that the disk is at 78% — when that
 * number may be six hours old and the disk may have filled up since.
 * **Stale data with no label is worse than an error screen**, because an
 * error screen never stops anyone from acting.
 *
 * This banner is the label. It appears **only** when there is no validated
 * network, says how long ago the server's last response was, and
 * disappears on its own when the connection comes back — requiring no tap,
 * because there is nothing to decide.
 *
 * ## Why "the server's last response", and not "this data is from"
 *
 * The timestamp belongs to the whole app, not to this screen (see
 * [IdadeDoDado]). Saying "this data is from 11:45" would be more precise
 * than what is actually known: another screen may have talked to the
 * server since. The sentence chosen is true in every case.
 *
 * ## Why yellow, and not red
 *
 * There is no failure: the server is fine, the app is working and showing
 * what it has. Red would teach people to ignore red.
 */
@Composable
fun FaixaDeOffline(modifier: Modifier = Modifier) {
    val estado by RedeDoAparelho.estado.collectAsStateWithLifecycle()
    val ultima by IdadeDoDado.ultimaRespostaDaRede.collectAsStateWithLifecycle()
    val pendentes by FilaDeEnvio.pendentes.collectAsStateWithLifecycle()
    val recusadas by FilaDeEnvio.recusadas.collectAsStateWithLifecycle()
    val warning = vpsmStatusColors.warning

    // The banner also shows WITH network when there is a queued or refused
    // action: the queue exists to carry across the return of the connection,
    // and hiding it the instant Wi-Fi comes back would hide exactly the moment
    // it does its work.
    val visivel = estado == EstadoDaRede.OFFLINE || pendentes.isNotEmpty() || recusadas.isNotEmpty()

    AnimatedVisibility(visible = visivel, modifier = modifier) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(warning.container)
                .padding(horizontal = 16.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = textoDaFila(pendentes.size, recusadas.map { it.descricao })
                    ?: textoDaFaixa(ultima, System.currentTimeMillis()),
                color = warning.content,
                style = MaterialTheme.typography.bodySmall,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/**
 * The sentence, split from the drawing so it can be pinned by an ordinary
 * test.
 *
 * The unit follows the age: seconds interest nobody, and "320 minutes ago"
 * forces you to do arithmetic. Past a day the number stops being useful
 * and what matters is "this is old".
 */
internal fun textoDaFaixa(ultimaMs: Long?, agoraMs: Long): String {
    if (ultimaMs == null) return OFFLINE_SEM_CONTATO
    val minutos = ((agoraMs - ultimaMs) / 60_000L).coerceAtLeast(0)
    val quando = when {
        minutos < 1 -> "just now"
        minutos < 60 -> "$minutos min ago"
        minutos < 60 * 24 -> String.format(Locale.forLanguageTag("pt-BR"), "%d h ago", minutos / 60)
        else -> "over a day ago"
    }
    return "Offline — last server response $quando"
}

/**
 * The sentence about the QUEUE, when there is something to say about it —
 * null when there is not, and then the banner goes back to talking only
 * about the connection.
 *
 * The refusal comes first and alone because it changes what the person
 * does: a "waiting" action is going to happen, a refused one is NOT, and
 * whoever needs to redo something is whoever got refused. Before this the
 * discard was silent — the action left the queue after a 4xx and the
 * person went on believing it would happen.
 */
internal fun textoDaFila(pendentes: Int, recusadas: List<String>): String? = when {
    recusadas.isNotEmpty() -> {
        val quais = recusadas.take(2).joinToString("; ")
        val resto = recusadas.size - 2
        val cauda = if (resto > 0) " (and $resto more)" else ""
        "The server refused: $quais$cauda — redo it if you still want it."
    }
    pendentes == 1 -> "1 action waiting for the internet"
    pendentes > 1 -> "$pendentes actions waiting for the internet"
    else -> null
}
