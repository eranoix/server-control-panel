package dev.servercontrolpanel.data.push

import android.content.Context

private const val KEY_BATTERY_PROMPT_SEEN = "battery_optimization_prompt_seen"

class PushOnboardingState(context: Context) {
    private val prefs = context.applicationContext.getSharedPreferences(PUSH_PREFS_NAME, Context.MODE_PRIVATE)

    fun hasSeenBatteryOptimizationPrompt(): Boolean = prefs.getBoolean(KEY_BATTERY_PROMPT_SEEN, false)

    fun markBatteryOptimizationPromptSeen() {
        prefs.edit().putBoolean(KEY_BATTERY_PROMPT_SEEN, true).apply()
    }
}
