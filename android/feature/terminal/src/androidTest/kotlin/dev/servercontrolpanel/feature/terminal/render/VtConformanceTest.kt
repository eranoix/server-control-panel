package dev.servercontrolpanel.feature.terminal.render

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import androidx.test.platform.app.InstrumentationRegistry
import dev.servercontrolpanel.terminalengine.CellSnapshot
import dev.servercontrolpanel.terminalengine.TerminalEngine
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Test

private const val OPAQUE_ALPHA = 0xff shl 24

class VtConformanceTest {

    private val assets get() = InstrumentationRegistry.getInstrumentation().context.assets

    private fun readVt(name: String): ByteArray = assets.open("vt/$name").use { it.readBytes() }

    private fun readJson(name: String): JsonObject =
        Json.parseToJsonElement(assets.open("vt/$name").use { it.readBytes().decodeToString() }).jsonObject

    private val fixtureNames = listOf(
        "01-sgr16", "02-sgr256", "03-truecolor", "04-attrs", "05-wide",
        "06-combining", "07-wrap", "08-reflow", "09-scrollregion",
        "10-altscreen", "11-mixed",
    )

    @Test
    fun allFixtures_matchExpectedGridAtCellLevel() {
        for (name in fixtureNames) {
            val vt = readVt("$name.vt")
            val doc = readJson("$name.expected.json")
            val cols = doc.getValue("cols").jsonPrimitive.int
            val rows = doc.getValue("rows").jsonPrimitive.int
            val checks = doc.getValue("checks").jsonArray

            val engine = TerminalEngine.create(cols, rows)
            try {
                var offset = 0
                for ((index, checkElem) in checks.withIndex()) {
                    val check = checkElem.jsonObject
                    val consume = check.getValue("consumeBytes").jsonPrimitive.int
                    if (consume > 0) {
                        engine.write(vt.copyOfRange(offset, offset + consume))
                        offset += consume
                    }
                    check["resizeTo"]?.jsonObject?.let { resize ->
                        engine.resize(resize.getValue("cols").jsonPrimitive.int, resize.getValue("rows").jsonPrimitive.int)
                    }
                    assertGridMatches(name, index, engine.snapshot(), check.getValue("expect").jsonObject)
                }
            } finally {
                engine.close()
            }
        }
    }

