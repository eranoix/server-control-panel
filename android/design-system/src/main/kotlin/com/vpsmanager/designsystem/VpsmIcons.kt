package com.vpsmanager.designsystem

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.addPathNodes
import androidx.compose.ui.unit.dp

/**
 * The Material glyphs the navigation drawer needs and that the
 * `material-icons-core` set does NOT ship.
 *
 * ## Why this exists instead of pulling in `material-icons-extended`
 * The extended package would solve everything with one import, but it costs
 * **35.7 MB** of aar — and this build enables minification in no `buildType`
 * (see `app/build.gradle.kts`), so all 35 MB would land in the APK. In a
 * project that filters ABIs precisely to save 18 MB of download, trading five
 * icons for 35 MB would be a bigger regression than the defect these icons
 * fix. The `core` set (823 KB) covers Home/Settings/Notifications/Info/Menu;
 * the five below complete the rest.
 *
 * ## Where the artwork comes from
 * These are not invented drawings: each `PATH_*` is the `d` of the official
 * 24dp SVG Google publishes at
 * `fonts.gstatic.com/s/i/materialicons/<name>/v1/24px.svg` — the same vectors
 * `material-icons-extended` packages. The icon is built with the 24×24
 * viewport and the 24dp size every Material icon uses, so they line up pixel
 * for pixel with the `core` ones when they appear in the same list.
 *
 * Each `ImageVector` is created once (`by lazy`) and reused — the same
 * contract as the generated `Icons.Filled.*`, which are lazy objects too. A
 * new `ImageVector` on every recomposition would force Compose to rebuild the
 * vector's tree for nothing.
 */
object VpsmIcons {

    // ── The dashboard and launcher glyphs ───────────────────────────────────
    //
    // They arrived because the Administration launcher identified each section
    // by the GROUP'S INITIAL in a circle — and the initial identifies nothing:
    // five Docker sections become five identical "D"s, and `System` and
    // `Security` are both "S". The owner sent a photo of exactly that. A symbol
    // that does not distinguish is worse than none, because it occupies the
    // place where the eye looks for the difference.

    /** Memory chip — RAM. */
    val Memoria: ImageVector by lazy {
        materialVector(
            name = "Memoria",
            pathData = "M15,9H9v6h6V9z M13,13h-2v-2h2V13z M21,11V9h-2V7c0-1.1-0.9-2-2-2h-2V3h-2v2h-2V3H9v2H7" +
                "C5.9,5,5,5.9,5,7v2H3v2h2v2H3v2h2v2c0,1.1,0.9,2,2,2h2v2h2v-2h2v2h2v-2h2c1.1,0,2-0.9,2-2v-2h2v-2" +
                "h-2v-2H21z M17,17H7V7h10V17z",
        )
    }

    /** Discos empilhados — armazenamento. */
    val Disco: ImageVector by lazy {
        materialVector(
            name = "Disco",
            pathData = "M2,20h20v-4H2V20z M4,17h2v2H4V17z M2,4v4h20V4H2z M6,7H4V5h2V7z M2,14h20v-4H2V14z " +
                "M4,11h2v2H4V11z",
        )
    }

    /** A speedometer needle — CPU load. */
    val Velocimetro: ImageVector by lazy {
        materialVector(
            name = "Velocimetro",
            pathData = "M20.38,8.57l-1.23,1.85a8,8,0,0,1-0.22,7.58H5.07A8,8,0,0,1,15.58,6.85l1.85-1.23A10,10,0,0,0," +
                "3.35,19a2,2,0,0,0,1.72,1H18.92a2,2,0,0,0,1.74-1A10,10,0,0,0,20.38,8.57z M10.59,15.41a2,2,0,0,0," +
                "2.83,0l5.66-8.49-8.49,5.66A2,2,0,0,0,10.59,15.41z",
        )
    }

    /** Layers — containers, images, volumes: Docker's stacked things. */
    val Camadas: ImageVector by lazy {
        materialVector(
            name = "Camadas",
            pathData = "M11.99,18.54l-7.37-5.73L3,14.07l9,7l9-7l-1.63-1.27L11.99,18.54z " +
                "M12,16l7.36-5.73L21,9l-9-7L3,9l1.63,1.27L12,16z",
        )
    }

