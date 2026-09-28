package dev.servercontrolpanel.data.events

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

@Serializable
data class ClientOp(val op: String, val channel: String)

@Serializable
data class SubscribedAck(val op: String, val channel: String)

@Serializable
data class MobileEvent(val v: Int, val channel: String, val type: String, val data: JsonElement)
