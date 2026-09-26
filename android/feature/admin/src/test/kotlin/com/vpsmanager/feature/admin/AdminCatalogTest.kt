package com.vpsmanager.feature.admin

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import com.vpsmanager.data.sdui.SduiCatalogPort
import com.vpsmanager.data.sdui.SduiSection
import com.vpsmanager.data.sdui.SduiSectionsResult
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * The catalogue a server would return for an admin: five sections in three
 * groups, already in the grouped order the server emits.
 *
 * Note that no id here is known to the app — that is the point of the test.
 * The picker draws whatever ARRIVES, including a section this release has
 * never seen ("futuro.inventado"), which is what lets a new screen on the
 * server show up on the phone with no release.
 */
private val ADMIN_CATALOG = listOf(
    SduiSection("docker.containers", "Docker", "Containers"),
    SduiSection("docker.prune", "Docker", "Limpeza do Docker"),
    SduiSection("system.processes", "Sistema", "Processos"),
    SduiSection("security.audit", "Segurança", "Log de auditoria"),
    SduiSection("futuro.inventado", "Sistema", "Seção que este app nunca viu"),
)

/**
 * The SAME server as seen by a non-admin: the administrative sections simply
 * DO NOT COME. They do not come marked unavailable — they come absent, which
 * is the BFF's anti-enumeration stance (`CatalogFor` filters by omission).
 */
private val NON_ADMIN_CATALOG = listOf(
    SduiSection("docker.containers", "Docker", "Containers"),
    SduiSection("system.processes", "Sistema", "Processos"),
)

private fun fixedPort(result: SduiSectionsResult) = SduiCatalogPort { result }

@RunWith(RobolectricTestRunner::class)
class AdminCatalogViewModelTest {

    @Test
    fun `a lista de secoes e a que o servidor mandou, inclusive uma que este app nunca viu`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        val state = vm.uiState.value
        assertTrue("estado = $state", state is AdminCatalogState.Ready)
        val ready = state as AdminCatalogState.Ready

