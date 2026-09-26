package com.vpsmanager.feature.admin

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.ui.graphics.Color

/**
 * The colour of a family of sections.
 *
 * ## Why the grid needed this
 *
 * The owner sent a photo of thirty blocks: all with the same background, the
 * same circle, the same colour. A grid like that has no **relief** — the eye
 * cannot jump to "the Docker part" or "the Security part", because nothing
 * groups visually what the text already groups in words.
 *
 * The group label was there, written under each block in tiny type. But
 * reading thirty labels to find a family is exactly the work colour does for
 * free.
 *
 * ## The colour belongs to the GROUP, never to the state
 *
 * A rule that cannot be broken: here the colour is **taxonomic** — it says
 * which family the section belongs to. It may not mean "this is fine" or "this
 * has a problem", which is why none of these is the green or the red of state
 * (`vpsmStatusColors`). A red block because it is Security, beside a red block
 * because the disk filled up, would destroy the only language the panel has
 * for saying urgency.
 *
 * ## An unknown group does not invent a colour
 *
 * The server may bring a new group tomorrow — that is the promise of SDUI. A
 * group this map does not know falls back to the theme's neutral, and stays
 * legible. Adding hash-generated colours would give a random palette whose
 * meaning shifts with every new section.
 */
@Composable
@ReadOnlyComposable
internal fun corDoGrupo(grupo: String): Color {
    val esquema = MaterialTheme.colorScheme
    return when (grupo.lowercase().trim()) {
        // Blue: the machine underneath — what it is and how it is doing.
        "system", "sistema" -> Color(0xFF4F8FD9)
        // Cyan: what runs ON TOP of the machine.
        "docker" -> Color(0xFF3BA9B4)
        // Amber: who gets in, what is kept, what is audited. Amber and not
        // red on purpose — see the KDoc: red is urgency, not family.
        "security", "segurança", "seguranca" -> Color(0xFFC98A2E)
        // Violet: what happens on its own.
        "automation", "automação", "automacao" -> Color(0xFF8B72D0)
        // Moss green: what talks to the outside. Desaturated so it is not
        // mistaken for the green of "everything is fine".
        "integrations", "integrações", "integracoes" -> Color(0xFF5E9E76)
        else -> esquema.primary
    }
}
