package com.vpsmanager.data.files

import com.vpsmanager.core.model.FileEntry
import com.vpsmanager.mobileapiclient.api.MobileApi
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class FilesRepositoryTest {

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

    private fun repositoryFor(): FilesRepository {
        val api = MobileApi(basePath = server.url("/").toString())
        return FilesRepository(api)
    }

    @Test
    fun `list maps a directory with entries to Success`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """
                    {
                        "path": "/srv",
                        "parent": "/",
                        "entries": [
                            {"name": "app", "size": 4096, "modified": 1700000000, "is_dir": true},
                            {"name": "readme.txt", "size": 128, "modified": 1700000100, "is_dir": false}
                        ]
                    }
                    """.trimIndent()
                )
        )

        val result = repositoryFor().list("/srv")

        assertEquals(
            FileListResult.Success(
                path = "/srv",
                parent = "/",
                entries = listOf(
                    FileEntry(name = "app", size = 4096, isDir = true, modifiedEpochSeconds = 1700000000),
                    FileEntry(name = "readme.txt", size = 128, isDir = false, modifiedEpochSeconds = 1700000100),
                ),
            ),
            result,
        )
    }

    @Test
    fun `list maps a directory with zero entries to Empty`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"path":"/srv/empty","parent":"/srv","entries":[]}""")
        )

        val result = repositoryFor().list("/srv/empty")

        assertEquals(FileListResult.Empty, result)
    }

    @Test
    fun `list maps an HTTP client error to Error, never throwing`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(404)
                .setHeader("Content-Type", "application/problem+json")
                .setBody("""{"title":"Not Found"}""")
        )

        val result = repositoryFor().list("/does/not/exist")

        assertTrue(result is FileListResult.Error)
    }

    @Test
    fun `list maps an HTTP server error to Error, never throwing`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(500)
                .setHeader("Content-Type", "application/problem+json")
                .setBody("""{"title":"Internal Server Error"}""")
        )

        val result = repositoryFor().list("/srv")

        assertTrue(result is FileListResult.Error)
    }

    @Test
    fun `read maps a successful body to Success with content, mtime and language`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"content":"package main\n","mtime":1700000000,"language":"go","size":13}""")
        )

        val result = repositoryFor().read("/srv/main.go")

        assertEquals(
            FileReadResult.Success(content = "package main\n", mtime = 1700000000, language = "go"),
            result,
        )
    }

    @Test
    fun `read maps 413 (too large) and 415 (binary) to distinct, non-generic reasons`() = runTest {
        server.enqueue(MockResponse().setResponseCode(413).setBody("""{"title":"file too large"}"""))
        val tooLarge = repositoryFor().read("/srv/huge.bin") as FileReadResult.Error

        server.enqueue(MockResponse().setResponseCode(415).setBody("""{"title":"binary file"}"""))
        val binary = repositoryFor().read("/srv/photo.png") as FileReadResult.Error

        assertTrue(tooLarge.reason.contains("too large"))
        assertTrue(binary.reason.contains("text"))
        org.junit.Assert.assertNotEquals(tooLarge.reason, binary.reason)
    }

    @Test
    fun `write maps a 200 body to Success carrying the new mtime`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"ok":true,"mtime":1700000200}""")
        )

        val result = repositoryFor().write("/srv/main.go", "package main\n", 1700000000)

        assertEquals(FileWriteResult.Success(mtime = 1700000200), result)
    }

    @Test
    fun `write maps a 409 body to Conflict carrying the server's current content and mtime`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(409)
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """{"error":"conflict","server_content":"package main\n\nfunc main() {}\n","server_mtime":1700000150}"""
                )
        )

        val result = repositoryFor().write("/srv/main.go", "package main\n", 1700000000)

        assertEquals(
            FileWriteResult.Conflict(serverContent = "package main\n\nfunc main() {}\n", serverMtime = 1700000150),
            result,
        )
    }

    @Test
    fun `write maps a non-conflict client error to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(400).setBody("""{"title":"bad request"}"""))

        val result = repositoryFor().write("", "x", 0)

        assertTrue(result is FileWriteResult.Error)
    }
}
