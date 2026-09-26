package com.vpsmanager.app.nav

/**
 * Whether the Intent [com.vpsmanager.app.MainActivity] is currently holding has already had its
 * notification deep link extras turned into a navigation event.
 *
 * A screen rotation destroys and recreates the activity with the very same Intent — Android does
 * not clear it, and does not call `onNewIntent` again for it — producing a brand new instance of
 * this class in the process. Restoring [consumed] from the activity's saved state (rather than
 * always starting `false`) is what stops that rotation from re-resolving the same extras and
 * jumping back to the notification's screen every time the device is rotated.
 */
internal class DeepLinkConsumptionState(consumed: Boolean = false) {

    var consumed: Boolean = consumed
        private set

    /**
     * Returns [route] the first time it is offered; every call after that — until the next
     * [reset] — returns `null` regardless of what is passed in.
     */
    fun consumeOnce(route: String?): String? {
        if (consumed) return null
        consumed = true
        return route
    }

    /** A genuinely new Intent arrived (`onNewIntent`) — it deserves its own single consumption. */
    fun reset() {
        consumed = false
    }
}
