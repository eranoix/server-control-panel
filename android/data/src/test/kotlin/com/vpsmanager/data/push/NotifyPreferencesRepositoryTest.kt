package com.vpsmanager.data.push

import com.vpsmanager.mobileapiclient.api.MobileApi
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class NotifyPreferencesRepositoryTest {

    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun repositoryFor(): NotifyPreferencesRepository {
        val api = MobileApi(basePath = server.url("/").toString())
        return NotifyPreferencesRepository(api)
    }

    @Test
    fun fetchReturnsRulesFromServerResponse() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """
                    {
                      "rules": [
                        {"id": "deploy-failed", "name": "Deploy falhou", "min_severity": "critical", "type_prefix": "job.", "enabled_for_device": true},
                        {"id": "deploy-done", "name": "Deploy concluido", "min_severity": "info", "type_prefix": "job.", "enabled_for_device": false}
                      ]
                    }
                    """.trimIndent(),
                ),
        )

        val result = repositoryFor().fetch(deviceId = "device-123")

        check(result is NotifyPreferencesResult.Success)
        assertEquals(2, result.rules.size)
        assertEquals("deploy-failed", result.rules[0].id)
        assertTrue(result.rules[0].enabledForDevice)
        assertEquals("deploy-done", result.rules[1].id)
        assertTrue(!result.rules[1].enabledForDevice)

        val recorded = server.takeRequest()
        assertEquals("GET", recorded.method)
        assertTrue(recorded.path!!.contains("device_id=device-123"))
    }

    @Test
    fun updateSendsDeviceIdAndEnabledRuleIds() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"status":"ok"}"""),
        )

        val result = repositoryFor().update(deviceId = "device-123", enabledRuleIds = listOf("deploy-failed"))

        assertEquals(UpdateNotifyPreferencesResult.Success, result)
        val recorded = server.takeRequest()
        assertEquals("PUT", recorded.method)
        val body = recorded.body.readUtf8()
        assertTrue(body.contains("\"device_id\":\"device-123\""))
        assertTrue(body.contains("\"enabled_rule_ids\":[\"deploy-failed\"]"))
    }

    @Test
    fun fetchSurfacesErrorOnServerFailure() = runTest {
        server.enqueue(MockResponse().setResponseCode(500))

        val result = repositoryFor().fetch(deviceId = "device-123")

        check(result is NotifyPreferencesResult.Error)
    }
}