    /** Two arrows swapping sides — swap. */
    val Troca: ImageVector by lazy {
        materialVector(
            name = "Troca",
            pathData = "M6.99,11L3,15l3.99,4v-3H14v-2H6.99V11z M21,9l-3.99-4v3H10v2h7.01v3L21,9z",
        )
    }

    /** A stopwatch — how long the machine has been up. */
    val Cronometro: ImageVector by lazy {
        materialVector(
            name = "Cronometro",
            pathData = "M15,1H9v2h6V1z M11,14h2V8h-2V14z M19.03,7.39l1.42-1.42c-0.43-0.51-0.9-0.99-1.41-1.41" +
                "l-1.42,1.42C16.07,4.74,14.12,4,12,4c-4.97,0-9,4.03-9,9s4.02,9,9,9s9-4.03,9-9" +
                "C21,10.88,20.26,8.93,19.03,7.39z M12,20c-3.87,0-7-3.13-7-7s3.13-7,7-7s7,3.13,7,7S15.87,20,12,20z",
        )
    }

    /** A monitor heart — the subsystems' health. */
    val Saude: ImageVector by lazy {
        materialVector(
            name = "Saude",
            pathData = "M13.5,8c-1.11,0-2.08,0.6-2.6,1.5h-0.79C9.58,8.6,8.61,8,7.5,8C5.56,8,4,9.56,4,11.5" +
                "c0,3.78,4.9,7.62,6.19,8.55c0.48,0.35,1.14,0.35,1.62,0C13.1,19.12,18,15.28,18,11.5" +
                "C18,9.56,16.44,8,14.5,8H13.5z M12,18.3C10.53,17.16,6,13.53,6,11.5C6,10.66,6.66,10,7.5,10" +
                "c0.51,0,0.93,0.24,1.19,0.65l0.6,0.85h5.42l0.6-0.85C15.57,10.24,15.99,10,16.5,10" +
                "c0.84,0,1.5,0.66,1.5,1.5C18,13.53,13.47,17.16,12,18.3z",
        )
    }

    /** A rocket — a deployment. */
    val Entrega: ImageVector by lazy {
        materialVector(
            name = "Entrega",
            pathData = "M9.19,6.35c-2.04,2.29-3.44,5.58-3.57,5.89L2,10.69l4.05-4.05c0.47-0.47,1.15-0.68,1.81-0.55" +
                "L9.19,6.35z M11.17,17c0,0,3.74-1.55,5.89-3.7c5.4-5.4,4.5-9.62,4.21-10.57" +
                "c-0.95-0.3-5.17-1.19-10.57,4.21C8.55,9.09,7,12.83,7,12.83L11.17,17z " +
                "M17.65,14.81c-2.29,2.04-5.58,3.44-5.89,3.57L13.31,22l4.05-4.05c0.47-0.47,0.68-1.15,0.55-1.81" +
                "L17.65,14.81z M9,18c0,0.83-0.34,1.58-0.88,2.12C6.94,21.3,2,22,2,22s0.7-4.94,1.88-6.12" +
                "C4.42,15.34,5.17,15,6,15C7.66,15,9,16.34,9,18z M13,9c0-1.1,0.9-2,2-2s2,0.9,2,2s-0.9,2-2,2S13,10.1,13,9z",
        )
    }

    /** Terminal: the window with the `>` prompt and the cursor. */
    val Terminal: ImageVector by lazy {
        materialVector(
            name = "Terminal",
            pathData = "M20,4H4C2.89,4,2,4.9,2,6v12c0,1.1,0.89,2,2,2h16c1.1,0,2-0.9,2-2V6C22,4.9,21.11,4,20,4z " +
                "M20,18H4V8h16V18z M18,17h-6v-2h6V17z M7.5,17l-1.41-1.41L8.67,13l-2.59-2.59L7.5,9l4,4L7.5,17z",
        )
    }

    /** A folder — the server's files. */
    val Folder: ImageVector by lazy {
        materialVector(
            name = "Folder",
            pathData = "M10 4H4c-1.1 0-1.99.9-1.99 2L2 18c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2h-8l-2-2z",
        )
    }

    /** Speech bubble — WhatsApp. */
    val Chat: ImageVector by lazy {
        materialVector(
            name = "Chat",
            pathData = "M20 2H4c-1.1 0-1.99.9-1.99 2L2 22l4-4h14c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2zM6 9h12v2H6V9z" +
                "m8 5H6v-2h8v2zm4-6H6V6h12v2z",
        )
    }

