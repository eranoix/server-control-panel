package dev.servercontrolpanel.feature.admin

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.sdui.SduiCatalogPort
import dev.servercontrolpanel.data.sdui.SduiCatalogRepository
import dev.servercontrolpanel.data.sdui.SduiSection
import dev.servercontrolpanel.data.sdui.SduiSectionsResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

const val ADMIN_SECTION_AUTO = "auto"

sealed interface AdminCatalogState {
    data object Loading : AdminCatalogState

    data class Error(val message: String, val retry: () -> Unit) : AdminCatalogState

    data class Ready(
        val sections: List<SduiSection>,
        val selectedId: String?,
        val query: String = "",
        val recentIds: List<String> = emptyList(),
    ) : AdminCatalogState {

        val grouped: List<Pair<String, List<SduiSection>>>
            get() = sections.groupBy { it.group }.toList()

        val selected: SduiSection?
            get() = sections.firstOrNull { it.id == selectedId }

        val recents: List<SduiSection>
            get() = recentIds.mapNotNull { id -> sections.firstOrNull { it.id == id } }
    }
}

class AdminCatalogViewModel(
    private val initialRoute: String = ADMIN_SECTION_AUTO,
    private val catalogPort: SduiCatalogPort = SduiCatalogRepository(),
    private val readRecents: () -> List<String> = { emptyList() },
    private val writeRecent: (String) -> Unit = {},
) : ViewModel() {

    private val _uiState = MutableStateFlow<AdminCatalogState>(AdminCatalogState.Loading)
    val uiState: StateFlow<AdminCatalogState> = _uiState.asStateFlow()

    private var userChoice: String? = null

    private var currentQuery: String = ""

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
                        selectedId = resolveSelection(),
                        query = currentQuery,
                        recentIds = readRecents(),
                    )

                is SduiSectionsResult.Error ->
                    _uiState.value = AdminCatalogState.Error(result.reason, ::load)
            }
        }
    }

    private fun resolveSelection(): String? {
        userChoice?.let { return it }
        if (initialRoute != ADMIN_SECTION_AUTO) return initialRoute
        return null
    }

    fun select(sectionId: String) {
        userChoice = sectionId
        writeRecent(sectionId)
        val current = _uiState.value as? AdminCatalogState.Ready ?: return
        if (current.selectedId == sectionId) return
        _uiState.value = current.copy(selectedId = sectionId, recentIds = readRecents())
    }

    fun backToLauncher() {
        userChoice = null
        val current = _uiState.value as? AdminCatalogState.Ready ?: return
        _uiState.value = current.copy(selectedId = null, recentIds = readRecents())
    }

    fun search(term: String) {
        currentQuery = term
        val current = _uiState.value as? AdminCatalogState.Ready ?: return
        _uiState.value = current.copy(query = term)
    }
}