        assertEquals(ADMIN_CATALOG.map { it.id }, ready.sections.map { it.id })
        assertTrue(
            "uma seção desconhecida do cliente precisa aparecer mesmo assim",
            ready.sections.any { it.id == "futuro.inventado" },
        )
    }

    /**
     * The client-side proof of RBAC: what the server omitted is not invented
     * back by the app. A non-admin's picker has no `docker.prune` and no
     * `security.audit` — not hidden, not disabled.
     */
    @Test
    fun `secao sem permissao nao aparece porque o servidor nao a mandou`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(NON_ADMIN_CATALOG)),
        )

        val ready = vm.uiState.value as AdminCatalogState.Ready
        val ids = ready.sections.map { it.id }
        assertEquals(listOf("docker.containers", "system.processes"), ids)
        assertTrue("docker.prune não pode aparecer para não-admin", "docker.prune" !in ids)
        assertTrue("security.audit não pode aparecer para não-admin", "security.audit" !in ids)
    }

    @Test
    fun `sem secao na rota, nenhuma secao abre — quem aparece e o lancador`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        val ready = vm.uiState.value as AdminCatalogState.Ready
        // Opening on the first one treated the sections as if one of them
        // were the right answer. None is: which matters depends on what is
        // going on.
        assertNull(ready.selectedId)
        // And the catalogue is still whole — the launcher is not an empty catalogue.
        assertEquals(ADMIN_CATALOG.size, ready.sections.size)
    }

    @Test
    fun `voltar ao lancador fecha a secao e sobrevive a um recarregamento`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )
        vm.select("docker.containers")
        assertEquals("docker.containers", (vm.uiState.value as AdminCatalogState.Ready).selectedId)

        vm.backToLauncher()
        assertNull((vm.uiState.value as AdminCatalogState.Ready).selectedId)

        // Without clearing the choice, the next load would reopen the section
        // the person has just closed.
        vm.load()
        assertNull((vm.uiState.value as AdminCatalogState.Ready).selectedId)
    }

    @Test
    fun `a busca sobrevive a entrar numa secao e voltar`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        vm.search("docker")
        vm.select("docker.containers")
        vm.backToLauncher()

        assertEquals("docker", (vm.uiState.value as AdminCatalogState.Ready).query)
    }

    @Test
    fun `os recentes saem na ordem de uso e nao duplicam`() = runTest {
        val history = mutableListOf<String>()
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
            readRecents = { history.toList() },
            writeRecent = { id ->
                history.remove(id)
                history.add(0, id)
            },
        )

        vm.select("docker.containers")
        vm.backToLauncher()
        vm.select("scheduler.jobs")
        vm.backToLauncher()
        vm.select("docker.containers")

        val ready = vm.uiState.value as AdminCatalogState.Ready
        assertEquals(listOf("docker.containers", "scheduler.jobs"), ready.recentIds)
    }

    /**
     * A stored id that has left the catalogue (section removed on the server,
     * permission revoked) must NOT become a shortcut that opens onto a 404.
     */
    @Test
    fun `recente que saiu do catalogo some da lista em vez de virar atalho quebrado`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
            readRecents = { listOf("secao.que.sumiu", "docker.containers") },
        )

        val ready = vm.uiState.value as AdminCatalogState.Ready
        assertEquals(listOf("docker.containers"), ready.recents.map { it.id })
    }

    /**
     * A deep link (a notification) points at a concrete section and that choice
     * beats the automatic one — otherwise tapping a job notification would
     * land you in Containers.
     */
    @Test
    fun `uma secao concreta na rota vence a escolha automatica`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = "security.audit",
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        assertEquals("security.audit", (vm.uiState.value as AdminCatalogState.Ready).selectedId)
    }

    /**
     * The catalogue is a HINT, not a gate: a section that is not in the list is
     * still attempted, and `/screens/{id}` is what decides. Without that, a
     * valid deep link could be swallowed by the client over a momentarily
     * stale catalogue.
     */
    @Test
    fun `uma secao fora do catalogo ainda e tentada — quem autoriza e o fetch`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = "secao.que.nao.esta.no.catalogo",
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        assertEquals("secao.que.nao.esta.no.catalogo", (vm.uiState.value as AdminCatalogState.Ready).selectedId)
    }

    @Test
    fun `catalogo vazio nao escolhe secao nenhuma`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(emptyList())),
        )

        assertNull((vm.uiState.value as AdminCatalogState.Ready).selectedId)
    }

    @Test
    fun `os grupos saem agrupados e na ordem em que o servidor os emitiu`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        val groups = (vm.uiState.value as AdminCatalogState.Ready).grouped
        assertEquals(listOf("Docker", "Sistema", "Segurança"), groups.map { it.first })
        assertEquals(listOf("docker.containers", "docker.prune"), groups[0].second.map { it.id })
    }

    @Test
    fun `escolher uma secao troca a selecao e preserva a lista`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        vm.select("security.audit")

        val ready = vm.uiState.value as AdminCatalogState.Ready
        assertEquals("security.audit", ready.selectedId)
        assertEquals("Log de auditoria", ready.selected?.label)
        assertEquals(ADMIN_CATALOG.size, ready.sections.size)
    }

    /** Reloading the catalogue must not throw the user back to the initial section. */
    @Test
    fun `a escolha do usuario sobrevive a um recarregamento do catalogo`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )
        vm.select("system.processes")

        vm.load()

        assertEquals("system.processes", (vm.uiState.value as AdminCatalogState.Ready).selectedId)
    }

    @Test
    fun `falha ao listar vira erro com nova tentativa, nunca uma tela em branco`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Error("Falha de conexão. Verifique a rede e tente novamente.")),
        )

        val state = vm.uiState.value
        assertTrue("estado = $state", state is AdminCatalogState.Error)
        assertEquals("Falha de conexão. Verifique a rede e tente novamente.", (state as AdminCatalogState.Error).message)
    }
}

