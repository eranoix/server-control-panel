package com.vpsmanager.feature.videocall.call

import android.content.ComponentName
import android.telecom.PhoneAccount
import android.telecom.PhoneAccountHandle
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Records every [register] call and reports [isRegistered] true only for handles previously
 * registered through this fake — enough to prove [PhoneAccountRegistrar]'s idempotency without a
 * real Telecom binder.
 */
private class FakeTelecomAccountPort : TelecomAccountPort {
    var registerCallCount = 0
        private set

    private val registeredHandles = mutableSetOf<PhoneAccountHandle>()

    override fun isRegistered(handle: PhoneAccountHandle): Boolean = handle in registeredHandles

    override fun register(account: PhoneAccount) {
        registerCallCount++
        registeredHandles += account.accountHandle
    }
}

@RunWith(RobolectricTestRunner::class)
class PhoneAccountRegistrarTest {

    private val componentName = ComponentName(
        "com.vpsmanager.app",
        "com.vpsmanager.feature.videocall.call.VpsmConnectionService",
    )

    @Test
    fun registersSelfManagedPhoneAccountOnce() {
        val port = FakeTelecomAccountPort()
        val registrar = PhoneAccountRegistrar(componentName, port)

        registrar.ensureRegistered()
        registrar.ensureRegistered()

        assertEquals(1, port.registerCallCount)
    }

    @Test
    fun reRegistersWhenTelecomNoLongerReportsTheAccount() {
        // Simulates a revoked/removed self-managed account (T-11 failure mode: OEM
        // background-restriction sweep or the user disabling it under Settings > Apps >
        // Default apps) — Telecom stops reporting it as registered, so the next
        // ensureRegistered() call must re-register rather than trust a stale local flag.
        val port = object : TelecomAccountPort {
            var registerCallCount = 0
            var revoked = false

            override fun isRegistered(handle: PhoneAccountHandle): Boolean = !revoked && registerCallCount > 0

            override fun register(account: PhoneAccount) {
                registerCallCount++
            }
        }
        val registrar = PhoneAccountRegistrar(componentName, port)

        registrar.ensureRegistered()
        port.revoked = true
        registrar.ensureRegistered()

        assertEquals(2, port.registerCallCount)
    }
}