    /**
     * A video camera — the video call. Deliberately different from
     * `Icons.Filled.Call` (the handset), which would say "voice call".
     */
    val Videocam: ImageVector by lazy {
        materialVector(
            name = "Videocam",
            pathData = "M17 10.5V7c0-.55-.45-1-1-1H4c-.55 0-1 .45-1 1v10c0 .55.45 1 1 1h12c.55 0 1-.45 1-1v-3.5" +
                "l4 4v-11l-4 4z",
        )
    }

    /**
     * Sign out: the arrow POINTING OUT of the door. `core` only has
     * `ExitToApp`, whose arrow points inward — that is, it draws "enter"
     * underneath a label that says "Sign out".
     */
    val Logout: ImageVector by lazy {
        materialVector(
            name = "Logout",
            pathData = "M17 7l-1.41 1.41L18.17 11H8v2h10.17l-2.58 2.58L17 17l5-5zM4 5h8V3H4c-1.1 0-2 .9-2 2v14" +
                "c0 1.1.9 2 2 2h8v-2H4V5z",
        )
    }
    // ── Glyphs added to STOP THE REPETITION in the Admin launcher ─────────
    //
    // The owner sent a photo: a padlock on four different sections, layers on
    // three, a person on three. The icon map existed and did not distinguish,
    // because the available vocabulary was far too small — Material's `core`
    // set has ~48 glyphs, and `extended` weighs 35.7 MB and is out of the
    // question. Drawing the missing ones costs bytes, not megabytes.

    /** A closed box — a container, distinct from the IMAGES it comes from. */
    val Caixa: ImageVector by lazy {
        materialVector(
            name = "Caixa",
            pathData = "M12,2L4,6v12l8,4l8,-4V6L12,2z M12,4.2L17.5,7L12,9.8L6.5,7L12,4.2z M6,8.6l5,2.5v7.3" +
                "l-5,-2.5V8.6z M13,18.4v-7.3l5,-2.5v7.3L13,18.4z",
        )
    }

    /** Shield — perimeter protection (firewall), distinct from a secret. */
    val Escudo: ImageVector by lazy {
        materialVector(
            name = "Escudo",
            pathData = "M12,1L3,5v6c0,5.55,3.84,10.74,9,12c5.16,-1.26,9,-6.45,9,-12V5L12,1z M12,11.99h7" +
                "c-0.53,4.12,-3.28,7.79,-7,8.94V12H5V6.3l7,-3.11V11.99z",
        )
    }

    /** A key — a stored secret, distinct from the shield that blocks. */
    val Chave: ImageVector by lazy {
        materialVector(
            name = "Chave",
            pathData = "M21,10h-8.35C11.83,7.67,9.61,6,7,6c-3.31,0,-6,2.69,-6,6s2.69,6,6,6c2.61,0,4.83," +
                "-1.67,5.65,-4H13l2,2l2,-2l2,2l4,-4.04L21,10z M7,15c-1.65,0,-3,-1.35,-3,-3s1.35,-3,3,-3" +
                "s3,1.35,3,3S8.65,15,7,15z",
        )
    }

    /** A globe — a name that resolves on the network (DNS). */
    val Globo: ImageVector by lazy {
        materialVector(
            name = "Globo",
            pathData = "M12,2C6.48,2,2,6.48,2,12s4.48,10,10,10s10,-4.48,10,-10S17.52,2,12,2z M11,19.93" +
                "c-3.95,-0.49,-7,-3.85,-7,-7.93c0,-0.62,0.08,-1.21,0.21,-1.79L9,15v1c0,1.1,0.9,2,2,2V19.93z" +
                " M17.9,17.39c-0.26,-0.81,-1,-1.39,-1.9,-1.39h-1v-3c0,-0.55,-0.45,-1,-1,-1H8v-2h2c0.55,0,1," +
                "-0.45,1,-1V7h2c1.1,0,2,-0.9,2,-2V4.59C17.93,5.77,20,8.65,20,12C20,14.08,19.2,15.97,17.9,17.39z",
        )
    }

    /** Rising bars — a volume measured over time (network usage). */
    val Grafico: ImageVector by lazy {
        materialVector(
            name = "Grafico",
            pathData = "M5,9.2h3V19H5V9.2z M10.6,5h2.8v14h-2.8V5z M16.2,13H19v6h-2.8V13z",
        )
    }

