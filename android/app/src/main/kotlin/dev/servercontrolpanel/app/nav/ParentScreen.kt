package dev.servercontrolpanel.app.nav

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
import dev.servercontrolpanel.data.sdui.SduiCatalogPort
import dev.servercontrolpanel.data.sdui.SduiCatalogRepository
import dev.servercontrolpanel.data.sdui.SduiSectionsResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

internal const val TAG_PARENT_GRID = "parent-grid"

internal class ParentViewModel(
    private val parent: ParentPage,
    private val catalog: SduiCatalogPort = SduiCatalogRepository(),
) : ViewModel() {

    private val _children = MutableStateFlow<List<ChildPage>?>(null)

    val children: StateFlow<List<ChildPage>?> = _children.asStateFlow()

    private val _all = MutableStateFlow<List<ChildPage>>(emptyList())
    val all: StateFlow<List<ChildPage>> = _all.asStateFlow()

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            val declared = childrenOf(parent)
            val available = when (val r = catalog.sections()) {
                is SduiSectionsResult.Success -> r.sections
                is SduiSectionsResult.Error -> emptyList()
            }
            val availableIds = available.map { it.id }.toSet()

            val existing = declared.filter { child ->
                when (val d = child.destination) {
                    is ChildDestination.Native -> true
                    is ChildDestination.Sdui -> d.sectionId in availableIds
                }
            }
            val alreadyListed = declared.mapNotNull {
                (it.destination as? ChildDestination.Sdui)?.sectionId
            }.toSet()

            val unknown = available
                .filter { it.id !in alreadyListed && it.id !in HIDDEN_SDUI_CHILDREN }
                .filter { parentOfSection(it.id) == parent }
                .map {
                    ChildPage(
                        title = it.label,
                        icon = UNKNOWN_SECTION_ICON,
                        destination = ChildDestination.Sdui(it.id),
                    )
                }

            _children.value = existing + unknown

            val declaredInAll = ParentPage.entries.flatMap { childrenOf(it) }
            val declaredIds = declaredInAll.mapNotNull {
                (it.destination as? ChildDestination.Sdui)?.sectionId
            }.toSet()
            _all.value = declaredInAll.filter { child ->
                when (val d = child.destination) {
                    is ChildDestination.Native -> true
                    is ChildDestination.Sdui -> d.sectionId in availableIds
                }
            } + available
                .filter { it.id !in declaredIds && it.id !in HIDDEN_SDUI_CHILDREN }
                .map {
                    ChildPage(
                        title = it.label,
                        icon = UNKNOWN_SECTION_ICON,
                        destination = ChildDestination.Sdui(it.id),
                    )
                }
        }
    }
}

@Composable
internal fun ParentScreen(
    parent: ParentPage,
    onOpenNative: (String) -> Unit,
    onOpenSdui: (String) -> Unit,
    modifier: Modifier = Modifier,
    vm: ParentViewModel = viewModel(key = "mae-${parent.id}") { ParentViewModel(parent) },
) {
    val children by vm.children.collectAsStateWithLifecycle()
    val all by vm.all.collectAsStateWithLifecycle()
    var query by remember { mutableStateOf("") }

    Column(modifier = modifier.fillMaxSize().testTag(TAG_PARENT_GRID)) {
        OutlinedTextField(
            value = query,
            onValueChange = { query = it },
            singleLine = true,
            label = { Text(SEARCH_SCREEN_LABEL) },
            leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
            trailingIcon = {
                if (query.isNotBlank()) {
                    IconButton(onClick = { query = "" }) {
                        Icon(Icons.Filled.Close, contentDescription = "Clear search")
                    }
                }
            },
            modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
        )

        val searching = query.isNotBlank()
        val result = if (searching) filterScreens(all, query) else children

        Box(modifier = Modifier.fillMaxSize()) {
        when (val list = result) {
            null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }

            else -> if (list.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Text(
                        text = if (searching) {
                            "No screen matches \"$query\"."
                        } else {
                            "Nothing in ${parent.title} for this account."
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
                    items(list, key = { it.title + it.destination }) { child ->
                        ChildTile(
                            child = child,
                            color = parentColor(parent),
                            onClick = {
                                when (val d = child.destination) {
                                    is ChildDestination.Native -> onOpenNative(d.route)
                                    is ChildDestination.Sdui -> onOpenSdui(d.sectionId)
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

internal fun filterScreens(screens: List<ChildPage>, query: String): List<ChildPage> {
    val term = query.trim().lowercase()
    if (term.isEmpty()) return screens
    return screens.filter { child ->
        child.title.lowercase().contains(term) ||
            (child.destination as? ChildDestination.Sdui)?.sectionId?.lowercase()?.contains(term) == true
    }
}

internal const val SEARCH_SCREEN_LABEL = "Search screens"

@Composable
private fun parentColor(parent: ParentPage): Color = when (parent) {
    ParentPage.System -> Color(0xFF4F8FD9)
    ParentPage.Docker -> Color(0xFF3BA9B4)
    ParentPage.Security -> Color(0xFFC98A2E)
    ParentPage.Operations -> Color(0xFF8B72D0)
    ParentPage.Apps -> Color(0xFF5E9E76)
    ParentPage.Dev -> Color(0xFFB0736B)
    ParentPage.Home, ParentPage.Settings -> MaterialTheme.colorScheme.primary
}

@Composable
private fun ChildTile(
    child: ChildPage,
    color: Color,
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
            .semantics(mergeDescendants = true) { contentDescription = child.title },
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
                    .background(color.copy(alpha = 0.18f)),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    imageVector = child.icon,
                    contentDescription = null,
                    tint = color,
                    modifier = Modifier.size(22.dp),
                )
            }
            Text(
                text = child.title,
                style = MaterialTheme.typography.labelMedium,
                textAlign = TextAlign.Center,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 8.dp),
            )
        }
    }
}
