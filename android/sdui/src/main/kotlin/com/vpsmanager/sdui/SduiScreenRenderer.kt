package com.vpsmanager.sdui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.vpsmanager.core.sdui.SduiEnvelope
import com.vpsmanager.sdui.actionrunner.ActionOutcome
import com.vpsmanager.sdui.registry.RenderComponent

/**
 * The one screen-level composable `:sdui` exports. A feature module gets an
 * already-parsed [envelope] (via `com.vpsmanager.core.sdui.parseScreen`) and
 * renders it end to end — no other public entry point into this module's
 * rendering is needed.
 *
 * Components are laid out in a [LazyColumn], keyed by their own `id` (stable
 * across recompositions triggered by a screen refresh) — this also satisfies
 * A large component list is never all composed/measured at once.
 *
 * Deliberately does not touch window insets: `AppNavHost` applies
 * `imePadding()`/`consumeWindowInsets` exactly once at the nav-host level; a
 * feature screen hosting [SduiScreen] must not re-apply them here.
 *
 * [onOutcome]/[refreshKey] are optional, additive to every existing caller:
 * [onOutcome] bubbles every [ActionOutcome] dispatched anywhere on this
 * screen up to whoever hosts it; [refreshKey] flows back down so a host that
 * reacted to one (bumping [refreshKey]) can make read components (`Table`)
 * re-pull their own rows. See [RenderComponent]'s own doc comment for why
 * neither can be inferred purely from [com.vpsmanager.sdui.actionrunner.ScreenState].
 */
@Composable
fun SduiScreen(
    envelope: SduiEnvelope,
    modifier: Modifier = Modifier,
    onOutcome: (ActionOutcome) -> Unit = {},
    refreshKey: Any? = null,
) {
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        items(
            items = envelope.screen.components,
            key = { it.id },
        ) { component ->
            RenderComponent(component, onOutcome, refreshKey)
        }
    }
}
