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
 * An admin's catalog: five sections in three groups, in server order. It
 * includes a section the app has never seen ("futuro.inventado"), which must
 * still be shown.
 */
private val ADMIN_CATALOG = listOf(
    SduiSection("docker.containers", "Docker", "Containers"),
    SduiSection("docker.prune", "Docker", "Docker cleanup"),
    SduiSection("system.processes", "System", "Processes"),
    SduiSection("security.audit", "Security", "Audit log"),
    SduiSection("futuro.inventado", "System", "A section this app has never seen"),
)

/**
 * The same server for a non-admin: admin sections are omitted, not marked
 * unavailable (the BFF filters by omission to prevent enumeration).
 */
private val NON_ADMIN_CATALOG = listOf(
    SduiSection("docker.containers", "Docker", "Containers"),
    SduiSection("system.processes", "System", "Processes"),
)

private fun fixedPort(result: SduiSectionsResult) = SduiCatalogPort { result }

@RunWith(RobolectricTestRunner::class)
class AdminCatalogViewModelTest {

    @Test
    fun `the section list is what the server sent, including an unknown section`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        val state = vm.uiState.value
        assertTrue("state = $state", state is AdminCatalogState.Ready)
        val ready = state as AdminCatalogState.Ready

        assertEquals(ADMIN_CATALOG.map { it.id }, ready.sections.map { it.id })
        assertTrue(
            "a section unknown to the client must still appear",
            ready.sections.any { it.id == "futuro.inventado" },
        )
    }

    /** The app never re-adds sections the server omitted for this user. */
    @Test
    fun `a section without permission is absent because the server did not send it`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(NON_ADMIN_CATALOG)),
        )

        val ready = vm.uiState.value as AdminCatalogState.Ready
        val ids = ready.sections.map { it.id }
        assertEquals(listOf("docker.containers", "system.processes"), ids)
        assertTrue("docker.prune must not appear for a non-admin", "docker.prune" !in ids)
        assertTrue("security.audit must not appear for a non-admin", "security.audit" !in ids)
    }

    @Test
    fun `with no section in the route, the launcher shows instead of a section`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        val ready = vm.uiState.value as AdminCatalogState.Ready
        assertNull(ready.selectedId)
        assertEquals(ADMIN_CATALOG.size, ready.sections.size)
    }

    @Test
    fun `going back to the launcher closes the section and survives a reload`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )
        vm.select("docker.containers")
        assertEquals("docker.containers", (vm.uiState.value as AdminCatalogState.Ready).selectedId)

        vm.backToLauncher()
        assertNull((vm.uiState.value as AdminCatalogState.Ready).selectedId)

        // A reload must not reopen the section just closed.
        vm.load()
        assertNull((vm.uiState.value as AdminCatalogState.Ready).selectedId)
    }

    @Test
    fun `the search survives opening a section and coming back`() = runTest {
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
    fun `recents are in order of use without duplicates`() = runTest {
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

    /** A stored id no longer in the catalog must not become a shortcut to a 404. */
    @Test
    fun `a recent that left the catalog is dropped instead of becoming a broken shortcut`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
            readRecents = { listOf("section.that.vanished", "docker.containers") },
        )

        val ready = vm.uiState.value as AdminCatalogState.Ready
        assertEquals(listOf("docker.containers"), ready.recents.map { it.id })
    }

    /** A deep link to a concrete section (e.g. from a notification) opens that section. */
    @Test
    fun `a concrete section in the route wins over the automatic choice`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = "security.audit",
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        assertEquals("security.audit", (vm.uiState.value as AdminCatalogState.Ready).selectedId)
    }

    /**
     * The catalog is a hint, not a gate: `/screens/{id}` decides, so a stale
     * catalog cannot swallow a valid deep link.
     */
    @Test
    fun `a section outside the catalog is still attempted, the fetch authorizes`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = "section.not.in.catalog",
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        assertEquals("section.not.in.catalog", (vm.uiState.value as AdminCatalogState.Ready).selectedId)
    }

    @Test
    fun `an empty catalog selects no section`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(emptyList())),
        )

        assertNull((vm.uiState.value as AdminCatalogState.Ready).selectedId)
    }

    @Test
    fun `groups come out grouped in server order`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        val groups = (vm.uiState.value as AdminCatalogState.Ready).grouped
        assertEquals(listOf("Docker", "System", "Security"), groups.map { it.first })
        assertEquals(listOf("docker.containers", "docker.prune"), groups[0].second.map { it.id })
    }

    @Test
    fun `selecting a section changes the selection and keeps the list`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )

        vm.select("security.audit")

        val ready = vm.uiState.value as AdminCatalogState.Ready
        assertEquals("security.audit", ready.selectedId)
        assertEquals("Audit log", ready.selected?.label)
        assertEquals(ADMIN_CATALOG.size, ready.sections.size)
    }

    @Test
    fun `the user's choice survives a catalog reload`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Success(ADMIN_CATALOG)),
        )
        vm.select("system.processes")

        vm.load()

        assertEquals("system.processes", (vm.uiState.value as AdminCatalogState.Ready).selectedId)
    }

    @Test
    fun `a listing failure becomes a retryable error, never a blank screen`() = runTest {
        val vm = AdminCatalogViewModel(
            initialRoute = ADMIN_SECTION_AUTO,
            catalogPort = fixedPort(SduiSectionsResult.Error("Connection failed. Check the network and try again.")),
        )

        val state = vm.uiState.value
        assertTrue("state = $state", state is AdminCatalogState.Error)
        assertEquals("Connection failed. Check the network and try again.", (state as AdminCatalogState.Error).message)
    }
}

