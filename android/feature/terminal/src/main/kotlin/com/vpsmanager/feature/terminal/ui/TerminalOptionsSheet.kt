package com.vpsmanager.feature.terminal.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Icon
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.vpsmanager.feature.terminal.attach.ANEXAR_LABEL
import com.vpsmanager.feature.terminal.attach.ANEXAR_TAG
import com.vpsmanager.feature.terminal.prefs.TerminalLineSpacing
import com.vpsmanager.feature.terminal.power.ISENCAO_BATERIA_LABEL
import com.vpsmanager.feature.terminal.power.ISENCAO_BATERIA_TAG
import com.vpsmanager.feature.terminal.prefs.LinhasVisiveis
import com.vpsmanager.feature.terminal.prefs.ModoDeDigitacao
import com.vpsmanager.feature.terminal.prefs.TerminalScrollback
import com.vpsmanager.feature.terminal.prefs.TerminalFontSizePreference

/** Test tag for the options sheet — the requirement is that it costs no height while closed. */
const val OPTIONS_SHEET_TAG = "folha-opcoes-terminal"

/** Label of the button that opens the sheet, on the top bar. */
const val OPTIONS_DESCRIPTION = "Terminal options"

/** Label of the button that raises the software keyboard without relying on a tap on the grid. */
const val SHOW_KEYBOARD_LABEL = "Show keyboard"

/** Test tag for the sheet's keyboard button. */
const val SHOW_KEYBOARD_TAG = "botao-mostrar-teclado"

/** Label of the sheet's paste button — the path to pasting that does not depend on a selection existing. */
const val COLAR_LABEL = "Paste"

/** Test tag for the sheet's paste button. */
const val COLAR_TAG = "botao-colar"

// THE "CLEAR HISTORY" BUTTON IS GONE, and not as tidying up.
//
// It was born as a remedy for damage that was OURS: the session log was being
// written twice over, with `dtach`'s screen clear inside it, and the replay
// reproduced that. "Clear history" was how a person escaped it.
//
// With the causes fixed — one scribe per session, and the attach clear kept
// out of the log — the button had nothing left to do. And the owner was
// explicit: "I need my history live (...) I am never going to want to erase
// it."
//
// He is right, and the lesson outlives this button: offering "erase
// everything" as the way out of a defect transfers the cost of our mistake to
// the person, and charges them the highest price available — their own work.
// If this ever comes back, it has to come back for a reason of theirs, never
// one of ours.

/** Test tag for each line-spacing step. */
fun entrelinhaTag(passo: TerminalLineSpacing): String = "entrelinha-${passo.name}"

/** Tag for each input mode's chip, for the test that proves the switch. */
fun modoDeDigitacaoTag(modo: ModoDeDigitacao): String = "modo-digitacao-${modo.name}"

/** Tag for each history-size chip. */
fun scrollbackTag(passo: TerminalScrollback): String = "scrollback-${passo.name}"


/**
 * A section header: icon plus title on one line, caption just below.
 *
 * **Why this exists.** Each section of this sheet used to be a
 * `Text(titleMedium)` followed by the controls and a three-line paragraph of
 * explanation. Added up, the paragraphs cost more height than all the controls
 * together, and the whole sheet did not fit on one screen — changing the line
 * spacing meant scrolling past explanations already read a thousand times.
 *
 * The icon does the work the paragraph did: it gives the category at a glance,
 * without spending a line. The caption still exists — information is not noise
 * — but in `bodySmall`, in a secondary colour and kept lean, rather than as a
 * block of text competing with the control.
 */
