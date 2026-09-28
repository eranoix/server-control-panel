package dev.servercontrolpanel.benchmark

import android.os.Bundle
import android.widget.FrameLayout
import androidx.activity.ComponentActivity

class BenchmarkHostActivity : ComponentActivity() {

    lateinit var root: FrameLayout
        private set

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        root = FrameLayout(this)
        setContentView(root)
    }
}
