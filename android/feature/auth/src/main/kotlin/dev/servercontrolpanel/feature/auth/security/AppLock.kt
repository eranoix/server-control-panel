package dev.servercontrolpanel.feature.auth.security

import android.os.Build
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.fragment.app.FragmentActivity

object AppLock {

    private val BIOMETRIC_OR_PIN =
        BiometricManager.Authenticators.BIOMETRIC_STRONG or
            BiometricManager.Authenticators.DEVICE_CREDENTIAL

    fun available(activity: FragmentActivity): Boolean =
        BiometricManager.from(activity).canAuthenticate(supportedAuthenticators()) ==
            BiometricManager.BIOMETRIC_SUCCESS

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
                .setSubtitle("This app has a terminal with access to the server.")
                .setAllowedAuthenticators(supportedAuthenticators())
                .build(),
        )
    }

    private fun supportedAuthenticators(): Int =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            BIOMETRIC_OR_PIN
        } else {
            BiometricManager.Authenticators.BIOMETRIC_STRONG
        }
}
