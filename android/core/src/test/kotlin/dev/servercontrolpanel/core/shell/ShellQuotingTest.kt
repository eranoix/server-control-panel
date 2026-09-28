package dev.servercontrolpanel.core.shell

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

class ShellQuotingTest {

    @Test
    fun `a plain path gets no quotes`() {
        assertEquals("/opt/panel/data/mobile-inbox/photo.jpg", shellQuoted("/opt/panel/data/mobile-inbox/photo.jpg"))
    }

    @Test
    fun `a path with a space gets single quotes`() {
        assertEquals("'/tmp/Screen shot.png'", shellQuoted("/tmp/Screen shot.png"))
    }

    @Test
    fun `a single quote in the name is closed, escaped and reopened`() {
        assertEquals("""'/tmp/john'\''s photo.jpg'""", shellQuoted("/tmp/john's photo.jpg"))
    }

    @Test
    fun `empty text becomes an explicit empty argument`() {
        assertEquals("''", shellQuoted(""))
    }

    @Test
    fun `dangerous metacharacters are neutralized`() {
        listOf("/tmp/a;rm -rf b", "/tmp/\$(id)", "/tmp/`id`", "/tmp/a|b", "/tmp/a&b", "/tmp/a\nb", "/tmp/a*b").forEach { raw ->
            val quoted = shellQuoted(raw)
            assertTrue("should be quoted: $raw -> $quoted", quoted.startsWith("'") && quoted.endsWith("'"))
        }
    }

    @Test
    fun `inserting several paths separates them with spaces and ends with a space`() {
        val text = shellInsertionText(listOf("/tmp/a.png", "/tmp/b c.png"))
        assertEquals("/tmp/a.png '/tmp/b c.png' ", text)
    }

    @Test
    fun `insertion never ends with a newline`() {
        val text = shellInsertionText(listOf("/tmp/a.png", "/tmp/b.png"))
        assertTrue(!text.contains('\n'))
        assertTrue(text.endsWith(" "))
    }

    @Test
    fun `an empty list inserts nothing`() {
        assertEquals("", shellInsertionText(emptyList()))
    }

    @Test
    fun `a path with a space reaches the shell whole`() {
        assertEquals(listOf("/tmp/Screen shot.png"), argsSeenByShell("/tmp/Screen shot.png"))
    }

    @Test
    fun `a name with a single quote reaches the shell whole`() {
        assertEquals(listOf("/tmp/john's photo.jpg"), argsSeenByShell("/tmp/john's photo.jpg"))
    }

    @Test
    fun `a name with command substitution executes nothing`() {
        assertEquals(listOf("/tmp/\$(id).png"), argsSeenByShell("/tmp/\$(id).png"))
    }

    @Test
    fun `two inserted paths arrive as two arguments`() {
        val seen = argsSeenByShell("/tmp/one two.png", "/tmp/three;four.png")
        assertEquals(listOf("/tmp/one two.png", "/tmp/three;four.png"), seen)
    }

    private fun argsSeenByShell(vararg paths: String): List<String> {
        assumeTrue(File("/bin/sh").exists())
        val insertion = shellInsertionText(paths.toList())
        val process = ProcessBuilder("/bin/sh", "-c", "printf '%s\\n' $insertion")
            .redirectErrorStream(true)
            .start()
        val output = process.inputStream.bufferedReader().readText()
        process.waitFor()
        return output.split("\n").filter { it.isNotEmpty() }
    }
}
