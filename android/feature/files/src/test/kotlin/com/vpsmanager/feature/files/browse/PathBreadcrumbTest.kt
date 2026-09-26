package com.vpsmanager.feature.files.browse

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * What these tests protect: the breadcrumb is the one piece of information on
 * this screen with no empty state — it answers "where am I" even in a folder
 * with nothing in it and even when the listing failed. A step that points at
 * the wrong path takes the next action to the wrong folder, and the next
 * action in a file browser tends to be destructive.
 */
class PathBreadcrumbTest {

    @Test
    fun `a raiz e um degrau, nao um rotulo vazio`() {
        val crumbs = pathCrumbs("/")

        assertEquals(1, crumbs.size)
        assertEquals("/", crumbs.single().label)
        assertEquals("/", crumbs.single().path)
    }

    @Test
    fun `cada degrau aponta para o proprio nivel, acumulado desde a raiz`() {
        val crumbs = pathCrumbs("/opt/panel/data")

        assertEquals(listOf("/", "opt", "panel", "data"), crumbs.map { it.label })
        assertEquals(
            listOf("/", "/opt", "/opt/panel", "/opt/panel/data"),
            crumbs.map { it.path },
        )
    }

    /** A trailing slash is cosmetic in a path and must not become an empty step. */
    @Test
    fun `barra no fim nao cria degrau fantasma`() {
        assertEquals(
            pathCrumbs("/opt/data").map { it.path },
            pathCrumbs("/opt/data/").map { it.path },
        )
    }

    /**
     * `//opt///data` is a valid path to the kernel. Without discarding the
     * empty segments it would produce invisible steps — touch targets nobody
     * can see that navigate to a truncated path.
     */
    @Test
    fun `barras repetidas nao produzem degraus invisiveis`() {
        val crumbs = pathCrumbs("//opt///data")

        assertEquals(listOf("/", "opt", "data"), crumbs.map { it.label })
    }
}