@Composable
private fun CabecalhoDeSecao(icone: ImageVector, titulo: String, legenda: String? = null) {
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(
                imageVector = icone,
                contentDescription = null,
                modifier = Modifier.size(18.dp),
                tint = MaterialTheme.colorScheme.primary,
            )
            Spacer(modifier = Modifier.width(8.dp))
            Text(text = titulo, style = MaterialTheme.typography.titleSmall)
        }
        if (legenda != null) {
            Text(
                text = legenda,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * The chips of one choice, in a `FlowRow`.
 *
 * A plain `Row` was the defect visible in the operator's screenshot: with four
 * line-spacing steps the last one ("Loose") did not fit and broke IN THE
 * MIDDLE OF THE WORD, becoming "Loo / se". `FlowRow` moves the whole chip to
 * the line below instead of splitting the word.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun <T> EscadaDeChips(
    opcoes: List<T>,
    selecionado: (T) -> Boolean,
    rotulo: (T) -> String,
    tag: (T) -> String,
    onEscolher: (T) -> Unit,
) {
    FlowRow(
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        opcoes.forEach { opcao ->
            FilterChip(
                selected = selecionado(opcao),
                onClick = { onEscolher(opcao) },
                label = { Text(text = rotulo(opcao), maxLines = 1) },
                modifier = Modifier.testTag(tag(opcao)),
            )
        }
    }
}

/**
 * Where the three EPISODIC controls went — the ones that used to live on
 * permanent rows above the grid: font size, drag mode and session history.
 *
 * There is a single criterion: **someone reading command output does not need
 * them on screen**. Font size and line spacing are adjusted once in the app's
 * lifetime; history is pulled when context is missing. Neither is looked at
 * while reading an `ls -l` — and together the three cost 160.8 dp permanently
 * (measured on the emulator at 420 dpi), nearly four lines of shell.
 *
 * A bottom sheet, and not a dropdown on the top bar, for two reasons: it is
 * the surface a thumb reaches on a device held one-handed, and the loaded
 * history needs a LOT of scrollable room — a dropdown cannot hold a block of
 * session text, a sheet can.
 *
 * The mouse selector ("when the program asks for mouse") used to be here and
 * is GONE: it was a preference about a rare case that forced people to
 * understand the terminal's mouse model in order to decide. Today the gesture's
 * owner is derived from the emulator's real state (see `RoteamentoDeToque`),
 * and anyone who needs to select inside an `htop` uses long-press. Line
 * spacing took its place.
 *
 * Closed, the sheet does not exist in the composition: height cost = 0 dp.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TerminalOptionsSheet(
    fontSizeSp: Float,
    onFontSizeChange: (Float) -> Unit,
    gridCols: Int,
    gridRows: Int,
    linhasVisiveis: LinhasVisiveis,
    onLinhasVisiveisChange: (LinhasVisiveis) -> Unit,
    alturaVisivelPx: Int,
    alturaDeReferenciaPx: Int,
    scrollbackLinhas: Int,
    onScrollbackChange: (Int) -> Unit,
    modoDeDigitacao: ModoDeDigitacao,
    onModoDeDigitacaoChange: (ModoDeDigitacao) -> Unit,
    entrelinha: TerminalLineSpacing,
    onEntrelinhaChange: (TerminalLineSpacing) -> Unit,
    onColar: () -> Unit,
    onAnexar: () -> Unit,
    onShowKeyboard: () -> Unit,
    isentoDeBateria: Boolean,
    onPedirIsencaoDeBateria: () -> Unit,
    onDismissRequest: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = onDismissRequest,
        sheetState = sheetState,
        modifier = Modifier.testTag(OPTIONS_SHEET_TAG),
    ) {
        TerminalOptionsContent(
            fontSizeSp = fontSizeSp,
            onFontSizeChange = onFontSizeChange,
            gridCols = gridCols,
            gridRows = gridRows,
            linhasVisiveis = linhasVisiveis,
            onLinhasVisiveisChange = onLinhasVisiveisChange,
            alturaVisivelPx = alturaVisivelPx,
            alturaDeReferenciaPx = alturaDeReferenciaPx,
            scrollbackLinhas = scrollbackLinhas,
            onScrollbackChange = onScrollbackChange,
            modoDeDigitacao = modoDeDigitacao,
            onModoDeDigitacaoChange = onModoDeDigitacaoChange,
            entrelinha = entrelinha,
            onEntrelinhaChange = onEntrelinhaChange,
            onColar = onColar,
            onAnexar = onAnexar,
            onShowKeyboard = onShowKeyboard,
            isentoDeBateria = isentoDeBateria,
            onPedirIsencaoDeBateria = onPedirIsencaoDeBateria,
        )
    }
}

/**
 * The sheet's content, kept apart from the `ModalBottomSheet` wrapper: the
 * wrapper is a system dialog window and the content is what carries the
 * business rules (which controls exist, what each one fires). Separated, the
 * content is exercisable straight from a JVM test without depending on a
 * window's animation.
 */
@Composable
internal fun TerminalOptionsContent(
    fontSizeSp: Float,
    onFontSizeChange: (Float) -> Unit,
    gridCols: Int,
    gridRows: Int,
    linhasVisiveis: LinhasVisiveis,
    onLinhasVisiveisChange: (LinhasVisiveis) -> Unit,
    alturaVisivelPx: Int,
    alturaDeReferenciaPx: Int,
    scrollbackLinhas: Int,
    onScrollbackChange: (Int) -> Unit,
    modoDeDigitacao: ModoDeDigitacao,
    onModoDeDigitacaoChange: (ModoDeDigitacao) -> Unit,
    entrelinha: TerminalLineSpacing,
    onEntrelinhaChange: (TerminalLineSpacing) -> Unit,
    onColar: () -> Unit,
    onAnexar: () -> Unit,
    onShowKeyboard: () -> Unit,
    isentoDeBateria: Boolean,
    onPedirIsencaoDeBateria: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 20.dp)
            .padding(bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(text = "Font size", style = MaterialTheme.typography.titleMedium)
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(modifier = Modifier.weight(1f)) {
                // With "visible lines" pinned, the type size is DERIVED:
                // showing the hand-picked value here would be a lie, and the
                // A−/A+ buttons would move a number that is not in use.
                Text(
                    text = if (linhasVisiveis == LinhasVisiveis.AUTOMATICO) {
                        "${fontSizeSp.toInt()}sp"
                    } else {
                        "derived from ${linhasVisiveis.linhas} lines"
                    },
                )
                // The resulting grid in numbers: this is what you choose the
                // font size by. "Does an `ls -l` fit without wrapping?" is a
                // question about COLUMNS, and before this it had no visible
                // answer anywhere in the app.
                Text(
                    text = "current grid: $gridCols columns × $gridRows rows" +
                        // The reference height only differs from the visible
                        // one when something is covering the grid — keyboard,
                        // connection banner, attachment bar. See
                        // [GeometriaDaGrade].
                        if (alturaDeReferenciaPx != alturaVisivelPx) {
                            "  ·  ${alturaVisivelPx}px of ${alturaDeReferenciaPx}px"
                        } else {
                            ""
                        },
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            OutlinedButton(
                onClick = { onFontSizeChange(fontSizeSp - TerminalFontSizePreference.FONT_SIZE_STEP_SP) },
                enabled = fontSizeSp > TerminalFontSizePreference.MIN_FONT_SIZE_SP,
            ) { Text(text = "A-") }
            OutlinedButton(
                onClick = { onFontSizeChange(fontSizeSp + TerminalFontSizePreference.FONT_SIZE_STEP_SP) },
                enabled = fontSizeSp < TerminalFontSizePreference.MAX_FONT_SIZE_SP,
                modifier = Modifier.padding(start = 8.dp),
            ) { Text(text = "A+") }
        }

        HorizontalDivider()

        CabecalhoDeSecao(
            icone = Icons.Filled.Menu,
            titulo = "Visible lines",
            legenda = if (linhasVisiveis == LinhasVisiveis.AUTOMATICO) {
                "$gridRows fit right now. Pick a number to pin it — the type size adjusts."
            } else {
                "Pinned at ${linhasVisiveis.linhas}. The type size is derived from it."
            },
        )
        EscadaDeChips(
            opcoes = LinhasVisiveis.entries,
            selecionado = { it == linhasVisiveis },
            rotulo = { it.rotulo },
            tag = ::linhasVisiveisTag,
            onEscolher = onLinhasVisiveisChange,
        )

        CabecalhoDeSecao(
            icone = Icons.Filled.List,
            titulo = "Line spacing",
            legenda = "Space between lines, without changing the type size.",
        )
        EscadaDeChips(
            opcoes = TerminalLineSpacing.entries,
            selecionado = { it == entrelinha },
            rotulo = { it.rotulo },
            tag = ::entrelinhaTag,
            onEscolher = onEntrelinhaChange,
        )

        HorizontalDivider()

        // Autocorrect and a terminal want incompatible things, and the choice
        // belongs to the moment, not to the app: a shell command needs every
        // keystroke as typed; writing prose for the agent gains a great deal
        // from the device's corrector. That is why it is a switch and not a
        // decision locked in. See ModoDeDigitacao for the defect this fixes.
        CabecalhoDeSecao(
            icone = Icons.Filled.Edit,
            titulo = "Keyboard",
            legenda = modoDeDigitacao.descricao,
        )
        EscadaDeChips(
            opcoes = ModoDeDigitacao.entries,
            selecionado = { it == modoDeDigitacao },
            rotulo = { it.rotulo },
            tag = ::modoDeDigitacaoTag,
            onEscolher = onModoDeDigitacaoChange,
        )

        HorizontalDivider()

        // How much of the past can be read back. It sits with the keyboard
        // and the line spacing because it is of the same family: how the
        // terminal behaves on this device, not what the session is.
        //
        // The caption says "scroll back and reread", not "keep": this number
        // governs both the emulator's capacity and how much log the app
        // fetches from the server when opening the session. While it was only
        // capacity, it reserved room that was never filled — and promising
        // history that does not arrive is worse than not promising it.
        CabecalhoDeSecao(
            icone = Icons.Filled.Refresh,
            titulo = "Scrollback",
            legenda = "Lines you can scroll back and reread " +
                "(${TerminalScrollback.porLinhas(scrollbackLinhas).custoAproximado} " +
                "on the device). The app fetches this history when it opens the session, " +
                "so it applies from the next time you open it.",
        )
        EscadaDeChips(
            opcoes = TerminalScrollback.entries,
            selecionado = { it.linhas == scrollbackLinhas },
            rotulo = { it.rotulo },
            tag = ::scrollbackTag,
            onEscolher = { onScrollbackChange(it.linhas) },
        )

        HorizontalDivider()

        // The keyboard button had to sit next to the old mouse selector (that
        // selector was what took away the grid's job of raising the keyboard).
        // The selector is gone — a tap only goes to the program when the
        // PROGRAM ITSELF asks for the mouse, and that is nobody's preference
        // any more — but the button stays: it is the escape hatch for someone
        // who closed the keyboard and does not want to tap the grid to bring
        // it back, and the only route to the keyboard inside an `htop`, where
        // a tap on the grid belongs to the program.
        OutlinedButton(onClick = onShowKeyboard, modifier = Modifier.testTag(SHOW_KEYBOARD_TAG)) {
            Text(text = SHOW_KEYBOARD_LABEL)
        }

        // Copy lives on the system's floating bar, which only exists while
        // there is a selection. Paste depends on no selection at all, so it
        // needs a path that always exists — this one.
        OutlinedButton(onClick = onColar, modifier = Modifier.testTag(COLAR_TAG)) {
            Text(text = COLAR_LABEL)
        }

        // Attach sits RIGHT NEXT to Paste because, seen from a distance, it
        // is the same action: putting a reference to something outside the
        // terminal onto the command line. Paste brings it from the clipboard;
        // attach brings it from the device, by way of the server first (the
        // file has to EXIST there for the program on the other side to open it
        // — that is what "give you a reference to look at" demands, and it is
        // what a clipboard cannot solve).
        //
        // And it lives in the SHEET, not on the top bar, by the criterion this
        // sheet already applies: the top bar is for what you look at while
        // reading command output (only the touch mode, which is modal).
        // Attaching is episodic — it happens when you want to show something —
        // and episodic does not pay for permanent height. The sheet is
        // reachable without leaving the session, with a thumb, two taps away;
        // the upload's progress, on the other hand, shows up on the attachment
        // bar glued to the key row, with nothing to reopen.
        OutlinedButton(onClick = onAnexar, modifier = Modifier.testTag(ANEXAR_TAG)) {
            Text(text = ANEXAR_LABEL)
        }


        // The session in the background. The item only exists while the
        // exemption has NOT been granted: once granted it disappears — no
        // going on offering what has already been given. And because it lives
        // in here, behind an explicit tap, the app never asks for anything on
        // its own: whoever declined (or never opened the sheet) simply is not
        // asked again, which is the rule about not nagging.
        if (!isentoDeBateria) {
            HorizontalDivider()

            Text(text = "Session in the background", style = MaterialTheme.typography.titleMedium)
            Text(
                text = "Right now Android cuts this app's network a few seconds after you " +
                    "leave the screen, which is why the session reconnects when you come back.",
                style = MaterialTheme.typography.bodySmall,
            )
            OutlinedButton(
                onClick = onPedirIsencaoDeBateria,
                modifier = Modifier.testTag(ISENCAO_BATERIA_TAG),
            ) {
                Text(text = ISENCAO_BATERIA_LABEL)
            }
        }

    }
}

/** Test tag for each visible-lines step. */
internal fun linhasVisiveisTag(opcao: LinhasVisiveis): String = "linhas-visiveis-${opcao.linhas}"
