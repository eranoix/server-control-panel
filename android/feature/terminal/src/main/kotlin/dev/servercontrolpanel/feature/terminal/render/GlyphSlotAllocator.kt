package dev.servercontrolpanel.feature.terminal.render

data class GlyphKey(
    val codepoint: Int,
    val bold: Boolean,
    val italic: Boolean,
    val wide: Boolean,
)

internal class GlyphSlotAllocator(private val capacity: Int) {

    init {
        require(capacity > 0) { "capacity must be positive, was $capacity" }
    }

    private val slotOf = LinkedHashMap<GlyphKey, Int>(capacity, 0.75f, true)
    private val freeSlots = ArrayDeque<Int>().apply { for (i in 0 until capacity) addLast(i) }

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
