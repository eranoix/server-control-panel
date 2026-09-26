package dev.servercontrolpanel.core.sdui

import java.io.File

/**
 * Reads the shared SDUI fixtures from `sdui.fixtures.dir` (the repo's
 * `contracts/sdui/fixtures`). Never read a copy, which could go stale.
 */
object SduiFixtures {
    val directory: File by lazy {
        val path = System.getProperty("sdui.fixtures.dir")
            ?: error("sdui.fixtures.dir system property is not set; check android/core/build.gradle.kts")
        File(path).also {
            check(it.isDirectory) { "sdui.fixtures.dir does not point at a directory: $path" }
        }
    }

    fun read(name: String): String = directory.resolve(name).readText()
}
