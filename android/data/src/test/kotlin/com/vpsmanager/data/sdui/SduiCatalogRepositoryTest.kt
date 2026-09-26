package com.vpsmanager.data.sdui

import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * The HTTP -> [SduiSectionsResult] translation of the section catalogue. Here,
 * in `:data`, is where a `MockWebServer` is legitimate (the gate forbids it
 * outside this module), so here is where the shape on the wire is pinned.
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

    private fun responder(code: Int, body: String) {
        server.enqueue(
            MockResponse()
                .setResponseCode(code)
                .setHeader("Content-Type", "application/json")
                .setBody(body),
        )
    }

    @Test
    fun `le id, grupo e rotulo preservando a ordem agrupada do servidor`() = runTest {
        responder(
            200,
            """
            {"sections":[
              {"id":"docker.containers","group":"Docker","label":"Containers"},
              {"id":"docker.prune","group":"Docker","label":"Limpeza do Docker"},
              {"id":"system.processes","group":"Sistema","label":"Processos"}
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
        assertEquals(SduiSection("docker.prune", "Docker", "Limpeza do Docker"), sections[1])

        val request = server.takeRequest()
        assertEquals("/api/mobile/v1/screens", request.path)
        assertEquals("GET", request.method)
    }

    /**
     * Tolerance of a newer server: a field this release does not know about is
     * ignored, and the section stays usable. A client that broke here would
     * defeat the whole reason SDUI exists.
     */
    @Test
    fun `campo desconhecido do servidor e ignorado, nunca fatal`() = runTest {
        responder(
            200,
            """{"sections":[{"id":"system.ports","group":"Sistema","label":"Portas em escuta",
               "icone":"radar","badge_count":7,"novidade":{"desde":"2026-09"}}]}""",
        )

        val result = repositoryFor().sections()

        val sections = (result as SduiSectionsResult.Success).sections
        assertEquals(1, sections.size)
        assertEquals("system.ports", sections.single().id)
    }

    /**
     * A broken entry takes down the ENTRY, not the list: losing one malformed
     * section is far better than leaving the operator with no selector at all.
     */
    @Test
    fun `entrada sem id ou sem rotulo e pulada, o resto da lista sobrevive`() = runTest {
        responder(
            200,
            """
            {"sections":[
              {"group":"Docker","label":"Sem id"},
              {"id":"sem.label","group":"Docker"},
              {"id":"docker.images","group":"Docker","label":"Imagens do Docker"}
            ]}
            """.trimIndent(),
        )

        val sections = (repositoryFor().sections() as SduiSectionsResult.Success).sections

        assertEquals(listOf("docker.images"), sections.map { it.id })
    }

    /** A JSON `null` in the label must never become a section called "null". */
    @Test
    fun `rotulo nulo e tratado como ausente, nao como o texto null`() = runTest {
        responder(200, """{"sections":[{"id":"a.b","group":"Docker","label":null}]}""")

        val sections = (repositoryFor().sections() as SduiSectionsResult.Success).sections

        assertTrue("uma seção com rótulo null tem que ser pulada: $sections", sections.isEmpty())
    }

    /** A missing group becomes a neutral header — cosmetic, never loses the section. */
    @Test
    fun `grupo ausente cai em Outros em vez de derrubar a secao`() = runTest {
        responder(200, """{"sections":[{"id":"a.b","label":"Alguma coisa"}]}""")

        val sections = (repositoryFor().sections() as SduiSectionsResult.Success).sections

        assertEquals("Other", sections.single().group)
    }

    /**
     * A user with no permissions at all gets a 200 with an EMPTY list, never a
     * 403 — the server filters by omission. The data layer has to hand that
     * back as an empty success so the screen shows the state that teaches, and
     * not an error message.
     */
    @Test
    fun `lista vazia e sucesso vazio, nunca erro`() = runTest {
        responder(200, """{"sections":[]}""")

        val result = repositoryFor().sections()

        assertTrue("result = $result", result is SduiSectionsResult.Success)
        assertTrue((result as SduiSectionsResult.Success).sections.isEmpty())
    }

    /**
     * An old server, without the catalogue endpoint: the message says so,
     * instead of accusing the user of asking for something that does not exist.
     */
    @Test
    fun `404 no catalogo vira mensagem sobre o servidor, nao sobre o usuario`() = runTest {
        responder(404, """{"error":"not_found"}""")

        val result = repositoryFor().sections()

        assertTrue("result = $result", result is SduiSectionsResult.Error)
        assertEquals(
            "This server does not offer the section list yet.",
            (result as SduiSectionsResult.Error).reason,
        )
    }

    @Test
    fun `500 vira mensagem de servidor indisponivel`() = runTest {
        responder(500, """{"error":"internal_error"}""")

        val result = repositoryFor().sections()

        assertTrue("result = $result", result is SduiSectionsResult.Error)
        assertEquals("The server is unavailable right now.", (result as SduiSectionsResult.Error).reason)
    }
}