/**
 * The launcher: the grid, the search, and the third state only the search has.
 *
 * Robolectric because this is a real Compose tree — what we want to prove is
 * what appears on screen, not what the ViewModel holds.
 */
@RunWith(RobolectricTestRunner::class)
class AdminLauncherTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `a grade mostra o que o servidor mandou, com rotulo e grupo`() {
        composeRule.setContent {
            AdminLauncher(
                sections = ADMIN_CATALOG,
                query = "",
                recents = emptyList(),
                onQueryChange = {},
                onSelect = {},
            )
        }

        composeRule.onNodeWithText("Containers").assertExists()
        composeRule.onNodeWithText("Log de auditoria").assertExists()
        // The section this release has never seen has to appear just the same
        // — that is what gets a new server screen here with no release.
        composeRule.onNodeWithText("Seção que este app nunca viu").assertExists()
    }

    @Test
    fun `a busca filtra por rotulo, por grupo e por id`() {
        composeRule.setContent {
            AdminLauncher(
                sections = ADMIN_CATALOG,
                query = "seguran",
                recents = emptyList(),
                onQueryChange = {},
                onSelect = {},
            )
        }

        composeRule.onNodeWithText("Log de auditoria").assertExists()
        composeRule.onNodeWithText("Containers").assertDoesNotExist()
    }

    /**
     * "Searched and found nothing" is neither empty nor an error. If this
     * screen does not state the term, it becomes indistinguishable from "the
     * catalogue is gone" — a completely different problem, and a needlessly
     * alarming one.
     */
    @Test
    fun `busca sem resultado diz o termo e quantas secoes existem`() {
        composeRule.setContent {
            AdminLauncher(
                sections = ADMIN_CATALOG,
                query = "xyz",
                recents = emptyList(),
                onQueryChange = {},
                onSelect = {},
            )
        }

        composeRule.onNodeWithText("Nothing matches “xyz”").assertExists()
        composeRule.onNodeWithText(
            "None of the ${ADMIN_CATALOG.size} sections match that text.",
        ).assertExists()
    }

    @Test
    fun `recentes aparecem quando nao se esta buscando`() {
        composeRule.setContent {
            AdminLauncher(
                sections = ADMIN_CATALOG,
                query = "",
                recents = listOf(ADMIN_CATALOG[3]),
                onQueryChange = {},
                onSelect = {},
            )
        }

        composeRule.onNodeWithText(ADMIN_RECENTS_LABEL).assertExists()
    }
}

/**
 * Kept apart from the test above because `setContent` may be called ONCE by
 * Compose's rule — two scenarios need two trees, not two calls.
 */
@RunWith(RobolectricTestRunner::class)
class AdminLauncherSearchTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `recentes somem quando se digita`() {
        composeRule.setContent {
            AdminLauncher(
                sections = ADMIN_CATALOG,
                query = "docker",
                recents = listOf(ADMIN_CATALOG[3]),
                onQueryChange = {},
                onSelect = {},
            )
        }

        // Someone who typed has already said what they want; repeating the
        // recents there would mix answers to two different questions.
        composeRule.onNodeWithText(ADMIN_RECENTS_LABEL).assertDoesNotExist()
    }
}

/**
 * The filter is a pure function shared by the launcher and the palette — the
 * two can never disagree about what a term finds.
 */
class SectionFilterTest {

    @Test
    fun `termo vazio devolve o catalogo inteiro`() {
        assertEquals(ADMIN_CATALOG, filterSections(ADMIN_CATALOG, "   "))
    }

    @Test
    fun `casa por id, para quem chegou vindo de uma mensagem de erro`() {
        val found = filterSections(ADMIN_CATALOG, "security.audit")
        assertEquals(listOf("security.audit"), found.map { it.id })
    }

    @Test
    fun `casa por grupo, ignorando caixa`() {
        val found = filterSections(ADMIN_CATALOG, "DOCKER")
        assertEquals(listOf("docker.containers", "docker.prune"), found.map { it.id })
    }
}
