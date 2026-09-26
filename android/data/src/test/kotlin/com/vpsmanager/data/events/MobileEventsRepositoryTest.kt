package com.vpsmanager.data.events

import com.vpsmanager.mobileapiclient.api.MobileApi
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class MobileEventsRepositoryTest {

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

    private fun repositoryFor(): MobileEventsRepository {
        val api = MobileApi(basePath = server.url("/").toString())
        return MobileEventsRepository(api)
    }

    @Test
    fun `wsTicket maps a successful response to Success with the real ticket and TTL`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"ticket":"tk-123","expires_in":60}"""),
        )

        val result = repositoryFor().wsTicket()

        assertEquals(WsTicketResult.Success(ticket = "tk-123", expiresIn = 60), result)
    }

    @Test
    fun `wsTicket maps a 401 to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"title":"Unauthorized"}"""))

        val result = repositoryFor().wsTicket()

        assertTrue(result is WsTicketResult.Error)
    }

    @Test
    fun `wsTicket maps a network failure to Error, never throwing`() = runTest {
        server.shutdown()

        val result = repositoryFor().wsTicket()

        assertTrue(result is WsTicketResult.Error)
    }
}
