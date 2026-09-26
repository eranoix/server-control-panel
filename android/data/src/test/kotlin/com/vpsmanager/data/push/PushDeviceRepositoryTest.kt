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

class PushDeviceRepositoryTest {

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

    private fun repositoryFor(): PushDeviceRepository {
        val api = MobileApi(basePath = server.url("/").toString())
        return PushDeviceRepository(api)
    }

    @Test
    fun registerCallsCorrectEndpointWithDeviceId() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"status":"ok"}"""),
        )

        val result = repositoryFor().register(deviceId = "device-123", fcmToken = "fcm-token-abc")

        assertEquals(PushDeviceResult.Success, result)
        val recorded = server.takeRequest()
        assertEquals("POST", recorded.method)
        assertEquals("/notify/devices", recorded.path)
        val body = recorded.body.readUtf8()
        assertTrue(body.contains("\"device_id\":\"device-123\""))
        assertTrue(body.contains("\"fcm_token\":\"fcm-token-abc\""))
        assertTrue(body.contains("\"platform\":\"android\""))
    }

    @Test
    fun unregisterCallsDeleteWithDeviceId() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"status":"ok"}"""),
        )

        val result = repositoryFor().unregister(deviceId = "device-123")

        assertEquals(PushDeviceResult.Success, result)
        val recorded = server.takeRequest()
        assertEquals("DELETE", recorded.method)
        assertTrue(recorded.path!!.endsWith("/notify/devices/device-123"))
    }
}
