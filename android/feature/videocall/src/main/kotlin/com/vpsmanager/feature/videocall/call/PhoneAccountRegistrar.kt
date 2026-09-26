package com.vpsmanager.feature.videocall.call

import android.content.ComponentName
import android.content.Context
import android.telecom.PhoneAccount
import android.telecom.PhoneAccountHandle
import android.telecom.TelecomManager

private const val PHONE_ACCOUNT_ID = "vpsm_self_managed_account"
private const val ACCOUNT_LABEL = "Server Control Panel"

/**
 * Narrow seam over the two `android.telecom.TelecomManager` members [PhoneAccountRegistrar]
 * needs. `TelecomManager` requires a live `Context`/Binder to construct and its real methods
 * throw on the plain JVM (Android SDK stub jar) without a Robolectric shadow, matching
 * [com.vpsmanager.data.videocall.LocalMediaTrackControl]'s existing narrow-seam pattern in this
 * codebase. [RealTelecomAccountPort] wires the real manager; [PhoneAccountRegistrarTest] injects
 * a hand-rolled fake instead.
 */
interface TelecomAccountPort {
    /** True when Telecom already has an enabled account registered under [handle]. */
    fun isRegistered(handle: PhoneAccountHandle): Boolean

    fun register(account: PhoneAccount)
}

private class RealTelecomAccountPort(private val telecomManager: TelecomManager) : TelecomAccountPort {
    /**
     * Uses `getOwnSelfManagedPhoneAccounts()` (API 33+, minSdk here is 34), and
     * NOT `getPhoneAccount(handle)`.
     *
     * Measured on a real device: `getPhoneAccount` throws
     * `SecurityException: Neither user … nor current process has
     * android.permission.READ_PHONE_NUMBERS`. As of Android 12 that method
     * requires a telephony permission even for the app that owns the account —
     * and `READ_PHONE_NUMBERS` is a DANGEROUS permission, prompted at runtime,
     * for reading the user's own phone number. A self-managed video-calling app
     * has no business asking for that, and asking would be worse than the bug.
     *
     * `getOwnSelfManagedPhoneAccounts` exists for exactly this case: it returns
     * only the app's OWN self-managed accounts and requires nothing beyond
     * `MANAGE_OWN_CALLS`, which is already declared. It keeps the same
     * self-healing property as `ensureRegistered` — re-asking Telecom instead
     * of trusting a local flag.
     */
    override fun isRegistered(handle: PhoneAccountHandle): Boolean =
        runCatching { telecomManager.ownSelfManagedPhoneAccounts.contains(handle) }
            .getOrDefault(false)

    override fun register(account: PhoneAccount) {
        telecomManager.registerPhoneAccount(account)
    }
}

/**
 * Registers this app's self-managed [PhoneAccount] (`MANAGE_OWN_CALLS`) with Telecom —
 * `CAPABILITY_SELF_MANAGED` only, never `CAPABILITY_CALL_PROVIDER` (that capability requests the
 * default-dialer role this app does not want: a self-managed account gets Android's own native
 * calling UI — lock-screen, ringtone, DND bypass, Bluetooth routing — without competing for that
 * role).
 *
 * [ensureRegistered] re-checks Telecom itself via [TelecomAccountPort.isRegistered] on every
 * call rather than trusting a one-time local flag — a self-managed account can be silently
 * revoked outside this app's control (the user disabling it under Settings > Apps > Default
 * apps > Calling accounts, or an OEM's background-restriction/battery-optimization sweep
 * clearing it), and this is the one place that failure mode gets detected and self-healed,
 * instead of every subsequent `addNewIncomingCall` failing with no indication why.
 */
class PhoneAccountRegistrar(
    private val componentName: ComponentName,
    private val port: TelecomAccountPort,
) {
    constructor(context: Context, componentName: ComponentName) : this(
        componentName = componentName,
        port = RealTelecomAccountPort(context.getSystemService(TelecomManager::class.java)),
    )

    val phoneAccountHandle: PhoneAccountHandle = PhoneAccountHandle(componentName, PHONE_ACCOUNT_ID)

    /** No-op if Telecom already reports [phoneAccountHandle] as registered and enabled. */
    fun ensureRegistered() {
        if (port.isRegistered(phoneAccountHandle)) return
        val account = PhoneAccount.builder(phoneAccountHandle, ACCOUNT_LABEL)
            .setCapabilities(PhoneAccount.CAPABILITY_SELF_MANAGED)
            .build()
        port.register(account)
    }
}
