package dev.servercontrolpanel.app

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Matrix
import android.graphics.Path
import android.graphics.RectF
import android.graphics.drawable.AdaptiveIconDrawable
import android.graphics.drawable.Drawable
import androidx.test.core.app.ApplicationProvider
import java.io.File
import kotlin.math.hypot
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The adaptive app icon. The launcher decides the crop shape, and the platform only guarantees
 * that the central 66dp circle survives every mask; the hexagon mark's points are what a
 * circular mask cuts first. Checking that no mark pixel leaves that circle covers all masks.
 *
 * Runs with `@GraphicsMode(NATIVE)` so real Skia rasterises the compiled VectorDrawable and the
 * platform's AdaptiveIconDrawable positions the layers. It also writes one PNG per mask into
 * `build/reports/icon-masks/` for visual review without a device.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class AppIconTest {

    /**
     * Side in pixels of the visible area (the 72dp left of 108dp after AdaptiveIconDrawable's
     * inset). Ten pixels per dp gives a tenth of a dp resolution.
     */
    private val visiblePx = 720

    private val pxPerDp = visiblePx / 72f

    /** Radius of the 66dp circle every mask preserves, at this scale. */
    private val safeRadiusPx = 66f * pxPerDp / 2f

    private fun adaptiveIcon(): AdaptiveIconDrawable {
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        val drawable = context.getDrawable(R.mipmap.ic_launcher)
        assertNotNull("R.mipmap.ic_launcher did not resolve to any drawable", drawable)
        assertTrue(
            "The launcher icon must be an AdaptiveIconDrawable (separate background and " +
                "foreground layers), not a flat bitmap, or the launcher cannot mask it or " +
                "animate parallax. Got: ${drawable!!.javaClass.name}",
            drawable is AdaptiveIconDrawable,
        )
        return drawable as AdaptiveIconDrawable
    }

    /**
     * The raw 108dp artwork with no mask. `AdaptiveIconDrawable.draw()` already applies the
     * device mask, so the layers are drawn one by one at the positions it assigned.
     *
     * @param withBackground `false` isolates the foreground layer over transparency,
     *   which is how the monochrome layer needs to be measured.
     */
    private fun rawArtwork(withBackground: Boolean = true): Bitmap {
        val icon = adaptiveIcon()
        icon.setBounds(0, 0, visiblePx, visiblePx)
        val extent = icon.foreground.bounds
        val bitmap = Bitmap.createBitmap(extent.width(), extent.height(), Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        canvas.translate(-extent.left.toFloat(), -extent.top.toFloat())
        if (withBackground) icon.background.draw(canvas)
        icon.foreground.draw(canvas)
        return bitmap
    }

    /** The center of the 108dp, in pixels of the raw artwork. */
    private fun center(bitmap: Bitmap) = bitmap.width / 2f

    /**
     * Pixels of the cyan mark. Mark and background sit at opposite ends of the green channel
     * (211 vs 6), so a midpoint threshold separates them regardless of antialiasing.
     */
    private fun markPixels(bitmap: Bitmap): Set<Pair<Int, Int>> =
        pixelsWhere(bitmap) { Color.green(it) > 128 && Color.alpha(it) > 128 }

    private inline fun pixelsWhere(bitmap: Bitmap, accepts: (Int) -> Boolean): Set<Pair<Int, Int>> {
        val found = mutableSetOf<Pair<Int, Int>>()
        for (y in 0 until bitmap.height) {
            for (x in 0 until bitmap.width) {
                if (accepts(bitmap.getPixel(x, y))) found += x to y
            }
        }
        return found
    }

    private fun maxDistanceFromCenter(bitmap: Bitmap, pixels: Set<Pair<Int, Int>>): Float {
        val c = center(bitmap)
        return pixels.maxOf { (x, y) -> hypot(x + 0.5f - c, y + 0.5f - c) }
    }

    @Test
    fun `the whole mark fits in the 66dp safe circle`() {
        val artwork = rawArtwork()
        val mark = markPixels(artwork)
        assertTrue("No mark pixel was found, the icon rendered empty.", mark.isNotEmpty())

        val farthest = maxDistanceFromCenter(artwork, mark)
        assertTrue(
            "The mark leaves the safe zone: its farthest point is %.2f dp from the center and the "
                .format(farthest / pxPerDp) +
                "guaranteed circle has a 33 dp radius. A circular mask would cut the " +
                "hexagon's points. Reduce scaleX/scaleY of the <group> in " +
                "res/drawable/ic_launcher_foreground.xml.",
            farthest <= safeRadiusPx,
        )
    }

    @Test
    fun `the mark fills the frame instead of floating in the middle`() {
        // A mark that is too small gets lost among other launcher icons; 80% of the safe
        // circle keeps the optical size of Material icons.
        val artwork = rawArtwork()
        val farthest = maxDistanceFromCenter(artwork, markPixels(artwork))
        assertTrue(
            "The mark is too small for the frame: it fills %.0f%% of the safe circle.".format(
                100f * farthest / safeRadiusPx,
            ),
            farthest >= safeRadiusPx * 0.80f,
        )
    }

    @Test
    fun `the monochrome layer respects the same safe zone`() {
        // Without `monochrome` the icon is excluded from Android 13+ themed icons.
        val monochrome: Drawable? = adaptiveIcon().monochrome
        assertNotNull(
            "The <adaptive-icon> must declare <monochrome>; the mark is drawn in " +
                "currentColor, so the layer comes for free.",
            monochrome,
        )

        // It is cropped by the same mask, so it must also stay inside the safe zone.
        val icon = adaptiveIcon()
        icon.setBounds(0, 0, visiblePx, visiblePx)
        val extent = icon.foreground.bounds
        val bitmap = Bitmap.createBitmap(extent.width(), extent.height(), Bitmap.Config.ARGB_8888)
        Canvas(bitmap).apply {
            translate(-extent.left.toFloat(), -extent.top.toFloat())
            icon.monochrome!!.draw(this)
        }
        val drawing = pixelsWhere(bitmap) { Color.alpha(it) > 128 }
        assertTrue("The monochrome layer rendered empty.", drawing.isNotEmpty())
        assertTrue(
            "The monochrome layer leaves the safe zone (%.2f dp from the center, limit 33 dp)."
                .format(maxDistanceFromCenter(bitmap, drawing) / pxPerDp),
            maxDistanceFromCenter(bitmap, drawing) <= safeRadiusPx,
        )
    }

    @Test
    fun `there is no transparent hole inside any mask`() {
        // A transparent pixel inside the mask shows the wallpaper through the icon.
        // Outside the masks transparency is fine, since that part is never drawn.
        val artwork = rawArtwork()
        for ((name, mask) in launcherMasks(artwork)) {
            val inner = Bitmap.createBitmap(artwork.width, artwork.height, Bitmap.Config.ARGB_8888)
            Canvas(inner).drawPath(mask, android.graphics.Paint().apply { color = Color.WHITE })
            for (y in 0 until artwork.height) {
                for (x in 0 until artwork.width) {
                    if (Color.alpha(inner.getPixel(x, y)) < 255) continue
                    assertEquals(
                        "Transparent hole at ($x,$y), inside mask '$name'. The background " +
                            "layer must be opaque up to the 108dp edge.",
                        255,
                        Color.alpha(artwork.getPixel(x, y)),
                    )
                }
            }
        }
    }

    @Test
    fun `the mark survives every mask launchers use`() {
        val artwork = rawArtwork()
        val unmaskedMark = markPixels(artwork)

        val output = File("build/reports/icon-masks").apply { mkdirs() }
        File(output, "00-raw-art-108dp.png").outputStream().use {
            artwork.compress(Bitmap.CompressFormat.PNG, 100, it)
        }

        for ((name, mask) in launcherMasks(artwork)) {
            val clipped = Bitmap.createBitmap(artwork.width, artwork.height, Bitmap.Config.ARGB_8888)
            Canvas(clipped).apply {
                clipPath(mask)
                drawBitmap(artwork, 0f, 0f, null)
            }
            File(output, "$name.png").outputStream().use {
                clipped.compress(Bitmap.CompressFormat.PNG, 100, it)
            }

            val lost = unmaskedMark - markPixels(clipped)
            assertTrue(
                "Mask '$name' cut ${lost.size} pixels of the mark. The artwork must " +
                    "fit in the 66dp circle, the only area every mask preserves.",
                lost.isEmpty(),
            )
        }
    }

    /**
     * The four shapes real launchers use, in the 100x100 space of `config_icon_mask`,
     * placed over the central 72dp of the 108dp artwork where the mask applies.
     */
    private fun launcherMasks(artwork: Bitmap): List<Pair<String, Path>> {
        val scale = visiblePx / 100f
        val margin = (artwork.width - visiblePx) / 2f
        fun build(name: String, draw: Path.() -> Unit): Pair<String, Path> {
            val positioned = Path()
            Path().apply(draw).transform(
                Matrix().apply {
                    setScale(scale, scale)
                    postTranslate(margin, margin)
                },
                positioned,
            )
            return name to positioned
        }
        return listOf(
            // Circle: Pixel Launcher default, and the harshest mask for a hexagon.
            build("01-circle") { addCircle(50f, 50f, 50f, Path.Direction.CW) },
            // Squircle: Samsung One UI and many other launchers.
            build("02-squircle") {
                moveTo(50f, 0f)
                cubicTo(10f, 0f, 0f, 10f, 0f, 50f)
                cubicTo(0f, 90f, 10f, 100f, 50f, 100f)
                cubicTo(90f, 100f, 100f, 90f, 100f, 50f)
                cubicTo(100f, 10f, 90f, 0f, 50f, 0f)
                close()
            },
            // Rounded rectangle: default on several skins (MIUI, ColorOS).
            build("03-rounded-rectangle") {
                addRoundRect(RectF(0f, 0f, 100f, 100f), 20f, 20f, Path.Direction.CW)
            },
            // Teardrop (AOSP icon-shape overlay). Asymmetric on purpose, to catch centring
            // errors a symmetric mask would hide.
            build("04-drop") {
                moveTo(50f, 0f)
                cubicTo(77.6f, 0f, 100f, 22.4f, 100f, 50f)
                lineTo(100f, 85f)
                cubicTo(100f, 93.3f, 93.3f, 100f, 85f, 100f)
                lineTo(50f, 100f)
                cubicTo(22.4f, 100f, 0f, 77.6f, 0f, 50f)
                cubicTo(0f, 22.4f, 22.4f, 0f, 50f, 0f)
                close()
            },
        )
    }
}
