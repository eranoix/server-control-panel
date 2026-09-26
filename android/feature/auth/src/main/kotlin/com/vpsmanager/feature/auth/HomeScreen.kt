package com.vpsmanager.feature.auth

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.TextButton
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalContext
import com.vpsmanager.feature.auth.painel.BlocoDoPainel
import com.vpsmanager.feature.auth.painel.BlocosEscolhidos
import com.vpsmanager.feature.auth.painel.PedidoDeTopo
import com.vpsmanager.feature.auth.painel.CatalogoDeBlocos
import com.vpsmanager.feature.auth.painel.GradeDeBlocos
import com.vpsmanager.feature.auth.painel.blocosVisiveis
import com.vpsmanager.feature.auth.painel.catalogoDeBlocos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.vpsmanager.data.dashboard.DashboardSnapshot
import com.vpsmanager.data.dashboard.DashboardTarget
import com.vpsmanager.data.widget.ResumoGuardado
import com.vpsmanager.data.widget.resumoDe
import com.vpsmanager.designsystem.vpsmStatusColors
import kotlinx.coroutines.delay
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import kotlin.math.abs

/**
 * Home: the operations dashboard.
 *
 * ## The order of the cards, and why
 * 1. **Needs attention** — the question of someone opening the app in a hurry
 *    is "did something break?". It only exists when there is something; in
 *    silence it disappears and the rest moves up.
 * 2. **Server health** — the subsystems AGGREGATED ("9 · all ok"), with the
 *    healthy ones behind a button. Nine green lines teach the eye to ignore
 *    the area, and it is the future red line that pays the bill.
 * 3. **Right now** — the queue, the last deploy, the next scheduled job: "can
 *    I touch this right now?", which is the next question.
 * 4. **Resources** — CPU/memory/swap/disk. Fourth, not first: saturation is a
 *    debugging metric, not an alerting one; it explains *why*, after something
 *    has already fired.
 * 5. **Quick actions** — the "and then?" of each glance.
 * 6. **Session** — identity last; nobody opens a dashboard in a hurry to find
 *    out their own email address.
 *
 * ## Navigation
 * The screen knows no routes: it emits a [DashboardTarget], and whoever hosts
 * it translates that into a route ([onOpenSection] for the sections the server
 * describes, [onOpenTerminal] for the shell's Terminal destination). That way
 * navigation stays in one place and this screen stays testable without a
 * `NavHost`.
 */
@Composable
fun HomeScreen(
    /**
     * Opens the device's security screen.
     *
     * It lives here, on the Session card, and not in the drawer: it is a thing
     * of THIS device — like the identity and the server already on that card —
     * and not a place of work you navigate to all day. One more destination in
     * the drawer would push "Sign out" off the screen.
     *
     * Defaults to empty so it does not break anyone already composing this
     * screen in a test.
     */
    onAbrirSeguranca: () -> Unit = {},
    onAbrirDiagnostico: () -> Unit = {},
    modifier: Modifier = Modifier,
    viewModel: HomeViewModel = viewModel(),
    onOpenSection: (String) -> Unit = {},
    onOpenTerminal: () -> Unit = {},
    autoRefreshMillis: Long = HOME_AUTO_REFRESH_MILLIS,
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    // The widget summary's writer is wired up here because the screen has a
    // context and the ViewModel should not gain one just to store four
    // strings.
    val contexto = LocalContext.current
    LaunchedEffect(contexto) {
        viewModel.publicarResumoCom { snapshot ->
            ResumoGuardado.gravar(contexto, resumoDe(snapshot))
        }
    }

    AutoRefreshWhileResumed(intervalMillis = autoRefreshMillis, onTick = viewModel::autoRefresh)

    HomeDashboard(
        state = state,
        onRetry = viewModel::load,
        onRefresh = viewModel::refresh,
        onTarget = { target ->
            val sectionId = target.sectionId
            if (sectionId != null) onOpenSection(sectionId) else onOpenTerminal()
        },
        modifier = modifier,
        onAbrirSeguranca = onAbrirSeguranca,
        onAbrirDiagnostico = onAbrirDiagnostico,
    )
}

/**
 * Fires [onTick] every [intervalMillis] WHILE the screen is visible.
 *
 * The loop is tied to the lifecycle for two reasons. The first is battery and
 * radio: a backgrounded dashboard has nobody to show a new number to. The
 * second is honesty — when the app comes back out of a pocket, the first tick
 * arrives promptly and the timestamp keeps up; without it, the screen would
 * reappear with a half-hour-old number looking current.
 *
 * An [intervalMillis] of `<= 0` turns the loop off. That is what the tests
 * use: an infinite `delay` inside the composition never lets Compose go idle,
 * and the test's `waitForIdle` would wait forever.
 */
