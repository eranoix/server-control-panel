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

/** Label of the shortcut to [ServerSetupScreen] — the same one throughout the screen. */
internal const val ROTULO_CONFIGURAR_MANUALMENTE = "Set up manually"

/** Label of the shortcut to [LoginScreen] (passkey, or username and password). */
internal const val ROTULO_JA_TENHO_ACESSO = "I already have access"

/**
 * Scans the panel's pairing QR with CameraX on the preview/frame pipeline and
 * a pure-JVM zxing decoder — no ML Kit, no Play Services (see
 * `gradle/libs.versions.toml`). Every decoded frame goes through
 * [PairingRepository.parsePairingPayload] before anything else; frames that do
 * not look like a real pairing envelope (odd content, or an envelope version
 * this build does not understand) are ignored silently, so the scanner keeps
 * looking instead of reporting an error over, say, a QR code that is not even
 * from this app. [onPairingScanned] fires at most once per instance of the
 * screen.
 *
 * **The camera may be unavailable, and that must not take the app down.**
 * Every failure path — permission denied, permission denied permanently, a
 * device with no camera, the camera taken by another app (this app has video
 * calling), the camera disabled by policy, an unexpected provider failure —
 * becomes an [EstadoDoScanner.Degradado] that explains the cause and offers
 * the way out: [onManualSetupRequested] (configure the server by hand) and
 * [onLoginRequested] (sign in with username and password). See
 * [ChecagemDeCameraX] for the crash this replaces.
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
        ambiente = remember { AmbienteDaCamera() },
    )
}

/**
 * [PairingScanScreen]'s body with [AmbienteDaCamera] exposed, which is how the
 * JVM tests reproduce each cause of failure with no hardware at all.
 */
@Composable
internal fun PairingScanContent(
    modifier: Modifier = Modifier,
    onPairingScanned: (PairingPayload) -> Unit,
    onManualSetupRequested: (() -> Unit)?,
    onLoginRequested: (() -> Unit)?,
    ambiente: AmbienteDaCamera,
) {
    val context = LocalContext.current
    var estado by remember { mutableStateOf<EstadoDoScanner>(EstadoDoScanner.Verificando) }
    // Each increment re-runs the check: it is both "try again" and the return
    // from the system Settings.
    var tentativa by remember { mutableStateOf(0) }
    // rememberSaveable so the permission request is NOT repeated on every
    // recomposition or rotation — asking in a loop is what the system punishes
    // by swallowing the dialog, and what leaves the user with no idea why
    // nothing is happening.
    var jaPediPermissao by rememberSaveable { mutableStateOf(false) }

    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { concedida ->
        if (concedida) {
            tentativa++
        } else {
            estado = EstadoDoScanner.Degradado(
                falhaDePermissao(ambiente.podePedirPermissaoNovamente(context)),
            )
        }
    }

    LaunchedEffect(tentativa) {
        estado = EstadoDoScanner.Verificando
        if (!ambiente.temPermissao(context)) {
            if (jaPediPermissao) {
                // We have already asked once in this instance of the screen
                // and still have no permission: do not ask again unprompted.
                estado = EstadoDoScanner.Degradado(
                    falhaDePermissao(ambiente.podePedirPermissaoNovamente(context)),
                )
            } else {
                jaPediPermissao = true
                estado = EstadoDoScanner.PedindoPermissao
                permissionLauncher.launch(Manifest.permission.CAMERA)
            }
            return@LaunchedEffect
        }
        estado = when (val prontidao = ambiente.checagem.conferir(context)) {
            is ProntidaoDaCamera.Pronta -> EstadoDoScanner.Escaneando(prontidao.usarCameraFrontal)
            is ProntidaoDaCamera.Indisponivel -> EstadoDoScanner.Degradado(prontidao.falha)
        }
    }

    // Coming back from Settings with the permission granted has to fix the
    // screen on its own — forcing the operator to leave and come back would be
    // leaving the job half done. It only re-checks when the permission
    // actually changed, so there is no loop.
    val estadoAtual by rememberUpdatedState(estado)
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        val observador = LifecycleEventObserver { _, evento ->
            val aguardandoPermissao = (estadoAtual as? EstadoDoScanner.Degradado)?.falha.let {
                it is FalhaDeCamera.PermissaoNegada || it is FalhaDeCamera.PermissaoBloqueada
            }
            if (evento == Lifecycle.Event.ON_RESUME && aguardandoPermissao && ambiente.temPermissao(context)) {
                tentativa++
            }
        }
        lifecycleOwner.lifecycle.addObserver(observador)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observador) }
    }

    when (val atual = estado) {
        // A spinner on its own is a dead end too: if the system's permission
        // dialog is dismissed from outside, the operator is left staring at a
        // screen with nothing to tap. The exits stay up in EVERY state.
        EstadoDoScanner.Verificando,
        EstadoDoScanner.PedindoPermissao,
        -> Box(modifier = modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator()
            SaidasDoPareamento(
                onManualSetupRequested = onManualSetupRequested,
                onLoginRequested = onLoginRequested,
                sobreVideo = false,
            )
        }

        is EstadoDoScanner.Escaneando -> QrCameraPreview(
            modifier = modifier,
            usarCameraFrontal = atual.usarCameraFrontal,
            onPairingScanned = onPairingScanned,
            onManualSetupRequested = onManualSetupRequested,
            onLoginRequested = onLoginRequested,
            onFalha = { erro ->
                estado = EstadoDoScanner.Degradado(
                    classificarFalhaDeCamera(erro, contarCamerasDoAparelho(context)),
                )
            },
            onFalhaDeEstado = { falha -> estado = EstadoDoScanner.Degradado(falha) },
        )

        is EstadoDoScanner.Degradado -> CameraIndisponivelContent(
            modifier = modifier,
            falha = atual.falha,
            onPedirPermissao = { permissionLauncher.launch(Manifest.permission.CAMERA) },
            onAbrirConfiguracoes = { abrirConfiguracoesDoApp(context) },
            onTentarNovamente = { tentativa++ },
            onManualSetupRequested = onManualSetupRequested,
            onLoginRequested = onLoginRequested,
        )
    }
}

