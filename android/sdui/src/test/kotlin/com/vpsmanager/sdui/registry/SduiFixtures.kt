package com.vpsmanager.sdui.registry

import java.io.File

/**
 * Reads the real fixture corpus at `contracts/sdui/fixtures` (wired by
 * `:sdui`'s `build.gradle.kts` via the `sdui.fixtures.dir` system property,
 * the same mechanism `:core`'s tests use) — never a copy pasted into test
 * source, so a fixture edit is guaranteed to be seen here too.
 */
internal object SduiFixtures {
    val directory: File by lazy {
        val path = System.getProperty("sdui.fixtures.dir")
            ?: error("sdui.fixtures.dir system property not set — check :sdui build.gradle.kts")
        File(path).also {
            check(it.isDirectory) { "sdui.fixtures.dir does not point to a directory: $path" }
        }
    }

    fun read(name: String): String = File(directory, name).readText()
}
