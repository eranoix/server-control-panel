package com.vpsmanager.feature.notifications.fcm

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/** Parsing `google-services.json`: a mistake here fails silently, the push just never arrives. */
@RunWith(RobolectricTestRunner::class)
class FirebaseBootstrapTest {

    private fun json(vararg packages: String): String {
        val clients = packages.joinToString(",") { packageName ->
            """
            {
              "client_info": {
                "mobilesdk_app_id": "1:12345:android:${packageName.hashCode()}",
                "android_client_info": { "package_name": "$packageName" }
              },
              "api_key": [ { "current_key": "key-for-$packageName" } ]
            }
            """.trimIndent()
        }
        return """
        {
          "project_info": { "project_number": "12345", "project_id": "vpsm-test" },
          "client": [ $clients ]
        }
        """.trimIndent()
    }

    @Test
    fun `picks the client for OUR package, not the first in the list`() {
        // The file lists every app in the project; the first one would register under
        // another identity and pushes would silently never arrive.
        val options = FirebaseBootstrap.optionsFrom(
            json("com.other.app", "tech.northwind.vpsm.app"),
            "tech.northwind.vpsm.app",
        )
        assertEquals("key-for-tech.northwind.vpsm.app", options?.apiKey)
        assertEquals("vpsm-test", options?.projectId)
        assertEquals("12345", options?.gcmSenderId)
    }

    @Test
    fun `without our package returns null instead of guessing`() {
        assertNull(FirebaseBootstrap.optionsFrom(json("com.other.app"), "tech.northwind.vpsm.app"))
    }

    @Test
    fun `a corrupt file does not break startup`() {
        // Runs during app bootstrap: an exception would block launch over an optional feature.
        assertNull(FirebaseBootstrap.optionsFrom("{ this is not json", "tech.northwind.vpsm.app"))
        assertNull(FirebaseBootstrap.optionsFrom("{}", "tech.northwind.vpsm.app"))
    }
}
