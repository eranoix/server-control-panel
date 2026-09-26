package dev.servercontrolpanel.data.session

import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import java.io.IOException

/**
 * Outcome of loading the authenticated user's session/identity from the BFF.
 * A plain domain shape — [MobileApi]'s generated `MeResponse` DTO never
 * crosses this boundary, and no caller ever sees a raw exception.
 */
sealed interface SessionResult {
    data class Success(val user: String, val email: String, val isAdmin: Boolean) : SessionResult
    data object Empty : SessionResult
    data class Error(val reason: String) : SessionResult
}

/**
 * Abstraction consumers outside `:data` depend on (e.g. `DeployTriggerViewModel`'s admin gate
 * in `:feature-admin`), so their unit tests supply a fake instead of touching the generated
 * mobile BFF client directly — same convention as
 * [dev.servercontrolpanel.data.push.NotifyPreferencesSource]/[dev.servercontrolpanel.data.ops.OpsSource].
 */
interface SessionSource {
    suspend fun getMe(): SessionResult
}

/**
 * The single call site into the generated mobile BFF client
 * (`:data:mobile-api-client`) for session/identity data
 * (`GET /api/mobile/v1/me`). No other module may reference [MobileApi] or
 * its generated model types directly — callers only ever see [SessionResult].
 */
class SessionRepository(
    private val mobileApi: MobileApi = MobileApi(),
) : SessionSource {
    override suspend fun getMe(): SessionResult = try {
        val response = mobileApi.getMe()
        val email = response.email
        if (email.isNullOrBlank()) {
            SessionResult.Empty
        } else {
            SessionResult.Success(user = response.user, email = email, isAdmin = response.isAdmin)
        }
    } catch (e: ClientException) {
        SessionResult.Error("Could not load the session (error ${e.statusCode}).")
    } catch (e: ServerException) {
        SessionResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        SessionResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        SessionResult.Error("Configuration error while loading the session.")
    } catch (e: UnsupportedOperationException) {
        SessionResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        SessionResult.Error("Could not load the session.")
    }
}
