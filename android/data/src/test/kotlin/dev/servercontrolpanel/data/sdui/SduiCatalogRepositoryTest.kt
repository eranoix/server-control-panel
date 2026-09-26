package dev.servercontrolpanel.data.sdui

import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * The HTTP to [SduiSectionsResult] translation of the section catalogue. `MockWebServer` is only
 * allowed in `:data`, so the wire shape is pinned here.
 */
class SduiCatalogRepositoryTest {

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

    private fun repositoryFor() = SduiCatalogRepository(SduiDataClient(basePath = server.url("/").toString()))

    private fun respond(code: Int, body: String) {
        server.enqueue(
            MockResponse()
                .setResponseCode(code)
                .setHeader("Content-Type", "application/json")
                .setBody(body),
        )
    }

    @Test
    fun `reads id, group and label keeping the server's grouped order`() = runTest {
        respond(
            200,
            """
            {"sections":[
              {"id":"docker.containers","group":"Docker","label":"Containers"},
              {"id":"docker.prune","group":"Docker","label":"Docker cleanup"},
              {"id":"system.processes","group":"System","label":"Processes"}
            ]}
            """.trimIndent(),
        )

        val result = repositoryFor().sections()

        assertTrue("result = $result", result is SduiSectionsResult.Success)
        val sections = (result as SduiSectionsResult.Success).sections
        assertEquals(
            listOf("docker.containers", "docker.prune", "system.processes"),
            sections.map { it.id },
        )
        assertEquals(SduiSection("docker.prune", "Docker", "Docker cleanup"), sections[1])

        val request = server.takeRequest()
        assertEquals("/api/mobile/v1/screens", request.path)
        assertEquals("GET", request.method)
    }

    /** A field from a newer server is ignored and the section stays usable. */
    @Test
    fun `an unknown server field is ignored, never fatal`() = runTest {
        respond(
            200,
            """{"sections":[{"id":"system.ports","group":"System","label":"Listening ports",
               "icon":"radar","badge_count":7,"whats_new":{"since":"2026-09"}}]}""",
        )

        val result = repositoryFor().sections()

        val sections = (result as SduiSectionsResult.Success).sections
        assertEquals(1, sections.size)
        assertEquals("system.ports", sections.single().id)
    }

    /** A malformed entry is skipped without losing the rest of the list. */
    @Test
    fun `an entry without id or label is skipped and the rest survives`() = runTest {
        respond(
            200,
            """
            {"sections":[
              {"group":"Docker","label":"No id"},
              {"id":"no.label","group":"Docker"},
              {"id":"docker.images","group":"Docker","label":"Docker images"}
            ]}
            """.trimIndent(),
        )

        val sections = (repositoryFor().sections() as SduiSectionsResult.Success).sections

        assertEquals(listOf("docker.images"), sections.map { it.id })
    }

    /** A JSON `null` in the label must never become a section called "null". */
    @Test
    fun `a null label is treated as missing, not as the text null`() = runTest {
        respond(200, """{"sections":[{"id":"a.b","group":"Docker","label":null}]}""")

        val sections = (repositoryFor().sections() as SduiSectionsResult.Success).sections

        assertTrue("a section with a null label must be skipped: $sections", sections.isEmpty())
    }

    /** A missing group becomes a neutral header instead of losing the section. */
    @Test
    fun `a missing group falls back to Other instead of dropping the section`() = runTest {
        respond(200, """{"sections":[{"id":"a.b","label":"Something"}]}""")

        val sections = (repositoryFor().sections() as SduiSectionsResult.Success).sections

        assertEquals("Other", sections.single().group)
    }

    /**
     * A user without permissions gets 200 with an empty list (the server filters by omission),
     * which must be an empty success, not an error.
     */
    @Test
    fun `an empty list is an empty success, never an error`() = runTest {
        respond(200, """{"sections":[]}""")

        val result = repositoryFor().sections()

        assertTrue("result = $result", result is SduiSectionsResult.Success)
        assertTrue((result as SduiSectionsResult.Success).sections.isEmpty())
    }

    /** An old server without the catalogue endpoint gets a message about the server, not the user. */
    @Test
    fun `a catalogue 404 becomes a message about the server, not the user`() = runTest {
        respond(404, """{"error":"not_found"}""")

        val result = repositoryFor().sections()

        assertTrue("result = $result", result is SduiSectionsResult.Error)
        assertEquals(
            "This server does not offer the section list yet.",
            (result as SduiSectionsResult.Error).reason,
        )
    }

    @Test
    fun `a 500 becomes a server unavailable message`() = runTest {
        respond(500, """{"error":"internal_error"}""")

        val result = repositoryFor().sections()

        assertTrue("result = $result", result is SduiSectionsResult.Error)
        assertEquals("The server is unavailable right now.", (result as SduiSectionsResult.Error).reason)
    }
}
