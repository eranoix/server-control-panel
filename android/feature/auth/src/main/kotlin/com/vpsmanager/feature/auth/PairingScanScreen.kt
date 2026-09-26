package com.vpsmanager.feature.auth

import android.Manifest
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview as CameraPreview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.google.zxing.BinaryBitmap
import com.google.zxing.NotFoundException
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.qrcode.QRCodeReader
import com.vpsmanager.data.auth.PairingPayload
import com.vpsmanager.data.auth.PairingRepository
import java.util.concurrent.Executors

/** Label of the shortcut to [ServerSetupScreen], used everywhere on this screen. */
internal const val LABEL_SET_UP_MANUALLY = "Set up manually"

/** Label of the shortcut to [LoginScreen] (passkey, or username and password). */
internal const val LABEL_ALREADY_HAVE_ACCESS = "I already have access"

/**
 * Scans the panel's pairing QR with CameraX and pure-JVM zxing (no ML Kit or
 * Play Services). Decoded frames that are not a valid pairing envelope are
 * ignored so scanning continues. [onPairingScanned] fires at most once.
 *
 * Camera unavailability must never crash the app: every failure becomes
 * [ScannerState.Degraded] with an explanation and the exits
 * [onManualSetupRequested] and [onLoginRequested].
 */
@Composable
fun PairingScanScreen(
    modifier: Modifier = Modifier,
    onPairingScanned: (PairingPayload) -> Unit,
    onManualSetupRequested: (() -> Unit)? = null,
    onLoginRequested: (() -> Unit)? = null,
) {
    PairingScanContent(
        modifier = modifier,
        onPairingScanned = onPairingScanned,
        onManualSetupRequested = onManualSetupRequested,
        onLoginRequested = onLoginRequested,
        environment = remember { CameraEnvironment() },
    )
}

/**
 * [PairingScanScreen]'s body with [CameraEnvironment] exposed so JVM tests can
 * inject each failure cause.
 */
@Composable
internal fun PairingScanContent(
    modifier: Modifier = Modifier,
    onPairingScanned: (PairingPayload) -> Unit,
    onManualSetupRequested: (() -> Unit)?,
    onLoginRequested: (() -> Unit)?,
    environment: CameraEnvironment,
) {
    val context = LocalContext.current
    var state by remember { mutableStateOf<ScannerState>(ScannerState.Checking) }
    // Each increment re-runs the check (retry, or return from Settings).
    var attempt by remember { mutableStateOf(0) }
    // Saveable so rotation does not re-ask; repeated requests get the dialog suppressed by the system.
    var alreadyAskedPermission by rememberSaveable { mutableStateOf(false) }

    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted ->
        if (granted) {
            attempt++
        } else {
            state = ScannerState.Degraded(
                permissionFailure(environment.canAskPermissionAgain(context)),
            )
        }
    }

    LaunchedEffect(attempt) {
        state = ScannerState.Checking
        if (!environment.hasPermission(context)) {
            if (alreadyAskedPermission) {
                // Already asked once here; do not ask again unprompted.
                state = ScannerState.Degraded(
                    permissionFailure(environment.canAskPermissionAgain(context)),
                )
            } else {
                alreadyAskedPermission = true
                state = ScannerState.RequestingPermission
                permissionLauncher.launch(Manifest.permission.CAMERA)
            }
            return@LaunchedEffect
        }
        state = when (val readiness = environment.cameraCheck.check(context)) {
            is CameraReadiness.Ready -> ScannerState.Scanning(readiness.useFrontCamera)
            is CameraReadiness.Unavailable -> ScannerState.Degraded(readiness.failure)
        }
    }

    // Returning from Settings with the permission granted re-checks automatically.
    // It only fires when the permission actually changed, so it cannot loop.
    val currentState by rememberUpdatedState(state)
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            val awaitingPermission = (currentState as? ScannerState.Degraded)?.failure.let {
                it is CameraFailure.PermissionDenied || it is CameraFailure.PermissionBlocked
            }
            if (event == Lifecycle.Event.ON_RESUME && awaitingPermission && environment.hasPermission(context)) {
                attempt++
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    when (val current = state) {
        // Keep the exits visible even here: a lone spinner is a dead end if the
        // permission dialog is dismissed externally.
        ScannerState.Checking,
        ScannerState.RequestingPermission,
        -> Box(modifier = modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator()
            PairingExits(
                onManualSetupRequested = onManualSetupRequested,
                onLoginRequested = onLoginRequested,
                overVideo = false,
            )
        }

        is ScannerState.Scanning -> QrCameraPreview(
            modifier = modifier,
            useFrontCamera = current.useFrontCamera,
            onPairingScanned = onPairingScanned,
            onManualSetupRequested = onManualSetupRequested,
            onLoginRequested = onLoginRequested,
            onFailure = { error ->
                state = ScannerState.Degraded(
                    classifyCameraFailure(error, countDeviceCameras(context)),
                )
            },
            onStateFailure = { failure -> state = ScannerState.Degraded(failure) },
        )

        is ScannerState.Degraded -> CameraUnavailableContent(
            modifier = modifier,
            failure = current.failure,
            onRequestPermission = { permissionLauncher.launch(Manifest.permission.CAMERA) },
            onOpenSettings = { openAppSettings(context) },
            onRetry = { attempt++ },
            onManualSetupRequested = onManualSetupRequested,
            onLoginRequested = onLoginRequested,
        )
    }
}

