package com.vpsmanager.feature.auth.painel

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * "Go back to the top of Home."
 *
 * ## The defect this fixes
 *
 * The owner reported: *"when I tap home, it doesn't go to the home page"*. And
 * the navigation was correct — the destination did change. What did not happen
 * was the content going back to the beginning: the drawer navigates with
 * `restoreState = true`, which restores the saved scroll position, so tapping
 * **Home** handed the page back exactly where it had been left, with the
 * "Dashboard" header cut in half under the bar.
 *
 * To whoever tapped, that is not "state restored", it is "the button did not
 * work". "Go home" means the top of the page, not the same page halfway down.
 *
 * ## Why a counter, and not a boolean
 *
 * A boolean would have to be cleared once consumed, and that clearing is one
 * more write racing the read: two quick taps would lose the second. A counter
 * that only grows makes every tap a NEW value, and `LaunchedEffect(value)`
 * reacts to each one without anyone having to clean up.
 *
 * ## Why here and not in a CompositionLocal
 *
 * The drawer does the tapping (`:app`) and Home does the scrolling
 * (`:feature-auth`). The two already talk by parameter for everything else,
 * but this signal crosses a whole navigation — the `NavHost` destroys and
 * recreates the screen on the way — and a parameter does not survive that. A
 * process-wide counter does.
 */
object PedidoDeTopo {

    private val _contador = MutableStateFlow(0)

    /** Grows on every request. Home observes it and scrolls on seeing a new value. */
    val contador: StateFlow<Int> = _contador.asStateFlow()

    fun pedir() {
        _contador.value = _contador.value + 1
    }
}