@Composable
private fun AutoRefreshWhileResumed(intervalMillis: Long, onTick: () -> Unit) {
    if (intervalMillis <= 0) return
    val tick by rememberUpdatedState(onTick)
    val lifecycleOwner = LocalLifecycleOwner.current
    var resumed by remember { mutableStateOf(false) }

    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_RESUME -> resumed = true
                Lifecycle.Event.ON_PAUSE -> resumed = false
                else -> Unit
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    LaunchedEffect(resumed, intervalMillis) {
        if (!resumed) return@LaunchedEffect
        while (true) {
            delay(intervalMillis)
            tick()
        }
    }
}

/**
 * The content, with no ViewModel — every dependency is a parameter, so a test
 * can compose any state directly.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun HomeDashboard(
    state: HomeUiState,
    onRetry: () -> Unit,
    onRefresh: () -> Unit,
    onTarget: (DashboardTarget) -> Unit,
    modifier: Modifier = Modifier,
    onAbrirSeguranca: () -> Unit = {},
    onAbrirDiagnostico: () -> Unit = {},
) {
    PullToRefreshBox(
        isRefreshing = (state as? HomeUiState.Success)?.refreshing == true,
        onRefresh = onRefresh,
        modifier = modifier.fillMaxSize(),
    ) {
        when (state) {
            is HomeUiState.Loading -> HomeLoading()
            is HomeUiState.Error -> HomeError(message = state.message, onRetry = onRetry)
            is HomeUiState.Success -> DashboardContent(
                snapshot = state.snapshot,
                staleError = state.staleError,
                onRetry = onRetry,
                onTarget = onTarget,
                onAbrirSeguranca = onAbrirSeguranca,
                onAbrirDiagnostico = onAbrirDiagnostico,
            )
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun DashboardContent(
    snapshot: DashboardSnapshot,
    staleError: String?,
    onRetry: () -> Unit,
    onTarget: (DashboardTarget) -> Unit,
    onAbrirSeguranca: () -> Unit = {},
    onAbrirDiagnostico: () -> Unit = {},
) {
    val atencao = snapshot.attention
    val context = LocalContext.current

    // "GO HOME" MEANS THE TOP OF THE PAGE.
    //
    // The drawer navigates with `restoreState = true`, which restores the saved
    // scroll position — so tapping Home handed back the same page halfway down,
    // with the header cut off under the bar. To whoever tapped, that is "the
    // button did not work". See PedidoDeTopo.
    val rolagem = rememberLazyListState()
    val pedidoDeTopo by PedidoDeTopo.contador.collectAsStateWithLifecycle()
    LaunchedEffect(pedidoDeTopo) {
        if (pedidoDeTopo > 0) rolagem.animateScrollToItem(0)
    }
    val catalogo = remember(snapshot) { catalogoDeBlocos(snapshot) }
    var escolhidos by remember { mutableStateOf(BlocosEscolhidos.ler(context)) }
    var emEdicao by rememberSaveable { mutableStateOf(false) }
    var catalogoAberto by rememberSaveable { mutableStateOf(false) }
    val visiveis = remember(catalogo, escolhidos) { blocosVisiveis(catalogo, escolhidos) }

    if (catalogoAberto) {
        CatalogoDeBlocos(
            catalogo = catalogo,
            escolhidos = escolhidos,
            noTeto = escolhidos.size >= BlocosEscolhidos.MAXIMO,
            aoEscolher = { bloco ->
                escolhidos = BlocosEscolhidos.acrescentar(context, bloco.id)
            },
            aoFechar = { catalogoAberto = false },
        )
    }

    LazyColumn(
        state = rolagem,
        modifier = Modifier.fillMaxSize(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item("carimbo") {
            FreshnessLine(
                fetchedAtEpochMs = snapshot.fetchedAtEpochMs,
                staleError = staleError,
                onRetry = onRetry,
            )
        }
        // THE GRID COMES BEFORE EVERYTHING, including the attention card. It
        // is the dashboard the person assembled, and the reason the model was
        // chosen in the first place: the answer to "is everything all right?"
        // in two seconds. The attention card stays right below because it
        // EXPLAINS — the grid says the disk is red, the card says why that
        // matters and where to go.
        item("painel") {
            PainelDeBlocos(
                blocos = visiveis,
                emEdicao = emEdicao,
                temEscolha = escolhidos.isNotEmpty(),
                aoAlternarEdicao = { emEdicao = !emEdicao },
                aoTocar = { onTarget(it.alvo) },
                aoRemover = { escolhidos = BlocosEscolhidos.remover(context, it.id) },
                aoPedirCatalogo = { catalogoAberto = true },
            )
        }
        // THE ATTENTION CARD STAYS. I removed it by mistake and the tests
        // caught it: it is not the banner the owner complained about — it is
        // what carries a reverted deploy or a crossed threshold to the right
        // screen. Removing "tell me when something is wrong" was not the
        // request.
        if (atencao.isNotEmpty()) {
            item("atencao") { AttentionCard(signals = atencao, onTarget = onTarget) }
        }
        // THE HEALTH CARD IS GONE (at the owner's request).
        //
        // It spent a block of height on the FIRST screen to say "9 subsystems
        // · all ok" and hide the nine behind a button — a permanent frame for
        // information that only matters when it is bad. And when it is bad,
        // the attention card above already says so.
        //
        // Per-subsystem health is still there in full, in its own section of
        // Administration. If it ever comes back here, it has to come back as
        // an EXCEPTION: showing up only when something is degraded.
        // THE RESOURCES CARD IS GONE too. The block grid shows the same
        // judged signals, from the same `gradeResources`, and keeping both
        // would be the same information in two places — which diverge the day
        // only one of them is fixed. What the card had of its own and the grid
        // does not (the sentence explaining the threshold) lives on the
        // attention card, which is where it matters: when the threshold has
        // been crossed.
        if (snapshot.resourceSignals.isEmpty()) {
            item("recursos-ausentes") { ResourcesUnavailableCard() }
        }
        item("acoes") { QuickActionsCard(onTarget = onTarget) }
        item("sessao") {
            SessionCard(
                snapshot = snapshot,
                driftText = clockDriftText(snapshot),
                onAbrirSeguranca = onAbrirSeguranca,
                onAbrirDiagnostico = onAbrirDiagnostico,
            )
        }
    }
}

/**
 * The freshness timestamp, and the warning when the last fetch failed.
 *
 * Without a time, "no new data" and "the fetch is stuck" are
 * indistinguishable — and a stale number passing for a current one is the
 * worst possible defect on an operations dashboard.
 */
