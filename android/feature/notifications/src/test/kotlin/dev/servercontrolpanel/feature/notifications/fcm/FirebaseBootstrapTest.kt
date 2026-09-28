package dev.servercontrolpanel.feature.notifications.fcm

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

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
          "project_info": { "project_number": "12345", "project_id": "panel-test" },
          "client": [ $clients ]
        }
        """.trimIndent()
    }

    @Test
    fun `picks the client for OUR package, not the first in the list`() {
        val options = FirebaseBootstrap.optionsFrom(
            json("com.other.app", "tech.northwind.servercontrolpanel"),
            "tech.northwind.servercontrolpanel",
        )
        assertEquals("key-for-tech.northwind.servercontrolpanel", options?.apiKey)
        assertEquals("panel-test", options?.projectId)
        assertEquals("12345", options?.gcmSenderId)
    }

    @Test
    fun `without our package returns null instead of guessing`() {
        assertNull(FirebaseBootstrap.optionsFrom(json("com.other.app"), "tech.northwind.servercontrolpanel"))
    }

    @Test
    fun `a corrupt file does not break startup`() {
        assertNull(FirebaseBootstrap.optionsFrom("{ this is not json", "tech.northwind.servercontrolpanel"))
        assertNull(FirebaseBootstrap.optionsFrom("{}", "tech.northwind.servercontrolpanel"))
    }
}
