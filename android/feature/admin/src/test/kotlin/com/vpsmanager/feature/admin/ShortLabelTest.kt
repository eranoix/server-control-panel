package com.vpsmanager.feature.admin

import org.junit.Assert.assertEquals
import org.junit.Test

class ShortLabelTest {

    @Test
    fun `an enumeration in parentheses is dropped`() {
        assertEquals("Metrics", shortLabel("Metrics (CPU, memory, disk)"))
    }

    @Test
    fun `an acronym in parentheses is kept`() {
        assertEquals("Firewall (UFW)", shortLabel("Firewall (UFW)"))
        assertEquals("AdGuard (DNS)", shortLabel("AdGuard (DNS)"))
        assertEquals("Models (AI)", shortLabel("Models (AI)"))
    }

    @Test
    fun `a label without parentheses is unchanged`() {
        assertEquals("Containers", shortLabel("Containers"))
        assertEquals("Job queue", shortLabel("Job queue"))
    }

    @Test
    fun `a parenthetical in the middle leaves no gap or double space`() {
        assertEquals("System services", shortLabel("System (systemd and related) services"))
    }

    @Test
    fun `if the parenthetical is the whole name, the original is returned`() {
        assertEquals("(no label defined)", shortLabel("(no label defined)"))
    }

    @Test
    fun `an unclosed parenthesis does not break`() {
        assertEquals("Metrics (CPU", shortLabel("Metrics (CPU"))
    }

    @Test
    fun `surrounding whitespace is trimmed`() {
        assertEquals("Processes", shortLabel("  Processes  "))
    }
}
