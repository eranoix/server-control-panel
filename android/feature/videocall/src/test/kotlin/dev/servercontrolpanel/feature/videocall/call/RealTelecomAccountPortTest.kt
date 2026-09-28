package dev.servercontrolpanel.feature.videocall.call

import android.content.ComponentName
import android.telecom.PhoneAccountHandle
import android.telecom.TelecomManager
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.Implementation
import org.robolectric.annotation.Implements
import org.robolectric.shadows.ShadowTelecomManager

@Implements(TelecomManager::class)
class ShadowOwnSelfManagedPhoneAccountsOnly : ShadowTelecomManager() {
    companion object {
        val ownSelfManagedAccounts = mutableListOf<PhoneAccountHandle>()
    }

    @Implementation
    fun getOwnSelfManagedPhoneAccounts(): MutableList<PhoneAccountHandle> = ownSelfManagedAccounts
}

@RunWith(RobolectricTestRunner::class)
@Config(shadows = [ShadowOwnSelfManagedPhoneAccountsOnly::class])
class RealTelecomAccountPortTest {

    private val componentName = ComponentName(
        "dev.servercontrolpanel.app",
        "dev.servercontrolpanel.feature.videocall.call.PanelConnectionService",
    )

    @Test
    fun `ensureRegistered trusts getOwnSelfManagedPhoneAccounts, not the shared accounts map`() {
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        val telecomManager = context.getSystemService(TelecomManager::class.java)
        val registrar = PhoneAccountRegistrar(context, componentName)

        ShadowOwnSelfManagedPhoneAccountsOnly.ownSelfManagedAccounts += registrar.phoneAccountHandle

        registrar.ensureRegistered()

        assertTrue(
            "getOwnSelfManagedPhoneAccounts() must be the API consulted for isRegistered()",
            shadowOf(telecomManager).allPhoneAccounts.isEmpty(),
        )
    }

    @Test
    fun `ensureRegistered still registers when getOwnSelfManagedPhoneAccounts reports nothing`() {
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        val telecomManager = context.getSystemService(TelecomManager::class.java)
        val registrar = PhoneAccountRegistrar(context, componentName)

        telecomManager.registerPhoneAccount(
            android.telecom.PhoneAccount.builder(registrar.phoneAccountHandle, "Server Control Panel")
                .setCapabilities(android.telecom.PhoneAccount.CAPABILITY_SELF_MANAGED)
                .build(),
        )
        assertFalse(shadowOf(telecomManager).allPhoneAccounts.isEmpty())

        registrar.ensureRegistered()

        assertFalse(shadowOf(telecomManager).allPhoneAccounts.isEmpty())
    }
}
