package com.vpsmanager.feature.auth.painel

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Build
import androidx.compose.material.icons.filled.DateRange
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Settings
import androidx.compose.ui.graphics.vector.ImageVector
import com.vpsmanager.designsystem.VpsmIcons

/**
 * A dashboard block's icon.
 *
 * ## Why a block needs an icon
 *
 * Blocks are read at a glance, at arm's length, and their label is one word in
 * small caps — "MEMORY", "SWAP", "DISK /". On a grid of eight, the eye has to
 * run through eight words to find the one that matters. A symbol to the left
 * of the label is recognised before it is read, and that is what turns the
 * grid into a dashboard rather than a list with big numbers.
 *
 * ## The icon does NOT replace the label
 *
 * It sits beside it, never in its place. A symbol on its own is guesswork — a
 * stopwatch could be "uptime" or "scheduled jobs", and only the text settles
 * the difference. For the same reason the icon carries no description for
 * screen readers: the label is already there and is what should be announced.
 *
 * ## Matched by id, from specific to generic
 *
 * A block's id comes from the resource judgement (`cpu`, `memoria`, `swap`,
 * `disco:/mnt/x`) or from the aggregates. `disco:` matches by prefix because
 * there is one block per mount point, and they all deserve the same symbol.
 */
internal fun iconeDoBloco(id: String): ImageVector = when {
    id == "cpu" || id == "steal" || id == "iowait" -> VpsmIcons.Velocimetro
    id == "memoria" -> VpsmIcons.Memoria
    id == "swap" -> VpsmIcons.Troca
    id.startsWith("disco") -> VpsmIcons.Disco
    id == "rede" -> VpsmIcons.Troca
    id == BLOCO_FILA -> Icons.Filled.List
    id == BLOCO_SAUDE -> VpsmIcons.Saude
    id == BLOCO_DEPLOYS -> VpsmIcons.Entrega
    id == BLOCO_AGENDADOS -> Icons.Filled.DateRange
    id == BLOCO_UPTIME -> VpsmIcons.Cronometro
    id.startsWith("alerta:") -> Icons.Filled.Build
    else -> Icons.Filled.Settings
}
