package com.vpsmanager.data.session

import com.vpsmanager.mobileapiclient.api.MobileApi
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class SessionRepositoryTest {

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

    private fun repositoryFor(): SessionRepository {
        val api = MobileApi(basePath = server.url("/").toString())
        return SessionRepository(api)
    }

    @Test
    fun `getMe maps a full identity to Success`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """{"user":"sam","server_time":1000,"email":"sam@example.com","is_admin":true,"capabilities":["admin.full"]}"""
                )
        )

        val result = repositoryFor().getMe()

        assertEquals(SessionResult.Success(user = "sam", email = "sam@example.com", isAdmin = true), result)
    }

    @Test
    fun `getMe maps a response with no email to Empty`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"user":"sam","server_time":1000,"is_admin":false,"capabilities":[]}""")
        )

        val result = repositoryFor().getMe()

        assertEquals(SessionResult.Empty, result)
    }

    @Test
    fun `getMe maps an HTTP client error to Error, never throwing`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(401)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"title":"Unauthorized"}""")
        )

        val result = repositoryFor().getMe()

        assertTrue(result is SessionResult.Error)
    }

    @Test
    fun `getMe maps an HTTP server error to Error, never throwing`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(503)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"title":"Service Unavailable"}""")
        )

        val result = repositoryFor().getMe()

        assertTrue(result is SessionResult.Error)
    }
}
