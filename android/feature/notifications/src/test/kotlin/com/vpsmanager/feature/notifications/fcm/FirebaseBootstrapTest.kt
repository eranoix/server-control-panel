package com.vpsmanager.feature.notifications.fcm

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * What these tests protect: reading `google-services.json` is the only point
 * between "the owner created the project in Firebase" and "the notification
 * arrives". A mistake here breaks nothing visibly — the push simply never
 * turns up, which is the hardest failure mode there is to notice.
 */
@RunWith(RobolectricTestRunner::class)
class FirebaseBootstrapTest {

    private fun json(vararg pacotes: String): String {
        val clientes = pacotes.joinToString(",") { pacote ->
            """
            {
              "client_info": {
                "mobilesdk_app_id": "1:12345:android:${pacote.hashCode()}",
                "android_client_info": { "package_name": "$pacote" }
              },
              "api_key": [ { "current_key": "chave-de-$pacote" } ]
            }
            """.trimIndent()
        }
        return """
        {
          "project_info": { "project_number": "12345", "project_id": "vpsm-teste" },
          "client": [ $clientes ]
        }
        """.trimIndent()
    }

    @Test
    fun `escolhe o cliente do NOSSO pacote, nao o primeiro da lista`() {
        // The file from the console describes every app in the project. Taking
        // the first one would register the device under another identity — and
        // the push would leave the server and never arrive, with no error
        // anywhere.
        val opcoes = FirebaseBootstrap.opcoesDe(
            json("com.outro.app", "tech.northwind.vpsm.app"),
            "tech.northwind.vpsm.app",
        )
        assertEquals("chave-de-tech.northwind.vpsm.app", opcoes?.apiKey)
        assertEquals("vpsm-teste", opcoes?.projectId)
        assertEquals("12345", opcoes?.gcmSenderId)
    }

    @Test
    fun `sem o nosso pacote devolve nulo em vez de chutar`() {
        assertNull(FirebaseBootstrap.opcoesDe(json("com.outro.app"), "tech.northwind.vpsm.app"))
    }

    @Test
    fun `arquivo corrompido nao derruba o boot`() {
        // This code runs inside a Bootstrap step: an exception here would stop
        // the app from opening because of an optional feature.
        assertNull(FirebaseBootstrap.opcoesDe("{ isto não é json", "tech.northwind.vpsm.app"))
        assertNull(FirebaseBootstrap.opcoesDe("{}", "tech.northwind.vpsm.app"))
    }
}
