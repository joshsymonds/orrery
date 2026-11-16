package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.text.TextLayoutResult
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

private const val SLUG = "orrery-style-a2b-glance"
private const val STATE = "building · 1h 12m"
private const val BLOCKER = "waiting on the owner's answer"
private val ACTIONS = listOf("talk", "pause", "stop")


/**
 * The title row at a phone's width, with the longest content it carries: an epic's slug, a state with its phase time,
 * a blocker, and Talk, Pause and Stop, in the app's own monospace type. One row for all of it measured the state and actions first and left the crumbs
 * about one character, so on a phone (v0.85.3) the slug stood one letter to a line.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w411dp-h891dp")
// Real text measurement, so a crumb that wraps shows as more than one line.
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class TitleRowPhoneWidthTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun theEpicPagesSlugStaysOnOneLineBesideStateBlockerAndActions() {
        setRow(listOf(Crumb("orrery") {}, Crumb(SLUG)))

        val slug = layout(hasTestTag("crumb:1"))
        assertEquals("the slug's lines", 1, slug.lineCount)
        assertEquals("the slug's shown characters", SLUG.length, slug.getLineEnd(0, visibleEnd = true))
        assertEquals("the orrery crumb's lines", 1, layout(hasText("orrery") and hasAnyAncestor(hasTestTag("crumb:0"))).lineCount)
        assertAllShown()
    }

    @Test
    fun theContractPagesSlugStaysOnOneLineAsAMiddleCrumb() {
        setRow(listOf(Crumb("orrery") {}, Crumb(SLUG) {}, Crumb("contract")))

        val slug = layout(hasText(SLUG) and hasAnyAncestor(hasTestTag("crumb:1")))
        assertEquals("the slug's lines", 1, slug.lineCount)
        assertEquals("the slug's shown characters", SLUG.length, slug.getLineEnd(0, visibleEnd = true))
        val contract = layout(hasTestTag("crumb:2"))
        assertEquals("the last crumb's lines", 1, contract.lineCount)
        assertEquals("the last crumb shows whole", "contract".length, contract.getLineEnd(0, visibleEnd = true))
        assertAllShown()
    }

    @Test
    fun thePhasePageShowsItsOwnCrumbWholeAndTheSlugOnOneLine() {
        val page = "R12 · integrating"
        setRow(listOf(Crumb("orrery") {}, Crumb(SLUG) {}, Crumb(page)))

        val slug = layout(hasText(SLUG) and hasAnyAncestor(hasTestTag("crumb:1")))
        assertEquals("the slug's lines", 1, slug.lineCount)
        assertEquals("the slug's shown characters", SLUG.length, slug.getLineEnd(0, visibleEnd = true))
        val own = layout(hasTestTag("crumb:2"))
        assertEquals("the page's own crumb's lines", 1, own.lineCount)
        assertEquals("the page's own crumb shows whole", page.length, own.getLineEnd(0, visibleEnd = true))
        assertAllShown()
    }

    @Test
    fun aSlugWiderThanThePhoneEndsInAnEllipsisOnOneLine() {
        val wide = List(4) { SLUG }.joinToString("-")
        setRow(listOf(Crumb("orrery") {}, Crumb(wide)))

        val slug = layout(hasTestTag("crumb:1"))
        assertEquals("the slug's lines", 1, slug.lineCount)
        assertTrue("the slug ends in an ellipsis", slug.isLineEllipsized(0))
        val shown = slug.getLineEnd(0, visibleEnd = true)
        assertTrue("the slug shows $shown of ${wide.length} characters", shown >= SLUG.length)
        assertTrue("the slug ends inside the screen", right(hasTestTag("crumb:1")) <= rootWidth())
        assertAllShown()
    }

    private fun setRow(crumbs: List<Crumb>) {
        composeRule.setContent {
            OrreryTheme {
                TitleRow(crumbs = crumbs, onBack = {}, state = STATE, blocker = BLOCKER) {
                    ACTIONS.forEach { action ->
                        TextButton(onClick = {}, modifier = Modifier.testTag("action:$action")) {
                            Text(if (action == "stop") "Stop…" else action.replaceFirstChar { it.uppercase() })
                        }
                    }
                }
            }
        }
        composeRule.waitForIdle()
    }

    /** The state, the blocker and every action lie inside the screen's width. */
    private fun assertAllShown() {
        val right = rootWidth()
        (listOf("title-state", "title-blocker") + ACTIONS.map { "action:$it" }).forEach { tag ->
            val bounds = composeRule.onNode(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            assertTrue("$tag ends inside the screen", bounds.right <= right)
            assertTrue("$tag has width", bounds.width > 0f)
        }
    }

    private fun right(matcher: SemanticsMatcher): Float =
        composeRule.onNode(matcher, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot.right

    private fun rootWidth(): Float = composeRule.onRoot().fetchSemanticsNode().boundsInRoot.width

    private fun layout(matcher: SemanticsMatcher): TextLayoutResult {
        val node = composeRule.onNode(matcher, useUnmergedTree = true).fetchSemanticsNode()
        val layouts = mutableListOf<TextLayoutResult>()
        checkNotNull(node.config.getOrNull(SemanticsActions.GetTextLayoutResult)?.action) {
            "no text layout on that node"
        }.invoke(layouts)
        return layouts.single()
    }
}
