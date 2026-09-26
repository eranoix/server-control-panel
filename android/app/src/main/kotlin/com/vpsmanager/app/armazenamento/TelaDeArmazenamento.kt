package com.vpsmanager.app.armazenamento

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.vpsmanager.data.armazenamento.ArmazenamentoDoApp

const val TAG_ARMAZENAMENTO = "tela-armazenamento"
const val TAG_LIBERAR_ESPACO = "botao-liberar-espaco"

/**
 * What the app takes up, item by item, and the button that gives the space
 * back.
 *
 * ## Why this screen exists, and not just the routine
 *
 * The automatic cleanup ([ManutencaoWorker]) handles the build-up, but it
 * is invisible: nobody can tell whether it ran or what it keeps. When the
 * only visible tool is "clear storage" in Android's settings, any oddity
 * turns into wiping everything — and the preferences, the configured
 * server and the session go with it. That is how the owner once lost a
 * whole night.
 *
 * So the screen has two obligations and no decoration: say **how much** and
 * **of what**, and offer a button whose limits are clear. The line in the
 * footer is not legal boilerplate — it is the difference between this
 * button and the system's.
 *
 * ## Why the list shows even what the routine does not delete
 *
 * Cache and media prune themselves and the routine does not touch them.
 * Hiding them would make the screen's total disagree with the number
 * Android shows, and a number that does not add up destroys trust in the
 * whole screen.
 */
@Composable
internal fun TelaDeArmazenamento(
    usos: List<ArmazenamentoDoApp.Uso>,
    onLiberarEspaco: () -> Unit,
    ultimoResultado: ArmazenamentoDoApp.Resultado?,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(20.dp)
            .testTag(TAG_ARMAZENAMENTO),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        val total = usos.sumOf { it.bytes }
        Text(
            text = ArmazenamentoDoApp.formatar(total),
            style = MaterialTheme.typography.headlineMedium,
        )
        Text(
            text = "in data the app can download again",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )

        HorizontalDivider(Modifier.padding(vertical = 16.dp))

        usos.forEach { uso ->
            Row(
                modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp),
                verticalAlignment = Alignment.Top,
                horizontalArrangement = Arrangement.SpaceBetween,
            ) {
                Column(Modifier.weight(1f).padding(end = 16.dp)) {
                    Text(uso.nome, style = MaterialTheme.typography.bodyLarge)
                    Text(
                        uso.explicacao,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    ArmazenamentoDoApp.formatar(uso.bytes),
                    style = MaterialTheme.typography.bodyLarge,
                )
            }
        }

        HorizontalDivider(Modifier.padding(vertical = 16.dp))

        Button(
            onClick = onLiberarEspaco,
            modifier = Modifier.testTag(TAG_LIBERAR_ESPACO),
        ) {
            Text("Free up space now")
        }

        // The result comes back in bytes, and not as "done!": whoever pressed it
        // wants to know whether it was worth it. "0 B" is an honest and useful
        // answer — it says the problem was not here, and saves pressing again.
        ultimoResultado?.let {
            Text(
                text = "Freed ${ArmazenamentoDoApp.formatar(it.liberadoBytes)} " +
                    "across ${it.arquivosRemovidos} file(s).",
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.padding(top = 12.dp),
            )
        }

        Text(
            text = "Preferences, the configured server, the session and anything queued " +
                "for upload are never deleted here. The app also does this " +
                "cleanup on its own, once a day.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 20.dp),
        )
    }
}
