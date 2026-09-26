package dev.servercontrolpanel.feature.auth

import android.Manifest
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf

/**
 * Renders [PairingScanScreen] under Robolectric.
 *
 * Guards that the screen degrades instead of crashing when the camera is
 * unavailable: each cause is injected through [CameraEnvironment] and must render
 * an explanation plus a way out.
 *
 * Causes are injected rather than relying on the emulator's camera config, which
 * is shared and unreliable. The happy path (real camera, QR decoding) still
 * needs a device.
 */
@RunWith(RobolectricTestRunner::class)
class PairingScanScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun grantCameraPermission() {
        val context = ApplicationProvider.getApplicationContext<android.app.Application>()
        shadowOf(context).grantPermissions(Manifest.permission.CAMERA)
    }

    private fun environment(
        hasPermission: Boolean = true,
        canAskAgain: Boolean = true,
        vararg readinesses: CameraReadiness,
    ): CameraEnvironment {
        var call = 0
        return CameraEnvironment(
            hasPermission = { hasPermission },
            canAskPermissionAgain = { canAskAgain },
            cameraCheck = { readinesses[minOf(call++, readinesses.size - 1)] },
        )
    }

    private fun render(environment: CameraEnvironment, onManual: () -> Unit = {}, onLogin: () -> Unit = {}) {
        composeRule.setContent {
            PairingScanContent(
                modifier = Modifier,
                onPairingScanned = {},
                onManualSetupRequested = onManual,
                onLoginRequested = onLogin,
                environment = environment,
            )
        }
    }

    @Test
    fun `with no camera on the device the screen degrades with the alternative path`() {
        render(environment(readinesses = arrayOf(CameraReadiness.Unavailable(CameraFailure.NoCamera))))

        composeRule.onNodeWithText("This device has no camera").assertExists()
        composeRule.onNodeWithText(LABEL_SET_UP_MANUALLY).assertHasClickAction()
        composeRule.onNodeWithText(LABEL_ALREADY_HAVE_ACCESS).assertHasClickAction()
    }

    /**
     * Through the public path with no injection: Robolectric's `CameraManager`
     * exposes no camera, so the degraded screen must appear, not a blank preview or a crash.
     */
    @Test
    fun `through the real path, a device without a camera reaches the degraded state`() {
        grantCameraPermission()

        composeRule.setContent { PairingScanScreen(onPairingScanned = {}, onManualSetupRequested = {}) }

        composeRule.onNodeWithText("This device has no camera").assertExists()
        composeRule.onNodeWithText(LABEL_SET_UP_MANUALLY).assertExists()
    }

    @Test
    fun `renders without crashing before permission is granted`() {
        composeRule.setContent { PairingScanScreen(onPairingScanned = {}) }

        composeRule.onRoot().assertExists()
    }

    @Test
    fun `while permission is requested the alternative paths stay available`() {
        render(
            environment(
                hasPermission = false,
                readinesses = arrayOf(CameraReadiness.Ready(useFrontCamera = false)),
            ),
        )

        composeRule.onNodeWithText(LABEL_SET_UP_MANUALLY).assertHasClickAction()
        composeRule.onNodeWithText(LABEL_ALREADY_HAVE_ACCESS).assertHasClickAction()
    }

    @Test
    fun `a camera in use offers retry, and retrying checks again`() {
        render(
            environment(
                readinesses = arrayOf(
                    CameraReadiness.Unavailable(CameraFailure.CameraInUse),
                    CameraReadiness.Unavailable(CameraFailure.NoCamera),
                ),
            ),
        )

        composeRule.onNodeWithText("The camera is in use").assertExists()
        composeRule.onNodeWithText("Try again").performClick()
        composeRule.waitForIdle()

        // A different cause on the second check proves the button really re-checked.
        composeRule.onNodeWithText("This device has no camera").assertExists()
    }

    @Test
    fun `a denied permission asks again instead of sending to Settings`() {
        var requests = 0
        composeRule.setContent {
            CameraUnavailableContent(
                failure = CameraFailure.PermissionDenied,
                onRequestPermission = { requests++ },
                onOpenSettings = { throw AssertionError("must not open Settings here") },
                onRetry = { throw AssertionError("must not re-check without permission") },
                onManualSetupRequested = {},
                onLoginRequested = {},
            )
        }

        composeRule.onNodeWithText("No camera access").assertExists()
        composeRule.onNodeWithText("Allow camera access").performClick()

        assertEquals(1, requests)
    }

    @Test
    fun `a permanently denied permission opens Settings instead of the dialog`() {
        var settingsOpened = 0
        composeRule.setContent {
            CameraUnavailableContent(
                failure = CameraFailure.PermissionBlocked,
                onRequestPermission = { throw AssertionError("asking again here is the loop we must avoid") },
                onOpenSettings = { settingsOpened++ },
                onRetry = {},
                onManualSetupRequested = {},
                onLoginRequested = {},
            )
        }

        composeRule.onNodeWithText("Camera access blocked").assertExists()
        composeRule.onNodeWithText("Open app settings").performClick()

        assertEquals(1, settingsOpened)
    }

    @Test
    fun `no camera offers no retry, only the alternative paths`() {
        var manual = 0
        var login = 0
        composeRule.setContent {
            CameraUnavailableContent(
                failure = CameraFailure.NoCamera,
                onRequestPermission = {},
                onOpenSettings = {},
                onRetry = { throw AssertionError("nothing to retry without a camera") },
                onManualSetupRequested = { manual++ },
                onLoginRequested = { login++ },
            )
        }

        composeRule.onNodeWithText("Try again").assertDoesNotExist()
        composeRule.onNodeWithText(LABEL_SET_UP_MANUALLY).performClick()
        composeRule.onNodeWithText(LABEL_ALREADY_HAVE_ACCESS).performClick()

        assertEquals(1, manual)
        assertEquals(1, login)
    }

    @Test
    fun `an unexpected failure shows the technical detail for the owner to report`() {
        render(
            environment(
                readinesses = arrayOf(
                    CameraReadiness.Unavailable(
                        CameraFailure.UnexpectedFailure("IllegalStateException: provider did not start"),
                    ),
                ),
            ),
        )

        composeRule.onNodeWithText("Could not open the camera").assertExists()
        composeRule.onNodeWithText("IllegalStateException: provider did not start").assertExists()
        composeRule.onNodeWithText("Try again").assertHasClickAction()
    }

    @Test
    fun `a camera blocked by the system has its own text, distinct from in use`() {
        render(
            environment(
                readinesses = arrayOf(
                    CameraReadiness.Unavailable(CameraFailure.CameraBlockedBySystem),
                ),
            ),
        )

        composeRule.onNodeWithText("The camera is disabled by the system").assertExists()
        composeRule.onNodeWithText("The camera is in use").assertDoesNotExist()
    }

    @Test
    fun `every degraded screen keeps both alternatives to QR pairing`() {
        val causes = listOf(
            CameraFailure.PermissionDenied,
            CameraFailure.PermissionBlocked,
            CameraFailure.NoCamera,
            CameraFailure.CameraInUse,
            CameraFailure.CameraBlockedBySystem,
            CameraFailure.UnexpectedFailure("x"),
        )
        var currentCause by mutableStateOf<CameraFailure>(CameraFailure.PermissionDenied)

        composeRule.setContent {
            CameraUnavailableContent(
                failure = currentCause,
                onRequestPermission = {},
                onOpenSettings = {},
                onRetry = {},
                onManualSetupRequested = {},
                onLoginRequested = {},
            )
        }

        causes.forEach { cause ->
            currentCause = cause
            composeRule.waitForIdle()
            composeRule.onNodeWithText(LABEL_SET_UP_MANUALLY).assertHasClickAction()
            composeRule.onNodeWithText(LABEL_ALREADY_HAVE_ACCESS).assertHasClickAction()
        }
        assertTrue("no cause may be left without a way out", causes.isNotEmpty())
    }
}
