package com.vpsmanager.app.nav

import com.vpsmanager.feature.notifications.fcm.NotificationDeepLink
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ResolveNotificationDeepLinkTest {

    @Test
    fun `a deploy job route resolves to the admin section with the job id attached`() {
        val resolved = resolveNotificationDeepLink(
            route = NotificationDeepLink.ROUTE_DEPLOY_JOB,
            entityId = "abc123",
        )

        assertEquals("admin/scheduler.jobs", resolved?.navRoute)
        assertEquals("abc123", resolved?.entityId)
    }

    @Test
    fun `an alert route with no entity id still resolves to the admin section`() {
        val resolved = resolveNotificationDeepLink(
            route = NotificationDeepLink.ROUTE_ALERT,
            entityId = null,
        )

        assertEquals("admin/scheduler.jobs", resolved?.navRoute)
        assertNull(resolved?.entityId)
    }

    @Test
    fun `a null route resolves to null — no extras on the Intent, not a notification tap`() {
        assertNull(resolveNotificationDeepLink(route = null, entityId = null))
    }

    @Test
    fun `an unrecognized route resolves to null instead of navigating anywhere`() {
        assertNull(resolveNotificationDeepLink(route = "not_a_real_route", entityId = "abc123"))
    }

    @Test
    fun `a blank route resolves to null`() {
        assertNull(resolveNotificationDeepLink(route = "", entityId = null))
    }

    @Test
    fun `a hostile route crafted to look like a nav path is never passed through as-is`() {
        val hostile = "admin/../../secret"

        val resolved = resolveNotificationDeepLink(route = hostile, entityId = null)

        assertNull(resolved)
    }

    @Test
    fun `the entity id is carried opaquely and never influences which route is resolved`() {
        val withSuspiciousEntityId = resolveNotificationDeepLink(
            route = NotificationDeepLink.ROUTE_DEPLOY_JOB,
            entityId = "../../etc/passwd",
        )

        assertEquals("admin/scheduler.jobs", withSuspiciousEntityId?.navRoute)
        assertEquals("../../etc/passwd", withSuspiciousEntityId?.entityId)
    }
}
