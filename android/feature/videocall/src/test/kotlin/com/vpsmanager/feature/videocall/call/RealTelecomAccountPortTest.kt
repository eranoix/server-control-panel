package com.vpsmanager.feature.videocall.call

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

/**
 * Pins the production [PhoneAccountRegistrar] wiring to `TelecomManager.getOwnSelfManagedPhoneAccounts()`
 * -- NOT the two near-identical members [ShadowTelecomManager] already shadows generically,
 * `getSelfManagedPhoneAccounts()` (needs the dangerous `READ_PHONE_STATE`) and
 * `getPhoneAccount(handle)` (needs the dangerous `READ_PHONE_NUMBERS` since Android 12 -- the
 * exact `SecurityException` measured on the operator's device). Robolectric's shadow can't
 * reproduce that `SecurityException` (shadows don't enforce permissions), so it can't be used
 * as the pin by itself. Instead, [ShadowOwnSelfManagedPhoneAccountsOnly] gives
 * `getOwnSelfManagedPhoneAccounts()` its OWN independent backing list, completely separate from
 * [ShadowTelecomManager]'s shared `accounts` map that the other two members read from
 * (and that [TelecomAccountPort.register] writes into). By pre-seeding ONLY the
 * `getOwnSelfManagedPhoneAccounts()`-backing list (never the shared `accounts` map),
 * `ensureRegistered()` can only see the handle as "already registered" -- and skip
 * re-registering -- if it queries that exact method. If the implementation is swapped to either
 * impostor, it reads the (deliberately empty) shared map instead, "sees" nothing registered,
 * and calls [TelecomManager.registerPhoneAccount] again -- which this test catches by asserting
 * no re-registration occurred.
 */
@Implements(TelecomManager::class)
class ShadowOwnSelfManagedPhoneAccountsOnly : ShadowTelecomManager() {
    companion object {
        /** Independent of [ShadowTelecomManager]'s own `accounts` map by construction. */
        val ownSelfManagedAccounts = mutableListOf<PhoneAccountHandle>()
    }

    @Implementation
    fun getOwnSelfManagedPhoneAccounts(): MutableList<PhoneAccountHandle> = ownSelfManagedAccounts
}

@RunWith(RobolectricTestRunner::class)
@Config(shadows = [ShadowOwnSelfManagedPhoneAccountsOnly::class])
class RealTelecomAccountPortTest {

    private val componentName = ComponentName(
        "com.vpsmanager.app",
        "com.vpsmanager.feature.videocall.call.VpsmConnectionService",
    )

    @Test
    fun `ensureRegistered trusts getOwnSelfManagedPhoneAccounts, not the shared accounts map`() {
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        val telecomManager = context.getSystemService(TelecomManager::class.java)
        val registrar = PhoneAccountRegistrar(context, componentName)

        // Simulate Telecom already reporting the handle as a registered own-self-managed
        // account, but WITHOUT it being present in the shared `accounts` map that
        // getSelfManagedPhoneAccounts()/getPhoneAccount() read from. Only a check against
        // getOwnSelfManagedPhoneAccounts() specifically can observe this as "already
        // registered".
        ShadowOwnSelfManagedPhoneAccountsOnly.ownSelfManagedAccounts += registrar.phoneAccountHandle

        registrar.ensureRegistered()

        // If ensureRegistered() used getSelfManagedPhoneAccounts() or getPhoneAccount(handle)
        // instead, it would find the shared map empty, conclude "not registered", and call
        // registerPhoneAccount() -- which this asserts did NOT happen.
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

        // Pre-populate the SHARED accounts map directly (as getSelfManagedPhoneAccounts() /
        // getPhoneAccount() would see) while leaving getOwnSelfManagedPhoneAccounts()'s own
        // list empty. The correct implementation must still (re-)register, because it never
        // trusts the shared map for this check.
        telecomManager.registerPhoneAccount(
            android.telecom.PhoneAccount.builder(registrar.phoneAccountHandle, "VPS Manager")
                .setCapabilities(android.telecom.PhoneAccount.CAPABILITY_SELF_MANAGED)
                .build(),
        )
        assertFalse(shadowOf(telecomManager).allPhoneAccounts.isEmpty())

        // Must not throw even though getOwnSelfManagedPhoneAccounts() is unpopulated.
        registrar.ensureRegistered()

        assertFalse(shadowOf(telecomManager).allPhoneAccounts.isEmpty())
    }
}
