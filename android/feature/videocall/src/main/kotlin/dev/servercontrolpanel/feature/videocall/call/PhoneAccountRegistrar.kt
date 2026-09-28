package dev.servercontrolpanel.feature.videocall.call

import android.content.ComponentName
import android.content.Context
import android.telecom.PhoneAccount
import android.telecom.PhoneAccountHandle
import android.telecom.TelecomManager

private const val PHONE_ACCOUNT_ID = "panel_self_managed_account"
private const val ACCOUNT_LABEL = "Server Control Panel"

interface TelecomAccountPort {
    fun isRegistered(handle: PhoneAccountHandle): Boolean

    fun register(account: PhoneAccount)
}

private class RealTelecomAccountPort(private val telecomManager: TelecomManager) : TelecomAccountPort {
    override fun isRegistered(handle: PhoneAccountHandle): Boolean =
        runCatching { telecomManager.ownSelfManagedPhoneAccounts.contains(handle) }
            .getOrDefault(false)

    override fun register(account: PhoneAccount) {
        telecomManager.registerPhoneAccount(account)
    }
}

class PhoneAccountRegistrar(
    private val componentName: ComponentName,
    private val port: TelecomAccountPort,
) {
    constructor(context: Context, componentName: ComponentName) : this(
        componentName = componentName,
        port = RealTelecomAccountPort(context.getSystemService(TelecomManager::class.java)),
    )

    val phoneAccountHandle: PhoneAccountHandle = PhoneAccountHandle(componentName, PHONE_ACCOUNT_ID)

    fun ensureRegistered() {
        if (port.isRegistered(phoneAccountHandle)) return
        val account = PhoneAccount.builder(phoneAccountHandle, ACCOUNT_LABEL)
            .setCapabilities(PhoneAccount.CAPABILITY_SELF_MANAGED)
            .build()
        port.register(account)
    }
}