    private fun assertGridMatches(fixture: String, checkIndex: Int, snapshot: CellSnapshot, expect: JsonObject) {
        val label = "$fixture check[$checkIndex]"
        assertEquals("$label cols", expect.getValue("cols").jsonPrimitive.int, snapshot.cols)
        assertEquals("$label rows", expect.getValue("rows").jsonPrimitive.int, snapshot.rows)

        val cursor = expect.getValue("cursor").jsonObject
        assertEquals("$label cursor.x", cursor.getValue("x").jsonPrimitive.int, snapshot.cursorX)
        assertEquals("$label cursor.y", cursor.getValue("y").jsonPrimitive.int, snapshot.cursorY)
        assertEquals("$label cursor.visible", cursor.getValue("visible").jsonPrimitive.boolean, snapshot.cursorVisible)

        val rowFlags = expect.getValue("rowFlags").jsonArray
        for (y in rowFlags.indices) {
            val flags = rowFlags[y].jsonObject
            assertEquals("$label row $y wrapped", flags.getValue("wrapped").jsonPrimitive.boolean, snapshot.isWrapped(y))
            assertEquals(
                "$label row $y wrapContinuation",
                flags.getValue("wrapContinuation").jsonPrimitive.boolean,
                snapshot.isWrapContinuation(y),
            )
        }

        val grid = expect.getValue("grid").jsonArray
        for (y in grid.indices) {
            val row = grid[y].jsonArray
            for (x in row.indices) {
                val expectedCell = row[x].jsonObject
                val actual = snapshot.cellAt(x, y)
                val cellLabel = "$label cell($x,$y)"

                val expectedCp = expectedCell.getValue("cp").jsonPrimitive.content
                val expectedCodepoint = if (expectedCp.isEmpty()) 0 else expectedCp.codePointAt(0)
                assertEquals("$cellLabel codepoint", expectedCodepoint, actual.codepoint)

                assertColorMatches("$cellLabel fg", expectedCell["fg"], actual.fg)
                assertColorMatches("$cellLabel bg", expectedCell["bg"], actual.bg)

                assertEquals("$cellLabel bold", expectedCell.getValue("bold").jsonPrimitive.boolean, actual.bold)
                assertEquals("$cellLabel italic", expectedCell.getValue("italic").jsonPrimitive.boolean, actual.italic)
                assertEquals("$cellLabel faint", expectedCell.getValue("faint").jsonPrimitive.boolean, actual.faint)
                assertEquals("$cellLabel blink", expectedCell.getValue("blink").jsonPrimitive.boolean, actual.blink)
                assertEquals("$cellLabel inverse", expectedCell.getValue("inverse").jsonPrimitive.boolean, actual.inverse)
                assertEquals("$cellLabel invisible", expectedCell.getValue("invisible").jsonPrimitive.boolean, actual.invisible)
                assertEquals("$cellLabel strike", expectedCell.getValue("strike").jsonPrimitive.boolean, actual.strikethrough)
                assertEquals("$cellLabel overline", expectedCell.getValue("overline").jsonPrimitive.boolean, actual.overline)
                assertEquals("$cellLabel underline", expectedCell.getValue("underline").jsonPrimitive.int, actual.underline)
                assertEquals("$cellLabel wide", expectedCell.getValue("wide").jsonPrimitive.content, actual.wide.name)
            }
        }
    }

    private fun assertColorMatches(label: String, expected: JsonElement?, actual: Int?) {
        val expectedHex = expected?.jsonPrimitive?.contentOrNull
        if (expectedHex == null) {
            assertEquals(label, null, actual)
        } else {
            val expectedRgb = Integer.parseInt(expectedHex.removePrefix("#"), 16)
            assertEquals(label, expectedRgb, actual)
        }
    }

    @Test
    fun wideGlyph_pixelLevelMatchesGoldenPng_noDoubleDrawOrClip() {
        val cellWidthPx = 16f
        val cellHeightPx = 28f
        val cols = 6
        val rows = 3
        val defaultFg = 0xE0E0E0
        val defaultBg = 0x000000

        val engine = TerminalEngine.create(cols, rows)
        val snapshot = try {
            engine.write(readVt("05-wide.vt"))
            engine.snapshot()
        } finally {
            engine.close()
        }

        val bitmap = Bitmap.createBitmap(
            (cols * cellWidthPx).toInt(),
            (rows * cellHeightPx).toInt(),
            Bitmap.Config.ARGB_8888,
        )
        val canvas = Canvas(bitmap)
        canvas.drawColor(OPAQUE_ALPHA or defaultBg)
        val glyphAtlas = GlyphAtlas(cellWidthPx.toInt(), cellHeightPx.toInt())
        for (y in 0 until rows) {
            val cells = (0 until cols).map { x -> snapshot.cellAt(x, y) }
            val ops = buildRowDrawOps(cells, defaultFg, defaultBg)
            rasterizeRow(
                canvas,
                ops,
                y * cellHeightPx,
                cellWidthPx,
                cellHeightPx,
                glyphAtlas,
                defaultBg,
                debugSolidBlocks = true,
            )
        }

        val golden = assets.open("vt/05-wide.golden.png").use { BitmapFactory.decodeStream(it) }
        assertEquals("golden width", golden.width, bitmap.width)
        assertEquals("golden height", golden.height, bitmap.height)
        for (y in 0 until bitmap.height) {
            for (x in 0 until bitmap.width) {
                assertEquals("pixel($x,$y)", golden.getPixel(x, y), bitmap.getPixel(x, y))
            }
        }
    }
}
