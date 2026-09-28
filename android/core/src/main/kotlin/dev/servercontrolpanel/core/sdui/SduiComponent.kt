package dev.servercontrolpanel.core.sdui

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable(with = SduiComponentSerializer::class)
sealed interface SduiComponent {
    val id: String
    val permissionHint: String?
    val critical: Boolean

    @Serializable
    data class Form(
        override val id: String,
        val fields: List<SduiFormField>,
        @SerialName("submit_action") val submitAction: SduiActionRef,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    @Serializable
    data class Table(
        override val id: String,
        val columns: List<SduiTableColumn>,
        @SerialName("rows_source") val rowsSource: SduiDataSource,
        @SerialName("row_actions") val rowActions: List<SduiActionRef>? = null,
        @SerialName("empty_state") val emptyState: SduiEmptyState? = null,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    @Serializable
    data class ListComponent(
        override val id: String,
        @SerialName("item_template") val itemTemplate: String,
        @SerialName("rows_source") val rowsSource: SduiDataSource,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    @Serializable
    data class Detail(
        override val id: String,
        @SerialName("data_source") val dataSource: SduiDataSource,
        val fields: List<SduiDetailField>,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    @Serializable
    data class Action(
        override val id: String,
        val label: String,
        @SerialName("action_id") val actionId: String,
        val style: String? = null,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    @Serializable
    data class Chart(
        override val id: String,
        @SerialName("chart_kind") val chartKind: String,
        @SerialName("series_source") val seriesSource: SduiDataSource,
        @SerialName("x_key") val xKey: String,
        @SerialName("y_key") val yKey: String,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    @Serializable
    data class ConfirmDestructive(
        override val id: String,
        @SerialName("action_id") val actionId: String,
        val message: String,
        @SerialName("require_typed_confirmation") val requireTypedConfirmation: String? = null,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    @Serializable
    data class Unknown(
        override val id: String,
        val type: String,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent
}

@Serializable
data class SduiDataSource(
    val endpoint: String,
    val method: String? = null,
)

@Serializable
data class SduiTableColumn(
    val key: String,
    val label: String,
    val kind: String,
    @SerialName("badge_map") val badgeMap: Map<String, String>? = null,
)

@Serializable
data class SduiFormField(
    val key: String,
    val label: String,
    val kind: String,
    val required: Boolean = false,
    val placeholder: String? = null,
    val value: String? = null,
    val options: List<String>? = null,
    @SerialName("options_source") val optionsSource: SduiDataSource? = null,
)

@Serializable
data class SduiDetailField(
    val key: String,
    val label: String,
    val kind: String? = null,
)

@Serializable
data class SduiActionRef(
    @SerialName("action_id") val actionId: String,
    val label: String? = null,
    val style: String? = null,
)

@Serializable
data class SduiEmptyState(
    val text: String,
)
