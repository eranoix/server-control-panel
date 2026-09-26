package com.vpsmanager.feature.whatsapp

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp

/**
 * The row of filter chips above the conversation list.
 *
 * ## The rule that decides the design: the number is mandatory
 *
 * A chip with no count turns two different states into the same screen. With
 * the number, "Unread 0" and "Unread 12" say, before any tap at all, whether
 * filtering is worth it — and an empty list after the tap stops being a
 * mystery.
 *
 * ## And the dash when it is not known
 *
 * A null [contagens] means **I do not know** — the list failed, or is still
 * loading. There the chip shows `—`, never `0`. The difference is not
 * cosmetic: `0` is a CLAIM ("there are no unread conversations") that the app
 * is in no position to make when the answer has not arrived. It is the same
 * rule as the offline banner, applied somewhere smaller: data you do not have
 * does not become a number, it becomes a dash.
 *
 * With no counts the chips are also **disabled**: filtering a list that never
 * loaded produces another empty list, and one more empty state to explain.
 */
@Composable
internal fun ChipsDeFiltro(
    selecionado: FiltroDeConversas,
    contagens: Map<FiltroDeConversas, Int>?,
    aoSelecionar: (FiltroDeConversas) -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = 12.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        FiltroDeConversas.entries.forEach { filtro ->
            val n = contagens?.get(filtro)
            val numero = n?.toString() ?: "—"
            FilterChip(
                selected = filtro == selecionado,
                onClick = { aoSelecionar(filtro) },
                enabled = contagens != null,
                label = { Text(text = "${filtro.rotulo} $numero") },
                colors = FilterChipDefaults.filterChipColors(),
                modifier = Modifier.semantics {
                    // The screen reader gets the whole phrase: "Unread, 12
                    // conversations". Without this it would read "Unread 12",
                    // which sounds like the name of a filter called "12".
                    contentDescription = when (n) {
                        null -> "${filtro.rotulo}, count unavailable"
                        1 -> "${filtro.rotulo}, 1 chat"
                        else -> "${filtro.rotulo}, $n chats"
                    }
                },
            )
        }
    }
}
