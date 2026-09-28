package dev.servercontrolpanel.sdui.registry

import java.io.File

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
