package dev.servercontrolpanel.data.session

import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import java.io.IOException

sealed interface SessionResult {
    data class Success(val user: String, val email: String, val isAdmin: Boolean) : SessionResult
    data object Empty : SessionResult
    data class Error(val reason: String) : SessionResult
}

interface SessionSource {
    suspend fun getMe(): SessionResult
}

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