@Composable
private fun FreshnessLine(fetchedAtEpochMs: Long, staleError: String?, onRetry: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "updated at ${horaDe(fetchedAtEpochMs)}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                text = "pull to refresh",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (staleError != null) {
            val warning = vpsmStatusColors.warning
            Card(
                colors = CardDefaults.cardColors(containerColor = warning.container),
                elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
            ) {
                Row(
                    modifier = Modifier.padding(horizontal = 12.dp, vertical = 10.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(
                        imageVector = Icons.Filled.Warning,
                        contentDescription = null,
                        tint = warning.accent,
                        modifier = Modifier.size(18.dp),
                    )
                    Column(modifier = Modifier.weight(1f)) {
                        Text(
                            text = "The numbers below are from ${horaDe(fetchedAtEpochMs)}",
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = FontWeight.SemiBold,
                            color = warning.content,
                        )
                        Text(
                            text = staleError,
                            style = MaterialTheme.typography.bodySmall,
                            color = warning.content,
                        )
                    }
                    Button(onClick = onRetry) { Text("Reload") }
                }
            }
        }
    }
}

/** A server too old to expose `system` — the card says so rather than inventing a zero. */
@Composable
private fun ResourcesUnavailableCard() {
    DashboardCard(title = "Resources", subtitle = "unavailable") {
        Text(
            text = "This server does not expose CPU, memory and disk in /ops/status. " +
                "Update Server Control Panel to see resources here.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun HomeLoading() {
    // A skeleton, not a blank screen: three grey cards in the same positions
    // the content will appear in, so the layout does not jump when it arrives.
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                CircularProgressIndicator(modifier = Modifier.size(18.dp))
                Text(
                    text = "Loading the dashboard…",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        // THE TITLES HERE HAVE TO BE THE CARDS THE SCREEN ACTUALLY HAS.
        // They went stale without anyone noticing: the list said "Server
        // health", "Right now" and "Resources", and ALL THREE have since left
        // the Home screen. A skeleton that promises cards which never arrive is
        // worse than no skeleton at all — it teaches the wrong screen during
        // loading and then contradicts itself.
        items(listOf("Quick actions", "Session")) { titulo ->
            Card(
                modifier = Modifier.fillMaxWidth(),
                colors = CardDefaults.cardColors(
                    containerColor = MaterialTheme.colorScheme.surfaceContainer,
                ),
                elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
            ) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text(
                        text = titulo,
                        style = MaterialTheme.typography.titleMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Text(
                        text = "…",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

/**
 * A hard error: nothing on screen to preserve.
 *
 * The message says WHAT TO DO, not just what happened — an error screen that
 * only describes leaves the person stuck.
 */
@Composable
private fun HomeError(message: String, onRetry: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Card(
            colors = CardDefaults.cardColors(
                containerColor = MaterialTheme.colorScheme.surfaceContainer,
            ),
            elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
        ) {
            Column(
                modifier = Modifier.padding(24.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Text(
                    text = "Can't reach the server",
                    style = MaterialTheme.typography.titleLarge,
                )
                Text(text = message, style = MaterialTheme.typography.bodyMedium)
                Text(
                    text = "Check the device's network and the server address in Settings. " +
                        "The dashboard comes back on its own as soon as the connection responds.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Button(onClick = onRetry) {
                    Icon(
                        imageVector = Icons.Filled.Refresh,
                        contentDescription = null,
                        modifier = Modifier.size(18.dp),
                    )
                    Text(text = "  Try again")
                }
            }
        }
    }
}

/** "07:08" in the DEVICE's time zone — it is the clock the operator is looking at. */
internal fun horaDe(epochMillis: Long): String =
    Instant.ofEpochMilli(epochMillis)
        .atZone(ZoneId.systemDefault())
        .format(DateTimeFormatter.ofPattern("HH:mm:ss"))

/**
 * How far the server's clock and the device's may diverge before it is worth
 * reporting.
 *
 * Under 60 s the difference is network latency, second-level rounding and the
 * duration of the call itself — reporting that would be noise. Above it, the
 * clock really is out of sync, and that explains half of all "but I just ran
 * that": logs stamped at another hour, a cron that looks like it never fired,
 * a token that expires early.
 */
private const val DERIVA_RELOGIO_SEGUNDOS = 60L

/** The drift sentence, or `null` when the two clocks agree. */
internal fun clockDriftText(snapshot: DashboardSnapshot): String? {
    val system = snapshot.ops.system ?: return null
    val deviceSeconds = snapshot.fetchedAtEpochMs / 1_000
    val drift = deviceSeconds - system.serverTimeEpoch
    if (abs(drift) < DERIVA_RELOGIO_SEGUNDOS) return null
    val sentido = if (drift > 0) "behind the" else "ahead of the"
    return "Server clock ${abs(drift)} s $sentido device"
}


/**
 * The grid, with the header that makes it editable.
 *
 * The edit button lives HERE and not on the shell's title bar, for a reason of
 * scope: the bar belongs to the shell and applies to every screen, and
 * "arrange" only means something on this one. An icon that changes meaning
 * depending on the screen is the beginning of a bar nobody reads.
 */
@Composable
private fun PainelDeBlocos(
    blocos: List<BlocoDoPainel>,
    emEdicao: Boolean,
    temEscolha: Boolean,
    aoAlternarEdicao: () -> Unit,
    aoTocar: (BlocoDoPainel) -> Unit,
    aoRemover: (BlocoDoPainel) -> Unit,
    aoPedirCatalogo: () -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = if (emEdicao) "Building the dashboard" else "Dashboard",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
            )
            TextButton(onClick = aoAlternarEdicao) {
                Text(text = if (emEdicao) "Done" else "Customize")
            }
        }
        if (blocos.isEmpty() && !emEdicao) {
            // THE REAL EMPTY: the person removed everything. We do not put
            // the starting blocks back on our own — that would undo what they
            // have just done. The invitation stays, and there is only one of
            // it.
            OutlinedCard(
                modifier = Modifier.fillMaxWidth().clickable(onClick = aoPedirCatalogo),
            ) {
                Column(
                    modifier = Modifier.fillMaxWidth().padding(20.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    Text(text = "+  Pick the first tile", style = MaterialTheme.typography.titleSmall)
                    Text(
                        text = if (temEscolha) {
                            "None of the chosen tiles is available right now."
                        } else {
                            "The dashboard starts empty on purpose: you choose what you look at every day."
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        textAlign = androidx.compose.ui.text.style.TextAlign.Center,
                    )
                }
            }
        } else {
            GradeDeBlocos(
                blocos = blocos,
                emEdicao = emEdicao,
                aoTocar = aoTocar,
                aoRemover = aoRemover,
                aoPedirCatalogo = aoPedirCatalogo,
            )
        }
    }
}