@Composable
private fun QrCameraPreview(
    modifier: Modifier,
    usarCameraFrontal: Boolean,
    onPairingScanned: (PairingPayload) -> Unit,
    onManualSetupRequested: (() -> Unit)?,
    onLoginRequested: (() -> Unit)?,
    onFalha: (Throwable) -> Unit,
    onFalhaDeEstado: (FalhaDeCamera) -> Unit,
) {
    val lifecycleOwner = LocalLifecycleOwner.current
    val pairingRepository = remember { PairingRepository() }
    var alreadyScanned by remember { mutableStateOf(false) }
    val currentOnPairingScanned by rememberUpdatedState(onPairingScanned)
    val currentOnFalha by rememberUpdatedState(onFalha)
    val currentOnFalhaDeEstado by rememberUpdatedState(onFalhaDeEstado)
    // Held here (rather than created inside the factory) so the
    // DisposableEffect below can shut it down: the previous version of this
    // screen leaked a thread per visit to the scanner.
    val analysisExecutor = remember { Executors.newSingleThreadExecutor() }
    val seletor = remember(usarCameraFrontal) {
        if (usarCameraFrontal) CameraSelector.DEFAULT_FRONT_CAMERA else CameraSelector.DEFAULT_BACK_CAMERA
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
                        // This block runs on a Runnable of the main executor:
                        // any exception escaping from here has NOBODY to catch
                        // it and kills the process. That is why the try covers
                        // the `get()` as well — that was exactly where the app
                        // was closing.
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
                            val camera = cameraProvider.bindToLifecycle(lifecycleOwner, seletor, preview, analysis)
                            // The camera can fall over AFTER it has opened —
                            // this app's own video call taking it mid-scan is
                            // the likeliest case. That does not arrive as an
                            // exception: CameraX reports it through this
                            // LiveData.
                            camera.cameraInfo.cameraState.observe(lifecycleOwner) { estadoDaCamera ->
                                estadoDaCamera.error?.let { erro ->
                                    classificarErroDeEstadoDaCamera(erro.code)?.let(currentOnFalhaDeEstado)
                                }
                            }
                        } catch (t: Throwable) {
                            currentOnFalha(t)
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
        SaidasDoPareamento(
            onManualSetupRequested = onManualSetupRequested,
            onLoginRequested = onLoginRequested,
            sobreVideo = true,
        )
    }
}

/**
 * The two shortcuts that take the operator out of QR pairing, in the top
 * corners. They live with the scanner screen rather than with its caller,
 * because only the screen knows what they are drawn on top of: over the video
 * the text has to be white; over the light background of the waiting state, it
 * must not — that is how a white button on a light card once became invisible
 * text.
 */
@Composable
private fun BoxScope.SaidasDoPareamento(
    onManualSetupRequested: (() -> Unit)?,
    onLoginRequested: (() -> Unit)?,
    sobreVideo: Boolean,
) {
    val cor = if (sobreVideo) Color.White else Color.Unspecified
    if (onManualSetupRequested != null) {
        TextButton(
            onClick = onManualSetupRequested,
            modifier = Modifier.align(Alignment.TopEnd).safeDrawingPadding().padding(8.dp),
        ) {
            Text(text = ROTULO_CONFIGURAR_MANUALMENTE, color = cor)
        }
    }
    if (onLoginRequested != null) {
        TextButton(
            onClick = onLoginRequested,
            modifier = Modifier.align(Alignment.TopStart).safeDrawingPadding().padding(8.dp),
        ) {
            Text(text = ROTULO_JA_TENHO_ACESSO, color = cor)
        }
    }
}

