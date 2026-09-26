package dev.servercontrolpanel.feature.admin

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.ui.graphics.Color

/**
 * The color of a section group, so the grid groups families visually.
 *
 * Colors are taxonomic only and must never look like status colors
 * (`panelStatusColors`): red and bright green are reserved for urgency and health.
 * Unknown groups fall back to the theme primary rather than a generated color.
 */
@Composable
@ReadOnlyComposable
internal fun groupColor(group: String): Color {
    val scheme = MaterialTheme.colorScheme
    return when (group.lowercase().trim()) {
        // Blue: the machine itself.
        "system", "sistema" -> Color(0xFF4F8FD9)
        // Cyan: what runs on the machine.
        "docker" -> Color(0xFF3BA9B4)
        // Amber, not red: red means urgency.
        "security", "segurança", "seguranca" -> Color(0xFFC98A2E)
        // Violet: things that run on their own.
        "automation", "automação", "automacao" -> Color(0xFF8B72D0)
        // Desaturated green, so it is not read as "healthy".
        "integrations", "integrações", "integracoes" -> Color(0xFF5E9E76)
        else -> scheme.primary
    }
}
