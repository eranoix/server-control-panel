package com.vpsmanager.app.nav

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.IconButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.vpsmanager.data.sdui.SduiCatalogPort
import com.vpsmanager.data.sdui.SduiCatalogRepository
import com.vpsmanager.data.sdui.SduiSectionsResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Test tag for a parent page's grid. */
internal const val TAG_GRADE_DA_MAE = "grade-da-mae"

/**
 * Drives a parent page's grid.
 *
 * ## Why it needs the server's catalogue
 *
 * The children declared in [filhasDe] are the intent; the catalogue (`GET
 * /screens`) is what EXISTS for this user on this server version. A
 * section the server does not offer — by permission or because its version
 * is older — must not appear in the grid and lead to an empty screen.
 *
 * And the converse too: a section the server offers and this app has never
 * heard of shows up all the same, under the parent the id's prefix points
 * at ([maeDaSecao]). It is the whole SDUI promise — a new screen with no
 * new version — and it would die if the grid were a fixed list.
 */
internal class MaeViewModel(
    private val mae: PaginaMae,
    private val catalogo: SduiCatalogPort = SduiCatalogRepository(),
) : ViewModel() {

    private val _filhas = MutableStateFlow<List<PaginaFilha>?>(null)

    /** null while loading; a (possibly empty) list afterwards. */
    val filhas: StateFlow<List<PaginaFilha>?> = _filhas.asStateFlow()

    /**
     * ALL the panel's screens, from every parent — what the search sweeps.
     *
     * The search is global on purpose: whoever types "secrets" remembers the
     * screen's NAME, not that it belongs to Security. A search that only
     * looked at the open parent would fail exactly for whoever needs it most
     * — and that was the one thing the thirty-block grid did better than the
     * parents.
     */
    private val _todas = MutableStateFlow<List<PaginaFilha>>(emptyList())
    val todas: StateFlow<List<PaginaFilha>> = _todas.asStateFlow()

    init {
        carregar()
    }

    fun carregar() {
        viewModelScope.launch {
            val declaradas = filhasDe(mae)
            val disponiveis = when (val r = catalogo.sections()) {
                is SduiSectionsResult.Success -> r.sections
                // An unavailable catalogue does NOT empty the grid: this parent's
                // native screens stay reachable, which is better than a blank page
                // because of one call that failed.
                is SduiSectionsResult.Error -> emptyList()
            }
            val idsDisponiveis = disponiveis.map { it.id }.toSet()

            val existentes = declaradas.filter { filha ->
                when (val d = filha.destino) {
                    is DestinoDaFilha.Nativa -> true
                    is DestinoDaFilha.Sdui -> d.sectionId in idsDisponiveis
                }
            }
            val jaListadas = declaradas.mapNotNull {
                (it.destino as? DestinoDaFilha.Sdui)?.sectionId
            }.toSet()

            val desconhecidas = disponiveis
                .filter { it.id !in jaListadas && it.id !in FILHAS_SDUI_OCULTAS }
                .filter { maeDaSecao(it.id) == mae }
                .map {
                    PaginaFilha(
                        titulo = it.label,
                        icone = ICONE_DE_SECAO_DESCONHECIDA,
                        destino = DestinoDaFilha.Sdui(it.id),
                    )
                }

            _filhas.value = existentes + desconhecidas

            // The complete map: the declared children of EVERY parent that the
            // server actually offers, plus what it offers and this app does not
            // know by name.
            val declaradasEmTodas = PaginaMae.entries.flatMap { filhasDe(it) }
            val idsDeclarados = declaradasEmTodas.mapNotNull {
                (it.destino as? DestinoDaFilha.Sdui)?.sectionId
            }.toSet()
            _todas.value = declaradasEmTodas.filter { filha ->
                when (val d = filha.destino) {
                    is DestinoDaFilha.Nativa -> true
                    is DestinoDaFilha.Sdui -> d.sectionId in idsDisponiveis
                }
            } + disponiveis
                .filter { it.id !in idsDeclarados && it.id !in FILHAS_SDUI_OCULTAS }
                .map {
                    PaginaFilha(
                        titulo = it.label,
                        icone = ICONE_DE_SECAO_DESCONHECIDA,
                        destino = DestinoDaFilha.Sdui(it.id),
                    )
                }
        }
    }
}

/**
 * A parent page's grid of icons.
 *
 * It is what the owner asked for: tapping the parent shows the children's
 * icons, and tapping a child goes in. The grid is adaptive (a minimum of
 * 104 dp per block) so it fits four columns on a wide phone and three on a
 * narrow one, without any label having to be abbreviated.
 */
