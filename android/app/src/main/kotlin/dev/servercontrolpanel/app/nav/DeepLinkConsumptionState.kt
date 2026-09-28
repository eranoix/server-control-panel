package dev.servercontrolpanel.app.nav

internal class DeepLinkConsumptionState(consumed: Boolean = false) {

    var consumed: Boolean = consumed
        private set

    fun consumeOnce(route: String?): String? {
        if (consumed) return null
        consumed = true
        return route
    }

    fun reset() {
        consumed = false
    }
}
