package com.vpsmanager.feature.admin

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.sdui.SduiCatalogPort
import com.vpsmanager.data.sdui.SduiCatalogRepository
import com.vpsmanager.data.sdui.SduiSection
import com.vpsmanager.data.sdui.SduiSectionsResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * The section id the drawer sends when it does not want to choose a section at
 * all — "open Administration and show whatever the server offers first".
 *
 * It exists so that `AppDestination.Admin` does not have to name a section.
 * The previous version pointed at a client-side hard-coded `scheduler.jobs`,
 * which left the server's other 24 screens unreachable; swapping a SECTION
 * constant for a SENTINEL constant is what gives the choice back to the
 * server. This token never goes over the wire, and it cannot collide with a
 * real screen id (they all have the form `group.name`).
 *
 * **What it resolves to has changed.** It used to mean "the catalogue's first
 * section"; it now means "none" — and no section chosen is the LAUNCHER.
 * Always opening on the first one treated the 25 sections as if one of them
 * were the right answer, and none is: which one matters depends on what is
 * going on. See [AdminLauncher].
 */
const val ADMIN_SECTION_AUTO = "auto"

/**
 * What the section picker shows. Deliberately kept apart from [AdminUiState]:
 * the catalogue and the rendered screen fail in independent ways — the
 * catalogue can load and the chosen section fail, or the other way round — and
 * fusing them into one state would force the whole screen to disappear over an
 * error that only affects half of it.
 */
sealed interface AdminCatalogState {
    data object Loading : AdminCatalogState

    data class Error(val message: String, val retry: () -> Unit) : AdminCatalogState

    /**
     * [sections] arrives already grouped and ordered BY THE SERVER — the order
     * is not recomputed here, or app and server would disagree about the shape
     * of the list.
     *
     * A null [selectedId] means **launcher open**, which is the state
     * Administration now starts in. An empty catalogue (a user with permission
     * for no section at all) is something else, recognised by `sections` being
     * empty — the two cases show different screens, and collapsing them into a
     * single field is what made the previous version open on an arbitrary
     * section.
     *
     * [busca] lives here, and not in a `remember` on the screen, so it
     * survives going into a section and back: someone who filtered by
     * "docker", opened one and returned finds the filter as they left it.
     */
    data class Ready(
        val sections: List<SduiSection>,
        val selectedId: String?,
        val busca: String = "",
        val recentesIds: List<String> = emptyList(),
    ) : AdminCatalogState {

        /** The sections grouped, preserving the order the groups arrived in. */
        val grouped: List<Pair<String, List<SduiSection>>>
            get() = sections.groupBy { it.group }.toList()

        val selected: SduiSection?
            get() = sections.firstOrNull { it.id == selectedId }

        /**
         * The recents resolved against the CURRENT catalogue, in the order
         * they were used. An id that has left the catalogue (section removed,
         * permission revoked) simply does not match and disappears — no ghost
         * item that opens onto a 404.
         */
        val recentes: List<SduiSection>
            get() = recentesIds.mapNotNull { id -> sections.firstOrNull { it.id == id } }
    }
}

/**
 * Owns the section catalogue and which section is open.
 *
 * The list is NEVER a constant in this module: it arrives whole from
 * `GET /api/mobile/v1/screens`, already filtered by RBAC on the server. A
 * section the user may not see simply does not come — the app does not receive
 * it and does not draw a disabled item, which would confirm the screen exists.
 *
 * [rotaInicial] is the id from the `admin/{sectionId}` route. When it is
 * [ADMIN_SECTION_AUTO], no section is opened and the screen shows the
 * LAUNCHER. When it is a concrete id (a deep link from a notification, say) it
 * is honoured even if it is not in the catalogue: what actually authorises is
 * the fetch of `/screens/{id}`, and a 404 there becomes the calm "section not
 * available" message — the catalogue is a navigation hint, never the gate.
 */
class AdminCatalogViewModel(
    private val rotaInicial: String = ADMIN_SECTION_AUTO,
    private val catalogPort: SduiCatalogPort = SduiCatalogRepository(),
    /**
     * Where the recents come from and go to. Injectable because the real
     * implementation touches `SharedPreferences`, which does not exist on a
     * host JVM — the test's double is an in-memory list.
     */
    private val lerRecentes: () -> List<String> = { emptyList() },
    private val gravarRecente: (String) -> Unit = {},
) : ViewModel() {

    private val _uiState = MutableStateFlow<AdminCatalogState>(AdminCatalogState.Loading)
    val uiState: StateFlow<AdminCatalogState> = _uiState.asStateFlow()

    /**
     * Preserves the user's choice across catalogue reloads: someone who has
     * already switched section must not be thrown back to the initial one just
     * because the list was fetched again.
     */
    private var escolhaDoUsuario: String? = null

    /**
     * The filter text, preserved across catalogue reloads.
     *
     * Declared BEFORE the `init` on purpose: properties and `init` blocks run
     * in declaration order, and `load()` reads this field. Declared after, the
     * read happened before initialisation and the state stayed stuck in
     * Loading — with no visible error on screen.
     */
    private var buscaAtual: String = ""

    init {
        load()
    }

    fun load() {
        _uiState.value = AdminCatalogState.Loading
        viewModelScope.launch {
            when (val result = catalogPort.sections()) {
                is SduiSectionsResult.Success ->
                    _uiState.value = AdminCatalogState.Ready(
                        sections = result.sections,
                        selectedId = resolverSelecao(),
                        busca = buscaAtual,
                        recentesIds = lerRecentes(),
                    )

                is SduiSectionsResult.Error ->
                    _uiState.value = AdminCatalogState.Error(result.reason, ::load)
            }
        }
    }

    /**
     * Order of precedence: whatever the user chose in this session, otherwise
     * the concrete id that came in on the route. With neither, it returns null
     * — and null is the launcher, not an error.
     *
     * A deep link still beats everything: a notification pointing at
     * `docker.containers` opens there, because the question "which section"
     * was already answered by whoever sent the notification.
     */
    private fun resolverSelecao(): String? {
        escolhaDoUsuario?.let { return it }
        if (rotaInicial != ADMIN_SECTION_AUTO) return rotaInicial
        return null
    }

    fun select(sectionId: String) {
        escolhaDoUsuario = sectionId
        gravarRecente(sectionId)
        val current = _uiState.value as? AdminCatalogState.Ready ?: return
        if (current.selectedId == sectionId) return
        _uiState.value = current.copy(selectedId = sectionId, recentesIds = lerRecentes())
    }

    /**
     * Closes the section and returns to the launcher.
     *
     * It clears [escolhaDoUsuario] deliberately: without that, the next
     * catalogue reload would reopen the section the person has just closed.
     */
    fun voltarAoLancador() {
        escolhaDoUsuario = null
        val current = _uiState.value as? AdminCatalogState.Ready ?: return
        _uiState.value = current.copy(selectedId = null, recentesIds = lerRecentes())
    }

    fun buscar(termo: String) {
        buscaAtual = termo
        val current = _uiState.value as? AdminCatalogState.Ready ?: return
        _uiState.value = current.copy(busca = termo)
    }
}
