package dev.servercontrolpanel.feature.admin

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.ui.graphics.Color

@Composable
@ReadOnlyComposable
internal fun groupColor(group: String): Color {
    val scheme = MaterialTheme.colorScheme
    return when (group.lowercase().trim()) {
        "system" -> Color(0xFF4F8FD9)
        "docker" -> Color(0xFF3BA9B4)
        "security" -> Color(0xFFC98A2E)
        "automation" -> Color(0xFF8B72D0)
        "integrations" -> Color(0xFF5E9E76)
        else -> scheme.primary
    }
}
