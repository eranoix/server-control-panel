package com.vpsmanager.feature.jira

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.vpsmanager.data.jira.CartaoDoJira
import java.time.LocalDate
import java.time.OffsetDateTime

/**
 * The colour of a card's edge band, taken from the status CATEGORY.
 *
 * Category and not status name: there are three values Jira guarantees
 * (`new`/`indeterminate`/`done`), while the name itself changes from project
 * to project. One colour per name would hand every workflow a new palette.
 *
 * None of them is the panel's green or red for STATE (`vpsmStatusColors`), and
 * that is a rule: here the colour says "what stage this is at", not "this is
 * fine or broken". A red card because it is in review, sitting beside a red
 * alert because the disk filled up, would destroy the only language the app
 * has for saying urgency.
 */
@Composable
internal fun corDaCategoria(categoria: String): Color = when (categoria) {
    "done" -> Color(0xFF5E9E76)
    "indeterminate" -> Color(0xFF3BA9B4)
    "new" -> Color(0xFF7C8794)
    else -> MaterialTheme.colorScheme.outline
}

/**
 * A board card — in the version that fits a third of the screen.
 *
 * ## Why it shrank
 *
 * The requirement is seeing all three columns at once, and on a 411 dp phone
 * that leaves **about 125 dp per column**. It is not a matter of taste: it is
 * what is left. At that width, "Medium · a11y · frontend · +2 · Sam Rivera ·
 * Backlog" is not information, it is a stack of two-word fragments.
 *
 * So the card answers only what you actually ask while LOOKING at the board:
 * **which one** (the key), **what it is** (three lines of summary), **whose it
 * is** (the initials) and **how long it has been sitting**. Priority, labels,
 * type, the spelled-out status, the due date and the assignee's full name all
 * still exist — on the detail sheet, one tap away. It is the same trade any
 * board makes when it zooms out: less per card, more cards in view.
 *
 * ## A single description, for the screen reader
 *
 * The card is one target, and TalkBack reads the whole of it at once. And here
 * the description is deliberately RICHER than what is visible: someone
 * listening does not lose what the width took from the eye — the sentence
 * carries priority, type and state, which the compact card does not show.
 */
@Composable
internal fun CartaoDoQuadro(
    cartao: CartaoDoJira,
    modifier: Modifier = Modifier,
    selecionando: Boolean = false,
    selecionado: Boolean = false,
    aoSelecionar: () -> Unit = {},
    agora: OffsetDateTime = OffsetDateTime.now(),
    hoje: LocalDate = LocalDate.now(),
) {
    val idade = quandoFoi(cartao.atualizada, agora)
    val vence = vencimento(cartao.vence, hoje)
    val faixa = corDaCategoria(cartao.categoria)

    Card(
        modifier = modifier
            .fillMaxWidth()
            .semantics { contentDescription = descricaoDoCartao(cartao, idade, vence) },
        colors = CardDefaults.cardColors(
            containerColor = if (selecionado) {
                MaterialTheme.colorScheme.secondaryContainer
            } else {
                MaterialTheme.colorScheme.surfaceContainerHigh
            },
        ),
        shape = RoundedCornerShape(10.dp),
    ) {
        Column(
            modifier = Modifier
                // The band is drawn, not a sibling Box: a Box with a height
                // of its own would demand `IntrinsicSize.Min` on the row, which
                // remeasures the subtree on every frame of the drag animation.
                .drawBehind { drawRect(color = faixa, size = Size(3.dp.toPx(), size.height)) }
                .padding(start = 10.dp, top = 8.dp, end = 8.dp, bottom = 8.dp)
                .fillMaxWidth(),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = cartao.chave,
                    style = MaterialTheme.typography.labelMedium,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.primary,
                    maxLines = 1,
                    overflow = TextOverflow.Clip,
                )
                Spacer(Modifier.weight(1f))
                if (selecionando) {
                    // The checkbox gives way to the age instead of
                    // squeezing both: at this width, together they push the key.
                    Checkbox(
                        checked = selecionado,
                        onCheckedChange = { aoSelecionar() },
                        modifier = Modifier.size(20.dp),
                    )
                } else if (vence == "venceu") {
                    // Overdue is the ONLY badge that survived the cut:
                    // it is the one that changes what you do with the card today.
                    Text(
                        text = "!",
                        style = MaterialTheme.typography.labelMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }

            Spacer(Modifier.height(4.dp))
            Text(
                text = cartao.resumo,
                // 12sp and not the theme's bodyMedium: at 125 dp the default
                // body fits four words per line and the summary becomes ellipsis.
                fontSize = 12.sp,
                lineHeight = 15.sp,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 3,
                overflow = TextOverflow.Ellipsis,
            )

            Spacer(Modifier.height(6.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(18.dp)
                        .clip(CircleShape)
                        .background(
                            if (cartao.responsavel == null) {
                                MaterialTheme.colorScheme.surfaceVariant
                            } else {
                                MaterialTheme.colorScheme.primaryContainer
                            },
                        ),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = if (cartao.responsavel == null) "–" else iniciais(cartao.responsavel),
                        fontSize = 9.sp,
                        color = if (cartao.responsavel == null) {
                            MaterialTheme.colorScheme.onSurfaceVariant
                        } else {
                            MaterialTheme.colorScheme.onPrimaryContainer
                        },
                    )
                }
                Spacer(Modifier.width(6.dp))
                Text(
                    text = idade,
                    fontSize = 10.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Clip,
                )
            }
        }
    }
}

/**
 * The sentence the screen reader speaks in place of the card.
 *
 * Assembled in the order the information is looked for: what it is, what state
 * it is in, who has it, and how long it has been sitting. It carries what the
 * compact card had to take away from the eye — a listener does not pay the
 * price of the column's width.
 */
internal fun descricaoDoCartao(cartao: CartaoDoJira, idade: String, vence: String): String = buildString {
    append(cartao.chave)
    append(", ")
    append(cartao.resumo)
    append(". Status: ")
    append(cartao.status)
    cartao.tipo?.takeIf { it.isNotBlank() }?.let { append(". Type: $it") }
    cartao.prioridade?.takeIf { it.isNotBlank() }?.let { append(". Priority: $it") }
    if (cartao.rotulos.isNotEmpty()) append(". Labels: ${cartao.rotulos.joinToString(", ")}")
    append(". ")
    append(cartao.responsavel?.let { "Assignee: $it" } ?: "Unassigned")
    if (idade.isNotEmpty()) append(". Updated $idade")
    if (vence.isNotEmpty()) append(". $vence")
}
