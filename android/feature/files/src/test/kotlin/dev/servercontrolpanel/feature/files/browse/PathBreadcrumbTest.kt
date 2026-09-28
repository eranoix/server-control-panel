package dev.servercontrolpanel.feature.files.browse

import org.junit.Assert.assertEquals
import org.junit.Test

class PathBreadcrumbTest {

    @Test
    fun `the root is a step, not an empty label`() {
        val crumbs = pathCrumbs("/")

        assertEquals(1, crumbs.size)
        assertEquals("/", crumbs.single().label)
        assertEquals("/", crumbs.single().path)
    }

    @Test
    fun `each step points at its own level, accumulated from the root`() {
        val crumbs = pathCrumbs("/opt/panel/data")

        assertEquals(listOf("/", "opt", "panel", "data"), crumbs.map { it.label })
        assertEquals(
            listOf("/", "/opt", "/opt/panel", "/opt/panel/data"),
            crumbs.map { it.path },
        )
    }

    @Test
    fun `a trailing slash does not create a phantom step`() {
        assertEquals(
            pathCrumbs("/opt/data").map { it.path },
            pathCrumbs("/opt/data/").map { it.path },
        )
    }

    @Test
    fun `repeated slashes do not produce invisible steps`() {
        val crumbs = pathCrumbs("//opt///data")

        assertEquals(listOf("/", "opt", "data"), crumbs.map { it.label })
    }
}
