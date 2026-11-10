package io.github.joshsymonds.orrery.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class HeaderScrollTest {
    private fun rule() = HeaderScrollRule(hideAfterPx = 28f, showAfterPx = 10f)

    @Test
    fun runOf28DownKeepsHeaderAndOnePixelMoreHidesIt() {
        val rule = rule()
        rule.onScroll(10f, atTop = false, atBottom = false)
        rule.onScroll(10f, atTop = false, atBottom = false)
        rule.onScroll(8f, atTop = false, atBottom = false)
        assertTrue(rule.visible)
        rule.onScroll(1f, atTop = false, atBottom = false)
        assertFalse(rule.visible)
    }

    @Test
    fun upRunOf9StaysHiddenAndAFurther2Shows() {
        val rule = rule()
        rule.onScroll(29f, atTop = false, atBottom = false)
        assertFalse(rule.visible)
        rule.onScroll(-9f, atTop = false, atBottom = false)
        assertFalse(rule.visible)
        rule.onScroll(-2f, atTop = false, atBottom = false)
        assertTrue(rule.visible)
    }

    @Test
    fun reversalRestartsTheDownRun() {
        val rule = rule()
        rule.onScroll(20f, atTop = false, atBottom = false)
        rule.onScroll(-1f, atTop = false, atBottom = false)
        rule.onScroll(20f, atTop = false, atBottom = false)
        assertTrue(rule.visible)
    }

    @Test
    fun atBottomShowsAHiddenHeader() {
        val rule = rule()
        rule.onScroll(29f, atTop = false, atBottom = false)
        assertFalse(rule.visible)
        rule.onScroll(5f, atTop = false, atBottom = true)
        assertTrue(rule.visible)
    }

    @Test
    fun atTopShowsAHiddenHeader() {
        val rule = rule()
        rule.onScroll(29f, atTop = false, atBottom = false)
        assertFalse(rule.visible)
        rule.onScroll(5f, atTop = true, atBottom = false)
        assertTrue(rule.visible)
    }

    @Test
    fun anEdgeResetsTheRunSoA20DownRunDoesNotHide() {
        val rule = rule()
        rule.onScroll(20f, atTop = false, atBottom = false)
        rule.onScroll(0f, atTop = true, atBottom = false)
        rule.onScroll(20f, atTop = false, atBottom = false)
        assertTrue(rule.visible)
    }
}