@Composable
private fun QrCameraPreview(
    modifier: Modifier,
    useFrontCamera: Boolean,
    onPairingScanned: (PairingPayload) -> Unit,
    onManualSetupRequested: (() -> Unit)?,
    onLoginRequested: (() -> Unit)?,
    onFailure: (Throwable) -> Unit,
    onStateFailure: (CameraFailure) -> Unit,
) {
    val lifecycleOwner = LocalLifecycleOwner.current
    val pairingRepository = remember { PairingRepository() }
    var alreadyScanned by remember { mutableStateOf(false) }
    val currentOnPairingScanned by rememberUpdatedState(onPairingScanned)
    val currentOnFailure by rememberUpdatedState(onFailure)
    val currentOnStateFailure by rememberUpdatedState(onStateFailure)
    // Held here so the DisposableEffect below can shut it down; otherwise each visit leaks a thread.
    val analysisExecutor = remember { Executors.newSingleThreadExecutor() }
    val selector = remember(useFrontCamera) {
        if (useFrontCamera) CameraSelector.DEFAULT_FRONT_CAMERA else CameraSelector.DEFAULT_BACK_CAMERA
    }

    DisposableEffect(Unit) {
        onDispose { analysisExecutor.shutdown() }
    }

    Box(modifier = modifier.fillMaxSize()) {
        AndroidView(
            modifier = Modifier.fillMaxSize(),
            factory = { ctx ->
                val previewView = PreviewView(ctx)
                val cameraProviderFuture = ProcessCameraProvider.getInstance(ctx)
                cameraProviderFuture.addListener(
                    {
                        // Runs on the main executor, where an escaping exception
                        // kills the process, so the try must also cover `get()`.
                        try {
                            val cameraProvider = cameraProviderFuture.get()
                            val preview = CameraPreview.Builder().build().also {
                                it.surfaceProvider = previewView.surfaceProvider
                            }
                            val analysis = ImageAnalysis.Builder()
                                .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                                .build()
                            analysis.setAnalyzer(analysisExecutor) { imageProxy ->
                                if (!alreadyScanned) {
                                    decodeQr(imageProxy)?.let { rawPayload ->
                                        pairingRepository.parsePairingPayload(rawPayload)?.let { pairingPayload ->
                                            alreadyScanned = true
                                            currentOnPairingScanned(pairingPayload)
                                        }
                                    }
                                }
                                imageProxy.close()
                            }
                            cameraProvider.unbindAll()
                            val camera = cameraProvider.bindToLifecycle(lifecycleOwner, selector, preview, analysis)
                            // Losing the camera after opening (e.g. to a video call)
                            // arrives through this LiveData, not as an exception.
                            camera.cameraInfo.cameraState.observe(lifecycleOwner) { cameraState ->
                                cameraState.error?.let { error ->
                                    classifyCameraStateError(error.code)?.let(currentOnStateFailure)
                                }
                            }
                        } catch (t: Throwable) {
                            currentOnFailure(t)
                        }
                    },
                    ContextCompat.getMainExecutor(ctx),
                )
                previewView
            },
        )
        Column(
            modifier = Modifier
                .fillMaxSize()
                .safeDrawingPadding()
                .padding(24.dp),
            verticalArrangement = Arrangement.Bottom,
        ) {
            Text(
                text = "Point the camera at the QR code shown in the admin panel.",
                color = Color.White,
                style = MaterialTheme.typography.bodyLarge,
            )
        }
        PairingExits(
            onManualSetupRequested = onManualSetupRequested,
            onLoginRequested = onLoginRequested,
            overVideo = true,
        )
    }
}

/**
 * The two exits from QR pairing, in the top corners. White over the camera
 * video, default color elsewhere so they stay visible on light backgrounds.
 */
@Composable
private fun BoxScope.PairingExits(
    onManualSetupRequested: (() -> Unit)?,
    onLoginRequested: (() -> Unit)?,
    overVideo: Boolean,
) {
    val color = if (overVideo) Color.White else Color.Unspecified
    if (onManualSetupRequested != null) {
        TextButton(
            onClick = onManualSetupRequested,
            modifier = Modifier.align(Alignment.TopEnd).safeDrawingPadding().padding(8.dp),
        ) {
            Text(text = LABEL_SET_UP_MANUALLY, color = color)
        }
    }
    if (onLoginRequested != null) {
        TextButton(
            onClick = onLoginRequested,
            modifier = Modifier.align(Alignment.TopStart).safeDrawingPadding().padding(8.dp),
        ) {
            Text(text = LABEL_ALREADY_HAVE_ACCESS, color = color)
        }
    }
}

/**
 * Decodes a single CameraX [ImageProxy] frame (YUV_420_888, luma plane
 * only) as a QR code. Returns `null` when no QR code is found, which is normal
 * for most frames.
 */