/**
 * Decodes a single CameraX [ImageProxy] frame (YUV_420_888, luma plane
 * only) as a QR code. Returns `null` for anything that isn't a QR code in
 * frame — that is the overwhelming majority of frames while the camera is
 * still searching, not an error condition.
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
 * The text of a [FalhaDeCamera]: what happened and, above all, what to do.
 * [acao] is the label of the button that attacks the cause — `null` when no
 * action would resolve it (a device with no camera), in which case only the
 * exits remain.
 */
internal data class TextoDaFalha(
    val titulo: String,
    val explicacao: String,
    val acao: String?,
    val detalheTecnico: String? = null,
)

/**
 * Each cause with its own text, in the imperative: stating only what happened
 * leaves the operator stuck; what they need is the next step.
 */
internal fun FalhaDeCamera.texto(): TextoDaFalha = when (this) {
    FalhaDeCamera.PermissaoNegada -> TextoDaFalha(
        titulo = "No camera access",
        explicacao = "The app needs the camera only to read the pairing QR code. Tap " +
            "\"Allow camera access\" and confirm in the system dialog. If you'd rather not grant " +
            "access, set up the server manually and sign in with username and password.",
        acao = "Allow camera access",
    )

    FalhaDeCamera.PermissaoBloqueada -> TextoDaFalha(
        titulo = "Camera access blocked",
        explicacao = "The permission was permanently denied, so the app can no longer ask. " +
            "Open this app's settings, go to Permissions → Camera and choose \"Allow\". " +
            "Or continue without the camera: set up the server manually and sign in with username and password.",
        acao = "Open app settings",
    )

    FalhaDeCamera.SemCamera -> TextoDaFalha(
        titulo = "This device has no camera",
        explicacao = "Without a camera the panel's QR code can't be read — and there is nothing to " +
            "retry. The way forward is to set the server address manually and sign in with " +
            "username and password.",
        acao = null,
    )

    FalhaDeCamera.CameraOcupada -> TextoDaFalha(
        titulo = "The camera is in use",
        explicacao = "Another app is using the camera — an ongoing video call, for example, " +
            "including one in this very app. End the other use and tap \"Try again\". " +
            "If you'd rather not wait, set up the server manually and sign in with username and password.",
        acao = "Try again",
    )

    FalhaDeCamera.CameraBloqueadaPeloSistema -> TextoDaFalha(
        titulo = "The camera is disabled by the system",
        explicacao = "A device policy or \"Do not disturb\" mode is blocking the " +
            "camera. Allow it in Android settings and tap \"Try again\" — or " +
            "set up the server manually and sign in with username and password.",
        acao = "Try again",
    )

    is FalhaDeCamera.FalhaInesperada -> TextoDaFalha(
        titulo = "Could not open the camera",
        explicacao = "The camera failed to start for a reason the app does not recognize. Tap " +
            "\"Try again\"; if it keeps failing, copy the detail below into your problem " +
            "report. Meanwhile, you can set up the server manually and sign in with " +
            "username and password.",
        acao = "Try again",
        detalheTecnico = detalhe,
    )
}

/**
 * The degraded state: it explains the cause, offers the action that attacks it
 * (when there is one) and ALWAYS the two exits from QR pairing — configure the
 * server by hand, and go to the login screen. This is what replaced the app
 * closing.
 */
@Composable
internal fun CameraIndisponivelContent(
    modifier: Modifier = Modifier,
    falha: FalhaDeCamera,
    onPedirPermissao: () -> Unit,
    onAbrirConfiguracoes: () -> Unit,
    onTentarNovamente: () -> Unit,
    onManualSetupRequested: (() -> Unit)?,
    onLoginRequested: (() -> Unit)?,
) {
    val texto = falha.texto()
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
                Text(text = texto.titulo, style = MaterialTheme.typography.titleLarge)
                Text(text = texto.explicacao, style = MaterialTheme.typography.bodyMedium)
                texto.detalheTecnico?.let { detalhe ->
                    Text(
                        text = detalhe,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                texto.acao?.let { rotulo ->
                    Button(
                        onClick = when (falha) {
                            FalhaDeCamera.PermissaoNegada -> onPedirPermissao
                            FalhaDeCamera.PermissaoBloqueada -> onAbrirConfiguracoes
                            else -> onTentarNovamente
                        },
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        Text(text = rotulo, textAlign = TextAlign.Center)
                    }
                }
                if (onManualSetupRequested != null) {
                    TextButton(onClick = onManualSetupRequested, modifier = Modifier.fillMaxWidth()) {
                        Text(text = ROTULO_CONFIGURAR_MANUALMENTE)
                    }
                }
                if (onLoginRequested != null) {
                    TextButton(onClick = onLoginRequested, modifier = Modifier.fillMaxWidth()) {
                        Text(text = ROTULO_JA_TENHO_ACESSO)
                    }
                }
            }
        }
    }
}
