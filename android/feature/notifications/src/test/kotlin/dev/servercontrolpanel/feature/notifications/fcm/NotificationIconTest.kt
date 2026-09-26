package dev.servercontrolpanel.feature.notifications.fcm

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
 * The status bar small icon. Referencing a drawable does not prove it inflates (a
 * wrong xmlns compiles fine but yields an inert vector), so these tests load the
 * real resource and inspect its pixels. They also check it is a SILHOUETTE, since
 * Android repaints the alpha shape in the theme colour and a filled icon turns grey.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class NotificationIconTest {

    private val context: Context get() = ApplicationProvider.getApplicationContext()

    /** A real deploy notification, assembled by the production builder. */
    private fun realNotification() = ActionableNotificationBuilder.build(
        context,
        mapOf(
            "event_type" to "job.finished",
            "job_id" to "hello-42",
            "severity" to "info",
            "title" to "Deploy finished",
            "body" to "hello: deploy finished in 12s",
        ),
    ).build()

    /** Rasterises the small icon at 24dp times [scale] over transparency, as the system does. */
    private fun rasterizeSmallIcon(scale: Int = 16): Bitmap {
        val icon = realNotification().smallIcon
        assertNotNull(
            "The notification has no smallIcon; Android rejects notifications without one.",
            icon,
        )
        val drawable = icon.loadDrawable(context)
        assertNotNull(
            "R.drawable.ic_notification did not inflate: loadDrawable returned null. This means " +
                "invalid vector XML; check the xmlns (apk/res/android, not apis/res/android).",
            drawable,
        )
        assertTrue(
            "The icon drawable inflated with no intrinsic size (${drawable!!.intrinsicWidth}x" +
                "${drawable.intrinsicHeight}). A <vector> whose android:width/height fell outside " +
                "the Android namespace looks like this: inert and invisible in the status bar.",
            drawable.intrinsicWidth > 0 && drawable.intrinsicHeight > 0,
        )
        assertEquals(
            "The small icon must be the canonical 24dp of the status bar.",
            24,
            drawable.intrinsicWidth,
        )

        val side = drawable.intrinsicWidth * scale
        val bitmap = Bitmap.createBitmap(side, side, Bitmap.Config.ARGB_8888)
        drawable.setBounds(0, 0, side, side)
        drawable.draw(Canvas(bitmap))
        return bitmap
    }

    @Test
    fun `the status bar icon inflates and draws something`() {
        val bitmap = rasterizeSmallIcon()
        val opaque = countOpaque(bitmap)
        assertTrue(
            "The icon inflated but rendered EMPTY: no opaque pixel. A notification with an " +
                "empty icon shows as a hole in the status bar.",
            opaque > 0,
        )
    }

    @Test
    fun `it is a silhouette, not a filled square`() {
        val bitmap = rasterizeSmallIcon()
        val total = bitmap.width * bitmap.height
        val opaque = countOpaque(bitmap)

        // The corners must be empty; a filled background shows as a grey square.
        for ((x, y) in listOf(
            0 to 0,
            bitmap.width - 1 to 0,
            0 to bitmap.height - 1,
            bitmap.width - 1 to bitmap.height - 1,
        )) {
            assertEquals(
                "Corner ($x,$y) of the status bar icon is painted. The small icon is a " +
                    "silhouette over transparency: with a filled background Android draws a " +
                    "shapeless grey square.",
                0,
                Color.alpha(bitmap.getPixel(x, y)),
            )
        }

        // Coverage must look like a shape, not a block; a 20dp mark in a 24dp frame covers about 40%.
        val coverage = 100f * opaque / total
        assertTrue(
            "The icon covers %.0f%% of the 24dp frame. Outside 15%% to 70%% it does not read ".format(
                coverage,
            ) + "as a symbol: it either vanished or became a block.",
            coverage in 15f..70f,
        )
    }

    @Test
    fun `keeps the cutouts that make the mark recognisable`() {
        // The negative-space spine and wedges are what distinguish the mark from a plain hexagon.
        val bitmap = rasterizeSmallIcon()
        val middle = bitmap.width / 2
        val transparentInCenterColumn = (0 until bitmap.height).count {
            Color.alpha(bitmap.getPixel(middle, it)) == 0
        }
        assertTrue(
            "The icon's centre column is solid: the negative-space spine is gone.",
            transparentInCenterColumn > bitmap.height / 4,
        )
    }

    @Test
    fun `the artwork is written out for visual review`() {
        // No assert can detect a blurry 24dp icon, so write PNGs for checking by eye.
        val output = File("build/reports/notification-icon").apply { mkdirs() }
        File(output, "ic-notification-24dp.png").outputStream().use {
            rasterizeSmallIcon(scale = 24).compress(Bitmap.CompressFormat.PNG, 100, it)
        }
        File(output, "ic-notification-actual-size.png").outputStream().use {
            rasterizeSmallIcon(scale = 1).compress(Bitmap.CompressFormat.PNG, 100, it)
        }
    }

    private fun countOpaque(bitmap: Bitmap): Int {
        var opaque = 0
        for (y in 0 until bitmap.height) {
            for (x in 0 until bitmap.width) {
                if (Color.alpha(bitmap.getPixel(x, y)) > 128) opaque++
            }
        }
        return opaque
    }
}
