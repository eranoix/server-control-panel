package com.vpsmanager.feature.terminal.render

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class GlyphSlotAllocatorTest {

    private fun key(cp: Char) = GlyphKey(cp.code, bold = false, italic = false, wide = false)

    @Test
    fun firstAcquisitionOfEachKey_needsRasterize_untilCapacityExhausted() {
        val allocator = GlyphSlotAllocator(capacity = 2)

        val a = allocator.acquire(key('a'))
        assertTrue(a.needsRasterize)
        assertNull(a.evicted)

        val b = allocator.acquire(key('b'))
        assertTrue(b.needsRasterize)
        assertNull(b.evicted)
        assertEquals(2, allocator.size())
    }

    @Test
    fun reAcquiringSameKey_hitsCacheWithoutRasterizing() {
        val allocator = GlyphSlotAllocator(capacity = 2)
        val first = allocator.acquire(key('a'))
        val second = allocator.acquire(key('a'))

        assertEquals(first.slot, second.slot)
        assertFalse(second.needsRasterize)
        assertNull(second.evicted)
    }

    @Test
    fun exceedingCapacity_evictsLeastRecentlyUsed() {
        val allocator = GlyphSlotAllocator(capacity = 2)
        allocator.acquire(key('a'))
        allocator.acquire(key('b'))
        // Touch 'a' so 'b' becomes the LRU one.
        allocator.acquire(key('a'))

        val third = allocator.acquire(key('c'))
        assertTrue(third.needsRasterize)
        assertEquals(key('b'), third.evicted)
        assertFalse(allocator.contains(key('b')))
        assertTrue(allocator.contains(key('a')))
        assertTrue(allocator.contains(key('c')))
        assertEquals(2, allocator.size())
    }

    @Test
    fun neverExceedsDeclaredCapacity_regardlessOfDistinctKeysSeen() {
        val allocator = GlyphSlotAllocator(capacity = 4)
        for (cp in 'a'..'z') {
            allocator.acquire(key(cp))
            assertTrue(allocator.size() <= 4)
        }
        assertEquals(4, allocator.size())
    }
}
