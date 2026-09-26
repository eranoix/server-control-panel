package dev.servercontrolpanel.data.push

import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.model.PutNotifyPrefsInputBody
import java.io.IOException

/** One category (server-side Rule) this device can be pushed for, and whether it currently is. */
data class NotifyRule(
    val id: String,
    val name: String,
    val minSeverity: String?,
    val typePrefix: String?,
    val enabledForDevice: Boolean,
)

/** Outcome of reading this device's current preferences (`GET /api/mobile/v1/notify/preferences`). */
sealed interface NotifyPreferencesResult {
    data class Success(val rules: List<NotifyRule>) : NotifyPreferencesResult
    data class Error(val reason: String) : NotifyPreferencesResult
}

/** Outcome of persisting this device's preferences (`PUT /api/mobile/v1/notify/preferences`). */
sealed interface UpdateNotifyPreferencesResult {
    data object Success : UpdateNotifyPreferencesResult
    data class Error(val reason: String) : UpdateNotifyPreferencesResult
}

/**
 * Abstraction `NotificationPreferencesViewModel` (`:feature-notifications`) depends on, so its
 * unit tests supply a fake instead of touching the generated mobile BFF client directly
 * same convention as `dev.servercontrolpanel.data.terminal.TerminalSessionsSource`.
 */
interface NotifyPreferencesSource {
    suspend fun fetch(deviceId: String): NotifyPreferencesResult
    suspend fun update(deviceId: String, enabledRuleIds: List<String>): UpdateNotifyPreferencesResult
}

/**
 * The single call site into the generated mobile BFF client for this device's push-category
 * preferences — mirrors [PushDeviceRepository]'s shape. [fetch] reads the server's Rule catalog
 * plus which of them this `device_id` currently receives (the server, not this repository, owns
 * the alert-fatigue default of "critical-only until changed" — see `notify_prefs.go`); [update]
 * persists the full replacement set of enabled Rule IDs for that same device, immediately, with
 * no separate "save" step.
 */
class NotifyPreferencesRepository(
    private val mobileApi: MobileApi = MobileApi(),
) : NotifyPreferencesSource {
    override suspend fun fetch(deviceId: String): NotifyPreferencesResult = try {
        val response = mobileApi.getNotifyPreferences(deviceId)
        NotifyPreferencesResult.Success(
            (response.rules ?: emptyList()).map {
                NotifyRule(
                    id = it.id,
                    name = it.name,
                    minSeverity = it.minSeverity,
                    typePrefix = it.typePrefix,
                    enabledForDevice = it.enabledForDevice,
                )
            },
        )
    } catch (e: ClientException) {
        NotifyPreferencesResult.Error("Could not load the preferences (error ${e.statusCode}).")
    } catch (e: ServerException) {
        NotifyPreferencesResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        NotifyPreferencesResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        NotifyPreferencesResult.Error("Configuration error while loading preferences.")
    } catch (e: UnsupportedOperationException) {
        NotifyPreferencesResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        NotifyPreferencesResult.Error("Could not load the preferences.")
    }

    override suspend fun update(deviceId: String, enabledRuleIds: List<String>): UpdateNotifyPreferencesResult = try {
        mobileApi.putNotifyPreferences(
            PutNotifyPrefsInputBody(deviceId = deviceId, enabledRuleIds = enabledRuleIds),
        )
        UpdateNotifyPreferencesResult.Success
    } catch (e: ClientException) {
        UpdateNotifyPreferencesResult.Error("Could not save the preference (error ${e.statusCode}).")
    } catch (e: ServerException) {
        UpdateNotifyPreferencesResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        UpdateNotifyPreferencesResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        UpdateNotifyPreferencesResult.Error("Configuration error while saving the preference.")
    } catch (e: UnsupportedOperationException) {
        UpdateNotifyPreferencesResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        UpdateNotifyPreferencesResult.Error("Could not save the preference.")
    }
}
