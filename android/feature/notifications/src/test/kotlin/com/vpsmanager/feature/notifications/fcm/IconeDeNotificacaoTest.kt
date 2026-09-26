package com.vpsmanager.feature.notifications.fcm

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import androidx.test.core.app.ApplicationProvider
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The notification's small icon — the one that shows in the status bar.
 *
 * This test was born from a real and silent defect: `ic_notification.xml`
 * declared the namespace as `http://schemas.android.com/apis/res/android`, with
 * an "apis" where "apk" belonged. aapt2 does not complain about that. It
 * compiles the attributes as belonging to some unknown namespace and carries on
 * — `aapt2 dump xmltree` over the APK showed width, height, viewportWidth,
 * viewportHeight and pathData ALL outside the Android namespace. The result is a
 * `<vector>` that reaches the inflater with no dimension, no viewport and no
 * path: green build, broken notification on the device.
 *
 * The lesson this test pins down is that **referencing a drawable does not prove
 * it inflates**. `setSmallIcon(R.drawable.ic_notification)` compiles against any
 * syntactically valid XML. Only loading the resource for real and looking at the
 * pixels tells an icon apart from an inert resource.
 *
 * The second group of assertions covers the other half of the platform's
 * requirement: the small icon has to be a SILHOUETTE (alpha channel only).
 * Android discards the colour and repaints the shape in the theme colour, so a
 * coloured PNG or a drawing flat to the edge turns into that shapeless grey
 * square in the status bar.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class IconeDeNotificacaoTest {

    private val context: Context get() = ApplicationProvider.getApplicationContext()

    /** A real deploy notification, assembled by the production builder. */
    private fun notificacaoReal() = ActionableNotificationBuilder.build(
        context,
        mapOf(
            "event_type" to "job.finished",
            "job_id" to "hello-42",
            "severity" to "info",
            "title" to "Deploy concluído",
            "body" to "hello — deploy terminou em 12s",
        ),
    ).build()

    /**
     * Rasterises the notification's small icon at the canonical size of 24dp,
     * scaled up, over a transparent background — which is exactly how the
     * system consumes it.
     */
    private fun rasterizaIconePequeno(escala: Int = 16): Bitmap {
        val icone = notificacaoReal().smallIcon
        assertNotNull(
            "A notificação saiu sem smallIcon — o Android recusa notificação sem ícone pequeno.",
            icone,
        )
        val drawable = icone.loadDrawable(context)
        assertNotNull(
            "R.drawable.ic_notification não inflacionou: loadDrawable devolveu null. É o sintoma " +
                "de XML de vetor inválido — confira o xmlns (apk/res/android, não apis/res/android).",
            drawable,
        )
        assertTrue(
            "O drawable do ícone inflacionou sem tamanho intrínseco (${drawable!!.intrinsicWidth}x" +
                "${drawable.intrinsicHeight}). Um <vector> cujo android:width/height caiu fora do " +
                "namespace do Android chega assim — inerte, e invisível na barra de status.",
            drawable.intrinsicWidth > 0 && drawable.intrinsicHeight > 0,
        )
        assertEquals(
            "O ícone pequeno precisa medir os 24dp canônicos da barra de status.",
            24,
            drawable.intrinsicWidth,
        )

        val lado = drawable.intrinsicWidth * escala
        val bitmap = Bitmap.createBitmap(lado, lado, Bitmap.Config.ARGB_8888)
        drawable.setBounds(0, 0, lado, lado)
        drawable.draw(Canvas(bitmap))
        return bitmap
    }

    @Test
    fun `o icone da barra de status inflaciona e desenha alguma coisa`() {
        val bitmap = rasterizaIconePequeno()
        val opacos = contaOpacos(bitmap)
        assertTrue(
            "O ícone inflacionou mas renderizou VAZIO: nenhum pixel opaco. Uma notificação com " +
                "ícone vazio aparece como um buraco na barra de status.",
            opacos > 0,
        )
    }

    @Test
    fun `e uma silhueta, nao um quadrado chapado`() {
        val bitmap = rasterizaIconePequeno()
        val total = bitmap.width * bitmap.height
        val opacos = contaOpacos(bitmap)

        // The four corners have to be empty. An icon with a flat background (the
        // classic mistake of reusing the app's coloured icon) fills the corners
        // and is exactly what the system shows as a grey square.
        for ((x, y) in listOf(
            0 to 0,
            bitmap.width - 1 to 0,
            0 to bitmap.height - 1,
            bitmap.width - 1 to bitmap.height - 1,
        )) {
            assertEquals(
                "A quina ($x,$y) do ícone da barra de status está pintada. O ícone pequeno é " +
                    "silhueta sobre transparente: com fundo chapado o Android desenha um " +
                    "quadrado cinza sem forma.",
                0,
                Color.alpha(bitmap.getPixel(x, y)),
            )
        }

        // And the ink covers a fraction of the area consistent with a shape, not
        // with a block. A 20dp mark in a 24dp frame covers around 40%.
        val cobertura = 100f * opacos / total
        assertTrue(
            "O ícone cobre %.0f%% do quadro de 24dp. Fora da faixa de 15%% a 70%% ele não lê ".format(
                cobertura,
            ) + "como símbolo: ou sumiu, ou virou bloco.",
            cobertura in 15f..70f,
        )
    }

    @Test
    fun `preserva os recortes que fazem a marca ser reconhecivel`() {
        // The vps-manager mark is a hexagon with a spine and wedges in negative
        // space. At 24dp those voids are the difference between "the vps-manager
        // symbol" and "any old filled hexagon" — and they are the first thing to
        // go if someone swaps the drawing for an over-simplified version.
        val bitmap = rasterizaIconePequeno()
        val meio = bitmap.width / 2
        val transparentesNaColunaCentral = (0 until bitmap.height).count {
            Color.alpha(bitmap.getPixel(meio, it)) == 0
        }
        assertTrue(
            "A coluna central do ícone está sólida: a espinha em espaço negativo desapareceu.",
            transparentesNaColunaCentral > bitmap.height / 4,
        )
    }

    @Test
    fun `a arte fica disponivel para conferencia visual`() {
        // The icon is 24dp: at that size one more detail turns into a blur, and
        // no assert describes "it came out blurry". Whoever touches this does not
        // necessarily have a device at hand — the scaled-up PNG allows checking
        // it by eye.
        val saida = File("build/reports/icone-notificacao").apply { mkdirs() }
        File(saida, "ic-notification-24dp.png").outputStream().use {
            rasterizaIconePequeno(escala = 24).compress(Bitmap.CompressFormat.PNG, 100, it)
        }
        File(saida, "ic-notification-tamanho-real.png").outputStream().use {
            rasterizaIconePequeno(escala = 1).compress(Bitmap.CompressFormat.PNG, 100, it)
        }
    }

    private fun contaOpacos(bitmap: Bitmap): Int {
        var opacos = 0
        for (y in 0 until bitmap.height) {
            for (x in 0 until bitmap.width) {
                if (Color.alpha(bitmap.getPixel(x, y)) > 128) opacos++
            }
        }
        return opacos
    }
}
