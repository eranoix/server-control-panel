package com.vpsmanager.feature.videocall.call

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

private class FakeFullScreenIntentPort(
    private var canUse: Boolean,
    private var dismissed: Boolean = false,
) : FullScreenIntentPort {
    override fun canUseFullScreenIntent(): Boolean = canUse
    override fun isBannerDismissed(): Boolean = dismissed
    override fun setBannerDismissed() {
        dismissed = true
    }
}

class IncomingCallNotifierTest {

    @Test
    fun `does not show banner when the toggle is still granted`() {
        val notifier = IncomingCallNotifier(FakeFullScreenIntentPort(canUse = true))

        assertFalse(notifier.shouldShowBanner())
    }

    @Test
    fun `shows banner once the toggle has been revoked`() {
        val notifier = IncomingCallNotifier(FakeFullScreenIntentPort(canUse = false))

        assertTrue(notifier.shouldShowBanner())
    }

    @Test
    fun `dismissing the banner hides it even though the toggle stays revoked`() {
        val notifier = IncomingCallNotifier(FakeFullScreenIntentPort(canUse = false))

        notifier.onBannerDismissed()

        assertFalse(notifier.shouldShowBanner())
    }
}
