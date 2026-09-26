package dev.servercontrolpanel.feature.auth.security

import android.os.Build
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.fragment.app.FragmentActivity

/**
 * What the device accepts as proof that this is its owner.
 *
 * [BIOMETRIC_OR_PIN] combines strong biometrics with the device credential.
 * Biometrics alone fail precisely for the person with a dirty finger or a wet
 * screen — the situation of someone working on a server in a hurry — and a
 * defence that fails at the wrong moment becomes an obstacle, not protection.
 */
object AppLock {

    private val BIOMETRIC_OR_PIN =
        BiometricManager.Authenticators.BIOMETRIC_STRONG or
            BiometricManager.Authenticators.DEVICE_CREDENTIAL

    /**
     * Can this device demand any proof at all?
     *
     * A device with no biometrics AND no PIN cannot — and in that case the
     * option must NOT be offered. Offering a switch that simply does nothing
     * when turned on is worse than having no switch: the person is left
     * believing they are protected.
     */
    fun available(activity: FragmentActivity): Boolean =
        BiometricManager.from(activity).canAuthenticate(supportedAuthenticators()) ==
            BiometricManager.BIOMETRIC_SUCCESS

    /**
     * Asks for the proof. [onUnlock] is only called once the system confirms.
     *
     * [onGiveUp] covers an error AND a cancellation through the same path on
     * purpose: from the caller's point of view, "the sensor failed" and "the
     * person pressed cancel" lead to the same decision — do not unlock.
     * Distinguishing the two here would only produce two paths doing the same
     * thing.
     */
    fun request(
        activity: FragmentActivity,
        onUnlock: () -> Unit,
        onGiveUp: () -> Unit,
    ) {
        val prompt = BiometricPrompt(
            activity,
            androidx.core.content.ContextCompat.getMainExecutor(activity),
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    onUnlock()
                }

                override fun onAuthenticationError(code: Int, message: CharSequence) {
                    onGiveUp()
                }
            },
        )
        prompt.authenticate(
            BiometricPrompt.PromptInfo.Builder()
                .setTitle("Unlock Server Control Panel")
                // The text says WHAT IS BEHIND the door, not "confirm your
                // identity". Whoever reads it needs to know why it is worth it.
                .setSubtitle("This app has a terminal with access to the server.")
                .setAllowedAuthenticators(supportedAuthenticators())
                .build(),
        )
    }

    /**
     * Before Android 11 the BIOMETRIC_STRONG + DEVICE_CREDENTIAL combination
     * throws `IllegalArgumentException` instead of degrading on its own. The
     * app's `minSdk` is 34, so in practice this never triggers — the check
     * stays because a lower `minSdk` in the future would make the exception
     * surface on the path that unlocks the app, which is the worst possible
     * place to discover it.
     */
    private fun supportedAuthenticators(): Int =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            BIOMETRIC_OR_PIN
        } else {
            BiometricManager.Authenticators.BIOMETRIC_STRONG
        }
}
