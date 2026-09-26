package com.vpsmanager.benchmark

import android.os.Bundle
import android.widget.FrameLayout
import androidx.activity.ComponentActivity

/**
 * Minimal real-Window host for [com.vpsmanager.benchmark.GridThroughputBenchmark]:
 * a bare [ComponentActivity] whose only job is to give a renderer under test
 * (Compose `TerminalCanvas` or `TerminalSurfaceGrid`) a genuine attached
 * `Window`/`Surface` to draw into, since `Window.addOnFrameMetricsAvailableListener`
 * requires a real window -- a Robolectric shadow or a headless View cannot
 * produce real frame timings.
 */
class BenchmarkHostActivity : ComponentActivity() {

    lateinit var root: FrameLayout
        private set

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        root = FrameLayout(this)
        setContentView(root)
    }
}
