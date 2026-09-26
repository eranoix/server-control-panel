package com.vpsmanager.data.terminal

import com.vpsmanager.mobileapiclient.api.MobileApi
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class TerminalRepositoryTest {

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

    private fun repositoryFor(): TerminalRepository {
        val api = MobileApi(basePath = server.url("/").toString())
        return TerminalRepository(api)
    }

    @Test
    fun `sessions maps a non-empty list to Success`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """[{"name":"main","attached":true,"created":1000,"tab":"shell"}]""",
                ),
        )

        val result = repositoryFor().sessions()

        assertEquals(
            TerminalSessionsResult.Success(
                listOf(TerminalSession(name = "main", attached = true, created = 1000, tab = "shell")),
            ),
            result,
        )
    }

    @Test
    fun `sessions maps an empty list to Empty`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("[]"),
        )

        val result = repositoryFor().sessions()

        assertEquals(TerminalSessionsResult.Empty, result)
    }

    @Test
    fun `sessions maps an HTTP error to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"title":"Unauthorized"}"""))

        val result = repositoryFor().sessions()

        assertTrue(result is TerminalSessionsResult.Error)
    }

    @Test
    fun `wsTicket maps a successful response to Success with the real ticket and TTL`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"ticket":"tk-123","expires_in":60}"""),
        )

        val result = repositoryFor().wsTicket("main")

        assertEquals(WsTicketResult.Success(ticket = "tk-123", expiresIn = 60), result)
    }

    @Test
    fun `wsTicket maps a 404 (session not owned) to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(404).setBody("""{"title":"Not Found"}"""))

        val result = repositoryFor().wsTicket("outra-sessao")

        assertTrue(result is WsTicketResult.Error)
    }

    @Test
    fun `scrollback maps a successful response to Success with the raw text`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"data":"linha 1\nlinha 2\n"}"""),
        )

        val result = repositoryFor().scrollback("main", lines = 100, plain = true)

        assertEquals(ScrollbackResult.Success("linha 1\nlinha 2\n"), result)
    }

    @Test
    fun `scrollback maps an HTTP error to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(404).setBody("""{"title":"Not Found"}"""))

        val result = repositoryFor().scrollback("outra-sessao")

        assertTrue(result is ScrollbackResult.Error)
    }
}
