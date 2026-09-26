package com.vpsmanager.data.offline

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * When the server last answered.
 *
 * ## Why this has to exist alongside the cache
 *
 * A cache that serves stale data without saying it is stale **lies**. It is the
 * difference between "the disk is at 78%" and "the disk was at 78% at 11:45" —
 * the first sentence, if the data is six hours old, keeps the owner from acting
 * on a disk that has already filled up.
 *
 * This object holds the instant of the last response that came FROM THE NETWORK
 * (never from the cache), and that is what the offline banner shows. Without
 * it, the only honest alternative would be to serve no cache at all — which
 * would return the app to the state of dying without internet.
 *
 * ## Why one stamp for the whole app, and not one per screen
 *
 * A per-screen stamp would be more precise and would require carrying OkHttp's
 * `Response` through the generated client all the way to each `ViewModel` — the
 * client hands back a typed body, not a response, and threading that through
 * would mean touching the generation template and 40 repositories.
 *
 * A global stamp answers the question that really matters when the network
 * drops: *"how long has this app gone without talking to the server?"*. It errs
 * on the safe side — it shows the MOST RECENT contact of any screen, so it
 * never claims a piece of data is newer than it is... except for a screen
 * nobody opened in the meantime. That is why the banner says "last response
 * from the server", and not "this data is from".
 */
object IdadeDoDado {

    private val _ultimaRespostaDaRede = MutableStateFlow<Long?>(null)

    /**
     * Epoch milliseconds of the last response that came off the network, or
     * `null` while none has arrived in this run of the process.
     *
     * `null` is not the same as "a long time ago": right after the app opens
     * there has been no contact at all, and showing a time there would be
     * making it up.
     */
    val ultimaRespostaDaRede: StateFlow<Long?> = _ultimaRespostaDaRede.asStateFlow()

    /** Called by the interceptor on every response that REALLY came off the network. */
    fun registrarRespostaDaRede(agoraMs: Long = System.currentTimeMillis()) {
        _ultimaRespostaDaRede.value = agoraMs
    }

    internal fun reiniciarParaTeste() {
        _ultimaRespostaDaRede.value = null
    }
}