@Composable
internal fun TelaDaMae(
    mae: PaginaMae,
    aoAbrirNativa: (String) -> Unit,
    aoAbrirSdui: (String) -> Unit,
    modifier: Modifier = Modifier,
    vm: MaeViewModel = viewModel(key = "mae-${mae.id}") { MaeViewModel(mae) },
) {
    val filhas by vm.filhas.collectAsStateWithLifecycle()
    val todas by vm.todas.collectAsStateWithLifecycle()
    var busca by remember { mutableStateOf("") }

    Column(modifier = modifier.fillMaxSize().testTag(TAG_GRADE_DA_MAE)) {
        OutlinedTextField(
            value = busca,
            onValueChange = { busca = it },
            singleLine = true,
            label = { Text(BUSCAR_TELA_LABEL) },
            leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
            trailingIcon = {
                if (busca.isNotBlank()) {
                    IconButton(onClick = { busca = "" }) {
                        Icon(Icons.Filled.Close, contentDescription = "Clear search")
                    }
                }
            },
            modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
        )

        // While searching, the grid stops being "this parent's children" and
        // becomes "the screens that match" — from any parent. It is what replaces
        // the search of the thirty-block grid that went away.
        val buscando = busca.isNotBlank()
        val resultado = if (buscando) filtrarTelas(todas, busca) else filhas

        Box(modifier = Modifier.fillMaxSize()) {
        when (val lista = resultado) {
            null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }

            else -> if (lista.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Text(
                        text = if (buscando) {
                            "No screen matches \"$busca\"."
                        } else {
                            "Nothing in ${mae.titulo} for this account."
                        },
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(32.dp),
                        textAlign = TextAlign.Center,
                    )
                }
            } else {
                LazyVerticalGrid(
                    columns = GridCells.Adaptive(minSize = 104.dp),
                    contentPadding = PaddingValues(12.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                    modifier = Modifier.fillMaxSize(),
                ) {
                    items(lista, key = { it.titulo + it.destino }) { filha ->
                        BlocoDaFilha(
                            filha = filha,
                            cor = corDaMae(mae),
                            onClick = {
                                when (val d = filha.destino) {
                                    is DestinoDaFilha.Nativa -> aoAbrirNativa(d.rota)
                                    is DestinoDaFilha.Sdui -> aoAbrirSdui(d.sectionId)
                                }
                            },
                        )
                    }
                }
            }
        }
        }
    }
}

/**
 * Filters screens by title and by section id.
 *
 * Both, because each is how a different person remembers the same screen:
 * by the name that shows ("Vault") or by the id they saw in a log
 * (`security.secrets`). Ignoring the id would make the search fail exactly
 * for whoever arrived from an error message — the case where it is worth
 * the most.
 */
internal fun filtrarTelas(telas: List<PaginaFilha>, busca: String): List<PaginaFilha> {
    val termo = busca.trim().lowercase()
    if (termo.isEmpty()) return telas
    return telas.filter { filha ->
        filha.titulo.lowercase().contains(termo) ||
            (filha.destino as? DestinoDaFilha.Sdui)?.sectionId?.lowercase()?.contains(termo) == true
    }
}

/** Label of the search field. The UI and the test read it from here. */
internal const val BUSCAR_TELA_LABEL = "Search screens"

/**
 * The family's colour, so the block is not just another grey square.
 *
 * The same rule Administration's grid already followed: the colour is
 * **taxonomic**, it says which family the screen belongs to — and that is
 * why none of them is the green or the red of STATE. A red block because
 * it belongs to Security, next to a red alert because the disk filled up,
 * would destroy the only language the app has for saying urgency.
 */
@Composable
private fun corDaMae(mae: PaginaMae): Color = when (mae) {
    PaginaMae.Sistema -> Color(0xFF4F8FD9)
    PaginaMae.Docker -> Color(0xFF3BA9B4)
    PaginaMae.Seguranca -> Color(0xFFC98A2E)
    PaginaMae.Operacoes -> Color(0xFF8B72D0)
    PaginaMae.Apps -> Color(0xFF5E9E76)
    PaginaMae.Dev -> Color(0xFFB0736B)
    PaginaMae.Inicio, PaginaMae.Configuracoes -> MaterialTheme.colorScheme.primary
}

@Composable
private fun BlocoDaFilha(
    filha: PaginaFilha,
    cor: Color,
    onClick: () -> Unit,
) {
    Surface(
        color = MaterialTheme.colorScheme.surfaceContainer,
        shape = RoundedCornerShape(14.dp),
        modifier = Modifier
            .fillMaxWidth()
            .height(104.dp)
            .clip(RoundedCornerShape(14.dp))
            .clickable(onClick = onClick)
            // A single target for the screen reader: without this it would read
            // the icon and the label as two separate nodes.
            .semantics(mergeDescendants = true) { contentDescription = filha.titulo },
    ) {
        Column(
            modifier = Modifier.fillMaxSize().padding(8.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Box(
                modifier = Modifier
                    .size(44.dp)
                    .clip(CircleShape)
                    .background(cor.copy(alpha = 0.18f)),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    imageVector = filha.icone,
                    contentDescription = null,
                    tint = cor,
                    modifier = Modifier.size(22.dp),
                )
            }
            Text(
                text = filha.titulo,
                style = MaterialTheme.typography.labelMedium,
                textAlign = TextAlign.Center,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 8.dp),
            )
        }
    }
}
