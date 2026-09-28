package dev.servercontrolpanel.feature.videocall.call

import android.content.ComponentName
import android.telecom.PhoneAccount
import android.telecom.PhoneAccountHandle
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

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
        "dev.servercontrolpanel.app",
        "dev.servercontrolpanel.feature.videocall.call.PanelConnectionService",
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
