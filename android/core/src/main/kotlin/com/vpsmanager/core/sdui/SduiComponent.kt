package com.vpsmanager.core.sdui

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * The closed, 7-type SDUI component vocabulary, plus [Unknown] — the variant
 * that makes an unrecognized server-sent `type` a safe no-op instead of a
 * parse crash.
 *
 * All 8 direct subclasses are nested here on purpose: a `sealed interface`
 * only reports every implementation via `sealedSubclasses` when the compiler
 * can see them all, which is exactly the property [SduiParsingTest] exploits
 * to guard against Kotlin/Go vocabulary drift — the Go side has its own
 * `len(AllComponentTypes()) == 7` guard, and this is its Kotlin mirror
 * (7 known + [Unknown] = 8).
 *
 * Deserialization is routed through [SduiComponentSerializer] (see
 * `SduiJson.kt`), which reads the wire `type` string and never throws for a
 * value it does not recognize.
 */
@Serializable(with = SduiComponentSerializer::class)
sealed interface SduiComponent {
    val id: String
    val permissionHint: String?
    val critical: Boolean

    /** The mutation-input workhorse (create container, edit scheduler job, ...). */
    @Serializable
    data class Form(
        override val id: String,
        val fields: List<SduiFormField>,
        @SerialName("submit_action") val submitAction: SduiActionRef,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    /** The workhorse for admin lists (containers, jobs, queue items, Jira issues). */
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

    /** Lighter-weight than [Table], for card-style feeds. */
    @Serializable
    data class ListComponent(
        override val id: String,
        @SerialName("item_template") val itemTemplate: String,
        @SerialName("rows_source") val rowsSource: SduiDataSource,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    /** Key-value read view of a single resource. */
    @Serializable
    data class Detail(
        override val id: String,
        @SerialName("data_source") val dataSource: SduiDataSource,
        val fields: List<SduiDetailField>,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    /** A standalone button/menu entry not attached to a row. */
    @Serializable
    data class Action(
        override val id: String,
        val label: String,
        @SerialName("action_id") val actionId: String,
        val style: String? = null,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    /** Read-only time series — deliberately minimal (line/bar only). */
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

    /** Wraps any action that deletes/kills/reboots; forces a confirmation UI. */
    @Serializable
    data class ConfirmDestructive(
        override val id: String,
        @SerialName("action_id") val actionId: String,
        val message: String,
        @SerialName("require_typed_confirmation") val requireTypedConfirmation: String? = null,
        @SerialName("permission_hint") override val permissionHint: String? = null,
        override val critical: Boolean = false,
    ) : SduiComponent

    /**
     * A component `type` this build of the client does not recognize.
     *
     * [critical] decides the renderer's contract (`:sdui`, plan 07-05): when
     * `false` the component is skipped and every other component on the
     * screen still renders; when `true` the renderer must show a needs-update
     * placeholder in its place instead of silently omitting something the
     * server considered essential.
     */
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
