package dev.servercontrolpanel.data.push

import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.model.PutNotifyPrefsInputBody
import java.io.IOException

data class NotifyRule(
    val id: String,
    val name: String,
    val minSeverity: String?,
    val typePrefix: String?,
    val enabledForDevice: Boolean,
)

sealed interface NotifyPreferencesResult {
    data class Success(val rules: List<NotifyRule>) : NotifyPreferencesResult
    data class Error(val reason: String) : NotifyPreferencesResult
}

sealed interface UpdateNotifyPreferencesResult {
    data object Success : UpdateNotifyPreferencesResult
    data class Error(val reason: String) : UpdateNotifyPreferencesResult
}

interface NotifyPreferencesSource {
    suspend fun fetch(deviceId: String): NotifyPreferencesResult
    suspend fun update(deviceId: String, enabledRuleIds: List<String>): UpdateNotifyPreferencesResult
}

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
