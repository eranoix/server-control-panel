package com.vpsmanager.feature.notifications.fcm

import androidx.core.app.NotificationCompat
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf

@RunWith(RobolectricTestRunner::class)
class ActionableNotificationBuilderTest {

    @Test
    fun `job failed payload targets the deploy channel with a Ver log action and job_id deep link`() {
        val context = RuntimeEnvironment.getApplication()
        val data = mapOf(
            "event_type" to "job.failed",
            "job_id" to "abc123",
            "title" to "Deploy falhou",
            "body" to "O deploy abc123 falhou.",
        )

        val notification = ActionableNotificationBuilder.build(context, data).build()

        assertEquals(NotificationChannels.CHANNEL_DEPLOY, NotificationCompat.getChannelId(notification))
        assertEquals(1, notification.actions?.size)
        assertEquals("View log", notification.actions!![0].title)

        val contentIntent = shadowOf(notification.contentIntent).savedIntent
        assertEquals(NotificationDeepLink.ROUTE_DEPLOY_JOB, contentIntent.getStringExtra(NotificationDeepLink.EXTRA_ROUTE))
        assertEquals("abc123", contentIntent.getStringExtra(NotificationDeepLink.EXTRA_ENTITY_ID))
    }

    @Test
    fun `critical metric alert gets no inline action beyond opening the app`() {
        val context = RuntimeEnvironment.getApplication()
        val data = mapOf(
            "event_type" to "metric.threshold",
            "severity" to "critical",
            "title" to "CPU critico",
            "body" to "CPU acima do limite.",
        )

        val notification = ActionableNotificationBuilder.build(context, data).build()

        assertEquals(NotificationChannels.CHANNEL_ALERTS, NotificationCompat.getChannelId(notification))
        assertTrue(notification.actions == null || notification.actions.isEmpty())
    }

    @Test
    fun `non-critical metric alert gets an inline Dismiss action`() {
        val context = RuntimeEnvironment.getApplication()
        val data = mapOf(
            "event_type" to "metric.threshold",
            "severity" to "warning",
            "title" to "Disco alto",
            "body" to "Uso de disco acima de 80 por cento.",
        )

        val notification = ActionableNotificationBuilder.build(context, data).build()

        assertEquals(1, notification.actions?.size)
        assertEquals("Dismiss", notification.actions!![0].title)
    }

    @Test
    fun `inlineActionFor never produces a destructive action`() {
        assertNull(ActionableNotificationBuilder.inlineActionFor("metric.threshold", "critical"))
        assertTrue(ActionableNotificationBuilder.inlineActionFor("job.failed", null) is InlineAction.OpenScreen)
        assertTrue(
            ActionableNotificationBuilder.inlineActionFor("metric.threshold", "info") is InlineAction.Dismiss,
        )
    }
}
