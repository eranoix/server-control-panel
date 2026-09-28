package dev.servercontrolpanel.data.events

import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import java.io.IOException

sealed interface WsTicketResult {
    data class Success(val ticket: String, val expiresIn: Int) : WsTicketResult
    data class Error(val reason: String) : WsTicketResult
}

interface MobileEventsTicketSource {
    suspend fun wsTicket(): WsTicketResult
}

class MobileEventsRepository(
    private val mobileApi: MobileApi = MobileApi(),
) : MobileEventsTicketSource {

    override suspend fun wsTicket(): WsTicketResult = try {
        val response = mobileApi.issueMobileEventsWSTicket()
        WsTicketResult.Success(ticket = response.ticket, expiresIn = response.expiresIn.toInt())
    } catch (e: ClientException) {
        WsTicketResult.Error("Could not connect to the event stream (error ${e.statusCode}).")
    } catch (e: ServerException) {
        WsTicketResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        WsTicketResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        WsTicketResult.Error("Configuration error while connecting to the event stream.")
    } catch (e: UnsupportedOperationException) {
        WsTicketResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        WsTicketResult.Error("Could not connect to the event stream.")
    }
}
