package com.vpsmanager.core.sdui

import java.io.File

/**
 * Reads the shared golden corpus directly off disk (`sdui.fixtures.dir`,
 * wired in `build.gradle.kts` to `contracts/sdui/fixtures` at the repo
 * root). Tests must never read a copy — that risks passing against a stale
 * snapshot of what the fixture used to look like.
 */
object SduiFixtures {
    val directory: File by lazy {
        val path = System.getProperty("sdui.fixtures.dir")
            ?: error("sdui.fixtures.dir system property is not set — check android/core/build.gradle.kts")
        File(path).also {
            check(it.isDirectory) { "sdui.fixtures.dir does not point at a directory: $path" }
        }
    }

    fun read(name: String): String = directory.resolve(name).readText()
}
