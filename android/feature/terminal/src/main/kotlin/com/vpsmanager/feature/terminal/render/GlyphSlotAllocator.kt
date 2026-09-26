package com.vpsmanager.feature.terminal.render

/**
 * Identity of one rasterized glyph in the atlas. Bold/italic and the
 * double-width flag are part of the key because they change the pixels
 * (and, for [wide], the slot size) even for the same codepoint.
 */
data class GlyphKey(
    val codepoint: Int,
    val bold: Boolean,
    val italic: Boolean,
    val wide: Boolean,
)

/**
 * Pure LRU slot bookkeeping for [GlyphAtlas], split out from any
 * `android.graphics` type so it is runnable on the host JVM: [GlyphAtlas]
 * owns the actual `Bitmap`/`Canvas` rasterization and delegates purely to
 * this class for "which fixed-size slot does this key own, and what do I
 * evict when the atlas is full".
 *
 * [capacity] slots are numbered `0 until capacity`. [acquire] never
 * allocates a 21st slot beyond capacity -- once full it always evicts the
 * least-recently-used key and reassigns that slot, which is exactly what
 * gives the atlas a hard, constant memory bound regardless of how many
 * distinct glyphs a session has ever drawn.
 */
internal class GlyphSlotAllocator(private val capacity: Int) {

    init {
        require(capacity > 0) { "capacity must be positive, was $capacity" }
    }

    // LinkedHashMap in access order gives O(1) "least recently used" eviction:
    // the first entry after a get()/put() reorder is always the LRU one.
    private val slotOf = LinkedHashMap<GlyphKey, Int>(capacity, 0.75f, true)
    private val freeSlots = ArrayDeque<Int>().apply { for (i in 0 until capacity) addLast(i) }

    /** Result of [acquire]: which slot to (re)use, and whether it must be re-rasterized. */
    data class Acquisition(val slot: Int, val needsRasterize: Boolean, val evicted: GlyphKey?)

    fun acquire(key: GlyphKey): Acquisition {
        val existing = slotOf[key]
        if (existing != null) {
            return Acquisition(existing, needsRasterize = false, evicted = null)
        }

        val free = freeSlots.removeFirstOrNull()
        if (free != null) {
            slotOf[key] = free
            return Acquisition(free, needsRasterize = true, evicted = null)
        }

        // Full: LinkedHashMap in access-order iterates LRU-first.
        val lruEntry = slotOf.entries.first()
        val reused = lruEntry.value
        slotOf.remove(lruEntry.key)
        slotOf[key] = reused
        return Acquisition(reused, needsRasterize = true, evicted = lruEntry.key)
    }

    fun size(): Int = slotOf.size
    fun capacity(): Int = capacity
    fun contains(key: GlyphKey): Boolean = slotOf.containsKey(key)
}