/** The launcher's rendered grid, search and no-results state. */
@RunWith(RobolectricTestRunner::class)
class AdminLauncherTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `the grid shows what the server sent, with label and group`() {
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
        composeRule.onNodeWithText("Audit log").assertExists()
        // A section unknown to this release must still appear.
        composeRule.onNodeWithText("A section this app has never seen").assertExists()
    }

    @Test
    fun `search filters by label, group and id`() {
        composeRule.setContent {
            AdminLauncher(
                sections = ADMIN_CATALOG,
                query = "Securit",
                recents = emptyList(),
                onQueryChange = {},
                onSelect = {},
            )
        }

        composeRule.onNodeWithText("Audit log").assertExists()
        composeRule.onNodeWithText("Containers").assertDoesNotExist()
    }

    /** No results must state the term so it is not mistaken for a missing catalog. */
    @Test
    fun `a search with no results states the term and the section count`() {
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
    fun `recents show when not searching`() {
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

/** Separate class because the compose rule allows only one `setContent` per test. */
@RunWith(RobolectricTestRunner::class)
class AdminLauncherSearchTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `recents disappear while typing`() {
        composeRule.setContent {
            AdminLauncher(
                sections = ADMIN_CATALOG,
                query = "docker",
                recents = listOf(ADMIN_CATALOG[3]),
                onQueryChange = {},
                onSelect = {},
            )
        }

        composeRule.onNodeWithText(ADMIN_RECENTS_LABEL).assertDoesNotExist()
    }
}

/**
 * The filter shared by the launcher and the palette.
 */
class SectionFilterTest {

    @Test
    fun `a blank term returns the whole catalog`() {
        assertEquals(ADMIN_CATALOG, filterSections(ADMIN_CATALOG, "   "))
    }

    @Test
    fun `matches by id`() {
        val found = filterSections(ADMIN_CATALOG, "security.audit")
        assertEquals(listOf("security.audit"), found.map { it.id })
    }

    @Test
    fun `matches by group, ignoring case`() {
        val found = filterSections(ADMIN_CATALOG, "DOCKER")
        assertEquals(listOf("docker.containers", "docker.prune"), found.map { it.id })
    }
}