    /** A phone — a paired device, distinct from a person's ACCOUNT. */
    val Celular: ImageVector by lazy {
        materialVector(
            name = "Celular",
            pathData = "M17,1.01L7,1c-1.1,0,-2,0.9,-2,2v18c0,1.1,0.9,2,2,2h10c1.1,0,2,-0.9,2,-2V3" +
                "c0,-1.1,-0.9,-1.99,-2,-1.99z M17,19H7V5h10V19z",
        )
    }

    /** A square with pins — the processor, and by extension processes. */
    val Cpu: ImageVector by lazy {
        materialVector(
            name = "Cpu",
            pathData = "M9,9h6v6H9V9z M21,11V9h-2V7c0,-1.1,-0.9,-2,-2,-2h-2V3h-2v2h-2V3H9v2H7C5.9,5,5,5.9,5,7" +
                "v2H3v2h2v2H3v2h2v2c0,1.1,0.9,2,2,2h2v2h2v-2h2v2h2v-2h2c1.1,0,2,-0.9,2,-2v-2h2v-2h-2v-2H21z" +
                " M17,17H7V7h10V17z",
        )
    }

    /** A socket — a listening port, something you connect to. */
    val Tomada: ImageVector by lazy {
        materialVector(
            name = "Tomada",
            pathData = "M16,7V3h-2v4h-4V3H8v4H8c-1.1,0,-2,0.9,-2,2v5.5L9.5,18v3h5v-3L18,14.5V9C18,7.9,17.1,7,16,7z" +
                " M16,13.5l-3.5,3.5h-1L8,13.5V9h8V13.5z",
        )
    }

    /** A monitor with a cursor — an ACTIVE session, not the account that opened it. */
    val SessaoAtiva: ImageVector by lazy {
        materialVector(
            name = "SessaoAtiva",
            pathData = "M20,3H4C2.9,3,2,3.9,2,5v11c0,1.1,0.9,2,2,2h5v2h6v-2h5c1.1,0,2,-0.9,2,-2V5" +
                "C22,3.9,21.1,3,20,3z M20,16H4V5h16V16z M7,7.5l3.5,3L7,13.5V7.5z M12,12h5v1.5h-5V12z",
        )
    }

    /** A paper clip — attach a file. */
    val Clipe: ImageVector by lazy {
        materialVector(
            name = "Clipe",
            pathData = "M16.5,6v11.5c0,2.21,-1.79,4,-4,4s-4,-1.79,-4,-4V5c0,-1.38,1.12,-2.5,2.5,-2.5" +
                "s2.5,1.12,2.5,2.5v10.5c0,0.55,-0.45,1,-1,1s-1,-0.45,-1,-1V6H10v9.5c0,1.38,1.12,2.5,2.5,2.5" +
                "s2.5,-1.12,2.5,-2.5V5c0,-2.21,-1.79,-4,-4,-4S7,2.79,7,5v12.5c0,3.04,2.46,5.5,5.5,5.5" +
                "s5.5,-2.46,5.5,-5.5V6H16.5z",
        )
    }

    /**
     * A kanban board — three columns of different heights.
     *
     * The closest Material drawing would be `view_column`, and it has all three
     * columns at the SAME height: a rectangle split in three, which at 24 dp
     * reads as "table". The uneven height is what says "kanban" — it is what
     * represents columns holding different amounts of work, which is the
     * information you look at a board for.
     */
    val Quadro: ImageVector by lazy {
        materialVector(
            name = "Quadro",
            pathData = "M4,4h4v13H4V4z M10,4h4v9h-4V4z M16,4h4v16h-4V4z M3,2C2.45,2,2,2.45,2,3v18" +
                "c0,0.55,0.45,1,1,1h18c0.55,0,1,-0.45,1,-1V3c0,-0.55,-0.45,-1,-1,-1H3z M4,20V3h16v17H4z",
        )
    }

}

/**
 * Assembles an `ImageVector` in Material's canonical shape: 24dp size, a 24×24
 * viewport and a single path filled with [Color.Black], which the `Icon`'s
 * `tint` replaces with the theme's colour.
 */
private fun materialVector(name: String, pathData: String): ImageVector =
    ImageVector.Builder(
        name = name,
        defaultWidth = 24.dp,
        defaultHeight = 24.dp,
        viewportWidth = 24f,
        viewportHeight = 24f,
    ).addPath(
        pathData = addPathNodes(pathData),
        fill = SolidColor(Color.Black),
    ).build()