private fun decodeQr(imageProxy: ImageProxy): String? {
    val buffer = imageProxy.planes[0].buffer
    val bytes = ByteArray(buffer.remaining())
    buffer.get(bytes)
    val source = PlanarYUVLuminanceSource(
        bytes,
        imageProxy.width,
        imageProxy.height,
        0,
        0,
        imageProxy.width,
        imageProxy.height,
        false,
    )
    val bitmap = BinaryBitmap(HybridBinarizer(source))
    return try {
        QRCodeReader().decode(bitmap).text
    } catch (e: NotFoundException) {
        null
    } catch (e: com.google.zxing.ChecksumException) {
        null
    } catch (e: com.google.zxing.FormatException) {
        null
    }
}

/**
 * User-facing text for a [CameraFailure]. [action] is the label of the button
 * that addresses the cause, or `null` when none can (then only the exits remain).
 */
internal data class FailureText(
    val title: String,
    val explanation: String,
    val action: String?,
    val technicalDetail: String? = null,
)

/**
 * Each cause gets its own text, always stating the next step.
 */
internal fun CameraFailure.text(): FailureText = when (this) {
    CameraFailure.PermissionDenied -> FailureText(
        title = "No camera access",
        explanation = "The app needs the camera only to read the pairing QR code. Tap " +
            "\"Allow camera access\" and confirm in the system dialog. If you'd rather not grant " +
            "access, set up the server manually and sign in with username and password.",
        action = "Allow camera access",
    )

    CameraFailure.PermissionBlocked -> FailureText(
        title = "Camera access blocked",
        explanation = "The permission was permanently denied, so the app can no longer ask. " +
            "Open this app's settings, go to Permissions → Camera and choose \"Allow\". " +
            "Or continue without the camera: set up the server manually and sign in with username and password.",
        action = "Open app settings",
    )

    CameraFailure.NoCamera -> FailureText(
        title = "This device has no camera",
        explanation = "Without a camera the panel's QR code can't be read — and there is nothing to " +
            "retry. The way forward is to set the server address manually and sign in with " +
            "username and password.",
        action = null,
    )

    CameraFailure.CameraInUse -> FailureText(
        title = "The camera is in use",
        explanation = "Another app is using the camera — an ongoing video call, for example, " +
            "including one in this very app. End the other use and tap \"Try again\". " +
            "If you'd rather not wait, set up the server manually and sign in with username and password.",
        action = "Try again",
    )

    CameraFailure.CameraBlockedBySystem -> FailureText(
        title = "The camera is disabled by the system",
        explanation = "A device policy or \"Do not disturb\" mode is blocking the " +
            "camera. Allow it in Android settings and tap \"Try again\" — or " +
            "set up the server manually and sign in with username and password.",
        action = "Try again",
    )

    is CameraFailure.UnexpectedFailure -> FailureText(
        title = "Could not open the camera",
        explanation = "The camera failed to start for a reason the app does not recognize. Tap " +
            "\"Try again\"; if it keeps failing, copy the detail below into your problem " +
            "report. Meanwhile, you can set up the server manually and sign in with " +
            "username and password.",
        action = "Try again",
        technicalDetail = detail,
    )
}

/**
 * The degraded state: explains the cause, offers its action when there is one,
 * and always shows both exits (manual setup and login).
 */
@Composable
internal fun CameraUnavailableContent(
    modifier: Modifier = Modifier,
    failure: CameraFailure,
    onRequestPermission: () -> Unit,
    onOpenSettings: () -> Unit,
    onRetry: () -> Unit,
    onManualSetupRequested: (() -> Unit)?,
    onLoginRequested: (() -> Unit)?,
) {
    val text = failure.text()
    Box(
        modifier = modifier
            .fillMaxSize()
            .safeDrawingPadding()
            .verticalScroll(rememberScrollState())
            .padding(24.dp),
        contentAlignment = Alignment.Center,
    ) {
        Card {
            Column(
                modifier = Modifier.padding(24.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Text(text = text.title, style = MaterialTheme.typography.titleLarge)
                Text(text = text.explanation, style = MaterialTheme.typography.bodyMedium)
                text.technicalDetail?.let { detail ->
                    Text(
                        text = detail,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                text.action?.let { label ->
                    Button(
                        onClick = when (failure) {
                            CameraFailure.PermissionDenied -> onRequestPermission
                            CameraFailure.PermissionBlocked -> onOpenSettings
                            else -> onRetry
                        },
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        Text(text = label, textAlign = TextAlign.Center)
                    }
                }
                if (onManualSetupRequested != null) {
                    TextButton(onClick = onManualSetupRequested, modifier = Modifier.fillMaxWidth()) {
                        Text(text = LABEL_SET_UP_MANUALLY)
                    }
                }
                if (onLoginRequested != null) {
                    TextButton(onClick = onLoginRequested, modifier = Modifier.fillMaxWidth()) {
                        Text(text = LABEL_ALREADY_HAVE_ACCESS)
                    }
                }
            }
        }
    }
}
