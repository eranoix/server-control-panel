package com.vpsmanager.designsystem

import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.ui.graphics.Color

/**
 * The app's colour scheme — VPS Manager's identity, not Material's factory
 * purple.
 *
 * ## Where these colours came from
 * They were not invented. The web panel already has a brand and a palette the
 * owner recognises as "the vps-manager", and this scheme derives from it:
 *
 * | role | source colour | where it lives in the panel |
 * |-------|---------------|---------------------|
 * | primary | `#22D3EE` cyan | the most-used accent in `index.html` |
 * | tertiary | `#38BDF8` sky blue | the second accent |
 * | error | `#EF4444` red | the panel's failure red |
 * | neutrals | cyan at very low chroma | the panel's cool "slate" grey |
 *
 * ## Why the hex values are NOT the panel's
 * Because copying `#22D3EE` into the `primary` role would give an illegible
 * app. Material 3's roles are not "pretty colours", they are POSITIONS on a
 * tonal scale with guaranteed contrast between each (colour, on-colour) pair:
 * the light theme's `primary` is tone 40 on the scale, `onPrimary` is tone 100,
 * and it is that tonal distance that makes text legible on top of the button.
 * `#22D3EE` is roughly tone 78 — white text on it lands at 1.9:1, which on a
 * phone screen in sunlight is simply invisible.
 *
 * What is preserved from the identity is the HUE (218° in CIELAB) and the
 * saturation; what is recomputed is the tone. Every value below is the panel's
 * same cyan at a different height on the scale — that is how the brand survives
 * legibility instead of fighting it.
 *
 * ## How they were generated, and how to check
 * A tonal palette in CIELAB (HCT's tone is CIELAB's L*), with chroma clipped by
 * binary search to what fits the sRGB gamut at each tone — the same procedure
 * as the Material Theme Builder, which likewise emits static constants like
 * these rather than computing at runtime.
 *
 * **Every (colour, on-colour) pair was measured by WCAG contrast ratio**, not
 * eyeballed: the worst pair is 6.43:1 in the light theme and 5.53:1 in the
 * dark, against the AA minimum of 4.5:1 for text. `VpsmColorsTest` redoes that
 * measurement on every build — a "prettier" colour that drops a pair below AA
 * breaks the build instead of reaching the device.
 *
 * ## What does NOT go through here
 * - **The terminal grid.** Cell colours come from the VT emulator, not from
 *   Material — see `TerminalPalette` in `:feature-terminal`. An app scheme does
 *   not decide what "red" means in a command's output.
 * - **The ok/warning/critical states.** See [vpsmStatusColors]: they derive
 *   from this scheme (none has a loose colour) apart from the warning amber,
 *   which exists because Material has no semantic role for "attention". The
 *   amber was rechecked against the new surfaces: 10.0:1 on light and 10.6:1 on
 *   dark.
 */
internal val VpsmLightColors = lightColorScheme(
    primary = Color(0xFF006877),
    onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFA1EFFF),
    onPrimaryContainer = Color(0xFF001F25),
    inversePrimary = Color(0xFF2FD9F4),
    secondary = Color(0xFF40646B),
    onSecondary = Color(0xFFFFFFFF),
    secondaryContainer = Color(0xFFC2E9F2),
    onSecondaryContainer = Color(0xFF001F25),
    tertiary = Color(0xFF00668A),
    onTertiary = Color(0xFFFFFFFF),
    tertiaryContainer = Color(0xFFC4E7FF),
    onTertiaryContainer = Color(0xFF001E2C),
    error = Color(0xFFBF0323),
    onError = Color(0xFFFFFFFF),
    errorContainer = Color(0xFFFFDAD5),
    onErrorContainer = Color(0xFF3D0600),
    background = Color(0xFFF4FBFC),
    onBackground = Color(0xFF171C1D),
    surface = Color(0xFFF4FBFC),
    onSurface = Color(0xFF171C1D),
    surfaceVariant = Color(0xFFD4E6EA),
    onSurfaceVariant = Color(0xFF3A494D),
    surfaceTint = Color(0xFF006877),
    inverseSurface = Color(0xFF2C3132),
    inverseOnSurface = Color(0xFFEBF2F3),
    outline = Color(0xFF6A7A7D),
    outlineVariant = Color(0xFFB8CACE),
    scrim = Color(0xFF000000),
    surfaceBright = Color(0xFFF4FBFC),
    surfaceDim = Color(0xFFD4DBDD),
    surfaceContainer = Color(0xFFE8EFF1),
    surfaceContainerHigh = Color(0xFFE3E9EB),
    surfaceContainerHighest = Color(0xFFDDE4E5),
    surfaceContainerLow = Color(0xFFEEF5F6),
    surfaceContainerLowest = Color(0xFFFFFFFF),
)

/** The same scheme at the other end of the tonal scale — see [VpsmLightColors]. */
internal val VpsmDarkColors = darkColorScheme(
    primary = Color(0xFF2FD9F4),
    onPrimary = Color(0xFF00363E),
    primaryContainer = Color(0xFF004E5A),
    onPrimaryContainer = Color(0xFFA1EFFF),
    inversePrimary = Color(0xFF006877),
    secondary = Color(0xFFA7CDD5),
    onSecondary = Color(0xFF0F353C),
    secondaryContainer = Color(0xFF284C53),
    onSecondaryContainer = Color(0xFFC2E9F2),
    tertiary = Color(0xFF7DD0FF),
    onTertiary = Color(0xFF003549),
    tertiaryContainer = Color(0xFF004C69),
    onTertiaryContainer = Color(0xFFC4E7FF),
    error = Color(0xFFFFB4AA),
    onError = Color(0xFF68000D),
    errorContainer = Color(0xFF930018),
    onErrorContainer = Color(0xFFFFDAD5),
    background = Color(0xFF0E1415),
    onBackground = Color(0xFFDDE4E5),
    surface = Color(0xFF0E1415),
    onSurface = Color(0xFFDDE4E5),
    surfaceVariant = Color(0xFF3A494D),
    onSurfaceVariant = Color(0xFFB8CACE),
    surfaceTint = Color(0xFF2FD9F4),
    inverseSurface = Color(0xFFDDE4E5),
    inverseOnSurface = Color(0xFF2C3132),
    outline = Color(0xFF839497),
    outlineVariant = Color(0xFF3A494D),
    scrim = Color(0xFF000000),
    surfaceBright = Color(0xFF343A3B),
    surfaceDim = Color(0xFF0E1415),
    surfaceContainer = Color(0xFF1B2022),
    surfaceContainerHigh = Color(0xFF262B2C),
    surfaceContainerHighest = Color(0xFF303637),
    surfaceContainerLow = Color(0xFF171C1D),
    surfaceContainerLowest = Color(0xFF080F11),
)

/**
 * The background colour of the app's adaptive icon, for whoever assembles
 * `res/`.
 *
 * It lives here, rather than loose in an XML, because it IS the scheme: it is
 * the dark theme's `onPrimary` (cyan tone 20). The `logo-mark.svg` drawn on top
 * uses [MarcaSobreFundo] — which is the dark `primary`. Icon and app speaking
 * the same language is not a coincidence maintained by hand, it is the same
 * constant.
 */
val FundoDoIconeAdaptativo: Color = Color(0xFF00363E)

/** The brand colour on top of [FundoDoIconeAdaptativo]. */
val MarcaSobreFundo: Color = Color(0xFF2FD9F4)
