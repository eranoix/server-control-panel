package com.vpsmanager.data.push

import android.content.Context

private const val KEY_BATTERY_PROMPT_SEEN = "battery_optimization_prompt_seen"

/**
 * The one boolean this app persists about push onboarding beyond the `device_id` itself:
 * whether [com.vpsmanager.feature.notifications.fcm.BatteryOptimizationPrompt] has already been
 * shown once. Reuses [PUSH_PREFS_NAME] rather than introducing a second SharedPreferences file
 * for one flag.
 */
class PushOnboardingState(context: Context) {
    private val prefs = context.applicationContext.getSharedPreferences(PUSH_PREFS_NAME, Context.MODE_PRIVATE)

    fun hasSeenBatteryOptimizationPrompt(): Boolean = prefs.getBoolean(KEY_BATTERY_PROMPT_SEEN, false)

    fun markBatteryOptimizationPromptSeen() {
        prefs.edit().putBoolean(KEY_BATTERY_PROMPT_SEEN, true).apply()
    }
}
