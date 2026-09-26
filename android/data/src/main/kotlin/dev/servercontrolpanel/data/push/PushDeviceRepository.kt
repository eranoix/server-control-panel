package dev.servercontrolpanel.data.push

import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.model.RegisterDeviceInputBody
import java.io.IOException

/** Outcome of registering/unregistering this device for push (`/api/mobile/v1/notify/devices`). */
sealed interface PushDeviceResult {
    data object Success : PushDeviceResult
    data class Error(val reason: String) : PushDeviceResult
}

/**
 * The single call site into the generated mobile BFF client (`:data:mobile-api-client`) for
 * FCM device registration — mirrors `dev.servercontrolpanel.data.terminal.TerminalRepository`'s exact
 * shape. Calls the real, only device-registration endpoints
 * (`POST`/`DELETE /api/mobile/v1/notify/devices`) — never the non-existent
 * `/api/mobile/v1/devices/register` an earlier draft cited.
 */
class PushDeviceRepository(
    private val mobileApi: MobileApi = MobileApi(),
) {
    suspend fun register(deviceId: String, fcmToken: String, platform: String = "android"): PushDeviceResult = try {
        mobileApi.registerPushDevice(
            RegisterDeviceInputBody(deviceId = deviceId, fcmToken = fcmToken, platform = platform),
        )
        PushDeviceResult.Success
    } catch (e: ClientException) {
        PushDeviceResult.Error("Could not register this device for notifications (error ${e.statusCode}).")
    } catch (e: ServerException) {
        PushDeviceResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        PushDeviceResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        PushDeviceResult.Error("Configuration error while registering the device.")
    } catch (e: UnsupportedOperationException) {
        PushDeviceResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        PushDeviceResult.Error("Could not register this device.")
    }

    suspend fun unregister(deviceId: String): PushDeviceResult = try {
        mobileApi.unregisterPushDevice(deviceId)
        PushDeviceResult.Success
    } catch (e: ClientException) {
        PushDeviceResult.Error("Could not remove this device (error ${e.statusCode}).")
    } catch (e: ServerException) {
        PushDeviceResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        PushDeviceResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        PushDeviceResult.Error("Configuration error while removing the device.")
    } catch (e: UnsupportedOperationException) {
        PushDeviceResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        PushDeviceResult.Error("Could not remove this device.")
    }
}
