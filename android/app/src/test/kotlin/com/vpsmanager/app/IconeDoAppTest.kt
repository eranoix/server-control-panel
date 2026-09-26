package com.vpsmanager.app

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
 * The app icon — the one piece of the product the owner sees BEFORE opening
 * the app, and the one that turns up in the most places: launcher, install
 * screen, notification, recents screen.
 *
 * This test exists because of a risk specific to adaptive icons: the app
 * hands over 108dp of artwork, but WHO DECIDES the crop is the device's
 * launcher, and each one crops to a different shape (circle, squircle,
 * rounded rectangle, teardrop). The platform contract guarantees exactly one
 * thing: the central 66dp circle survives any mask. Artwork that goes beyond
 * it is cut — and the vps-manager mark is a HEXAGON, whose top and bottom
 * points are exactly what a circular mask lops off first.
 *
 * A human eye on a screenshot proves ONE mask, the one on the device that
 * took the screenshot. The central assertion here is stronger: no pixel of
 * the mark falls outside the safe 66dp circle — which holds for ALL possible
 * masks at once, including the ones that do not exist yet.
 *
 * Runs with `@GraphicsMode(NATIVE)`: real Skia rasterises the really
 * compiled VectorDrawable, and it is the platform's own AdaptiveIconDrawable
 * that positions the layers — nothing here reimplements the very thing being
 * verified.
 *
 * As a bonus, the test writes one PNG per mask into
 * `build/reports/icone-mascaras/`. It is not decoration: whoever touches the
 * icon does not necessarily have a device in hand, and a green `assertTrue`
 * does not show that the mark came out crooked or too small. The images do.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class IconeDoAppTest {

    /**
     * Side, in pixels, of the icon's VISIBLE area — the 72dp left of the 108dp
     * after the inset AdaptiveIconDrawable applies to the layers. Ten pixels
     * per dp give the safe-zone measurement a resolution of a tenth of a dp
     * instead of "roughly".
     */
    private val visivelPx = 720

    private val pxPorDp = visivelPx / 72f

    /** Radius of the 66dp circle every mask preserves, at this scale. */
    private val raioSeguroPx = 66f * pxPorDp / 2f

    private fun iconeAdaptativo(): AdaptiveIconDrawable {
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        val drawable = context.getDrawable(R.mipmap.ic_launcher)
        assertNotNull("R.mipmap.ic_launcher não resolveu para nenhum drawable", drawable)
        assertTrue(
            "O ícone do lançador precisa ser um AdaptiveIconDrawable (camadas de fundo e " +
                "primeiro plano separadas), e não um bitmap chapado — sem isso o lançador não " +
                "consegue mascarar nem animar em paralaxe. Veio: ${drawable!!.javaClass.name}",
            drawable is AdaptiveIconDrawable,
        )
        return drawable as AdaptiveIconDrawable
    }

    /**
     * The RAW 108dp artwork, with no mask at all.
     *
     * `AdaptiveIconDrawable.draw()` is no use here: it already crops to the
     * device's own mask, and on an already-cropped result there is no way to
     * demonstrate that the artwork survives OTHER masks. The layers are drawn
     * one by one, at the positions AdaptiveIconDrawable itself gave them when it
     * received the bounds — the inset arithmetic (108/72) is its, not this
     * test's.
     *
     * @param comFundo `false` isolates the foreground layer over transparency,
     *   which is how the monochrome layer needs to be measured.
     */
    private fun arteCrua(comFundo: Boolean = true): Bitmap {
        val icone = iconeAdaptativo()
        icone.setBounds(0, 0, visivelPx, visivelPx)
        val extensao = icone.foreground.bounds
        val bitmap = Bitmap.createBitmap(extensao.width(), extensao.height(), Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        canvas.translate(-extensao.left.toFloat(), -extensao.top.toFloat())
        if (comFundo) icone.background.draw(canvas)
        icone.foreground.draw(canvas)
        return bitmap
    }

    /** The centre of the 108dp, in pixels of the raw artwork. */
    private fun centro(bitmap: Bitmap) = bitmap.width / 2f

    /**
     * Pixels of the MARK (the cyan), separated from the background. The two
     * tones sit at opposite ends of the green channel (#22d3ee has 211, #020617
     * has 6), so a cut in the middle tells them apart without depending on how
     * the antialiasing fell.
     */
    private fun pixeisDaMarca(bitmap: Bitmap): Set<Pair<Int, Int>> =
        pixeisOndeVale(bitmap) { Color.green(it) > 128 && Color.alpha(it) > 128 }

    private inline fun pixeisOndeVale(bitmap: Bitmap, aceita: (Int) -> Boolean): Set<Pair<Int, Int>> {
        val encontrados = mutableSetOf<Pair<Int, Int>>()
        for (y in 0 until bitmap.height) {
            for (x in 0 until bitmap.width) {
                if (aceita(bitmap.getPixel(x, y))) encontrados += x to y
            }
        }
        return encontrados
    }

    private fun distanciaMaximaAoCentro(bitmap: Bitmap, pixeis: Set<Pair<Int, Int>>): Float {
        val c = centro(bitmap)
        return pixeis.maxOf { (x, y) -> hypot(x + 0.5f - c, y + 0.5f - c) }
    }

    @Test
    fun `a marca inteira cabe no circulo seguro de 66dp`() {
        val arte = arteCrua()
        val marca = pixeisDaMarca(arte)
        assertTrue("Nenhum pixel da marca foi encontrado — o ícone renderizou vazio.", marca.isNotEmpty())

        val maisLonge = distanciaMaximaAoCentro(arte, marca)
        assertTrue(
            "A marca escapa da zona segura: o ponto mais distante do centro está a %.2f dp e o "
                .format(maisLonge / pxPorDp) +
                "círculo garantido tem raio de 33 dp. Numa máscara circular as pontas do " +
                "hexágono apareceriam decepadas. Reduza o scaleX/scaleY do <group> em " +
                "res/drawable/ic_launcher_foreground.xml.",
            maisLonge <= raioSeguroPx,
        )
    }

    @Test
    fun `a marca ocupa a moldura em vez de flutuar perdida no meio`() {
        // The other side of the same mistake: a mark that is too small is not cut by
        // any mask, but gets lost in a dark frame and disappears among the other
        // icons in the launcher. The floor of 80% of the safe circle is what keeps
        // the optical presence of Material's icons.
        val arte = arteCrua()
        val maisLonge = distanciaMaximaAoCentro(arte, pixeisDaMarca(arte))
        assertTrue(
            "A marca está pequena demais para a moldura: ocupa %.0f%% do círculo seguro.".format(
                100f * maisLonge / raioSeguroPx,
            ),
            maisLonge >= raioSeguroPx * 0.80f,
        )
    }

    @Test
    fun `a camada monocromatica respeita a mesma zona segura`() {
        // Without `monochrome` the icon is left OUT of Android 13+'s themed icons:
        // on a whole screen repainted in the wallpaper's colours, vps-manager would
        // be the only one still in its dark blue.
        val monocromatica: Drawable? = iconeAdaptativo().monochrome
        assertNotNull(
            "O <adaptive-icon> precisa declarar <monochrome> — a marca é desenhada em " +
                "currentColor, então a camada sai de graça.",
            monocromatica,
        )

        // And it is cropped by the same mask as the rest: declaring the layer is no
        // use if its artwork overflows the safe zone.
        val icone = iconeAdaptativo()
        icone.setBounds(0, 0, visivelPx, visivelPx)
        val extensao = icone.foreground.bounds
        val bitmap = Bitmap.createBitmap(extensao.width(), extensao.height(), Bitmap.Config.ARGB_8888)
        Canvas(bitmap).apply {
            translate(-extensao.left.toFloat(), -extensao.top.toFloat())
            icone.monochrome!!.draw(this)
        }
        val desenho = pixeisOndeVale(bitmap) { Color.alpha(it) > 128 }
        assertTrue("A camada monocromática renderizou vazia.", desenho.isNotEmpty())
        assertTrue(
            "A camada monocromática escapa da zona segura (%.2f dp do centro, limite 33 dp)."
                .format(distanciaMaximaAoCentro(bitmap, desenho) / pxPorDp),
            distanciaMaximaAoCentro(bitmap, desenho) <= raioSeguroPx,
        )
    }

    @Test
    fun `nao ha buraco transparente dentro de nenhuma mascara`() {
        // The launcher shifts the layers in parallax and crops to the device's mask.
        // Any transparent pixel inside the mask becomes a hole — the wallpaper
        // showing through the icon. Opacity is only demanded INSIDE the masks:
        // outside them transparency is what is expected, because that piece of the
        // artwork is never drawn.
        val arte = arteCrua()
        for ((nome, mascara) in mascarasDeLancador(arte)) {
            val dentro = Bitmap.createBitmap(arte.width, arte.height, Bitmap.Config.ARGB_8888)
            Canvas(dentro).drawPath(mascara, android.graphics.Paint().apply { color = Color.WHITE })
            for (y in 0 until arte.height) {
                for (x in 0 until arte.width) {
                    if (Color.alpha(dentro.getPixel(x, y)) < 255) continue
                    assertEquals(
                        "Buraco transparente em ($x,$y), dentro da máscara '$nome' — a camada " +
                            "de fundo precisa ser chapada até a borda dos 108dp.",
                        255,
                        Color.alpha(arte.getPixel(x, y)),
                    )
                }
            }
        }
    }

    @Test
    fun `sobrevive intacta a todas as mascaras que os lancadores usam`() {
        val arte = arteCrua()
        val marcaSemMascara = pixeisDaMarca(arte)

        val saida = File("build/reports/icone-mascaras").apply { mkdirs() }
        File(saida, "00-arte-crua-108dp.png").outputStream().use {
            arte.compress(Bitmap.CompressFormat.PNG, 100, it)
        }

        for ((nome, mascara) in mascarasDeLancador(arte)) {
            val recortado = Bitmap.createBitmap(arte.width, arte.height, Bitmap.Config.ARGB_8888)
            Canvas(recortado).apply {
                clipPath(mascara)
                drawBitmap(arte, 0f, 0f, null)
            }
            File(saida, "$nome.png").outputStream().use {
                recortado.compress(Bitmap.CompressFormat.PNG, 100, it)
            }

            val perdidos = marcaSemMascara - pixeisDaMarca(recortado)
            assertTrue(
                "A máscara '$nome' cortou ${perdidos.size} pixels da marca. O desenho precisa " +
                    "caber no círculo de 66dp, que é o único pedaço que TODA máscara preserva.",
                perdidos.isEmpty(),
            )
        }
    }

    /**
     * The four shapes real launchers use to crop the adaptive icon, in the
     * 100x100 space in which Android describes `config_icon_mask`, positioned
     * over the central 72dp of the 108dp artwork — which is the area the mask
     * occupies.
     */
    private fun mascarasDeLancador(arte: Bitmap): List<Pair<String, Path>> {
        val escala = visivelPx / 100f
        val margem = (arte.width - visivelPx) / 2f
        fun montar(nome: String, desenhar: Path.() -> Unit): Pair<String, Path> {
            val posicionado = Path()
            Path().apply(desenhar).transform(
                Matrix().apply {
                    setScale(escala, escala)
                    postTranslate(margem, margem)
                },
                posicionado,
            )
            return nome to posicionado
        }
        return listOf(
            // Circle — the Pixel Launcher default, and the mask that punishes a hexagon
            // the most (it cuts the four corners and threatens the two points).
            montar("01-circulo") { addCircle(50f, 50f, 50f, Path.Direction.CW) },
            // Squircle — Samsung One UI and a good share of the Chinese launchers.
            montar("02-squircle") {
                moveTo(50f, 0f)
                cubicTo(10f, 0f, 0f, 10f, 0f, 50f)
                cubicTo(0f, 90f, 10f, 100f, 50f, 100f)
                cubicTo(90f, 100f, 100f, 90f, 100f, 50f)
                cubicTo(100f, 10f, 90f, 0f, 50f, 0f)
                close()
            },
            // Rounded rectangle — the default on several skins (MIUI, ColorOS).
            montar("03-retangulo-arredondado") {
                addRoundRect(RectF(0f, 0f, 100f, 100f), 20f, 20f, Path.Direction.CW)
            },
            // Teardrop — the circle with the bottom-right corner squared off, as in
            // AOSP's icon-shape overlay. Asymmetric on purpose: it catches a centring
            // error that a symmetric mask would hide.
            montar("04-gota") {
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
