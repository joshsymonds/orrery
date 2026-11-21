package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListedNotice
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeBlock
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeKey
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The inbox head after round 6's phone head: the mark and the name, then Search, the Notices bell with its count, Pools,
 * Settings and New, with whatever does not fit folded into More. Native graphics, so text is measured in the app's font.
 */
@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "w412dp-h800dp")
class InboxHeadTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val HEAD = "inbox-head"
        const val UNREAD = "notices-unread"
        const val SETTINGS = "Settings"

        /** Every action of the head by its name, in round 6's order. */
        val ACTIONS = listOf("Search sessions and epics", "Notices", "Pools", SETTINGS, "New")

        /** How far, in pixels, a bound may stray from the bar for rounding. */
        const val SLACK = 0.5f
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private var settingsOpened = 0

    private fun unreadNotices(count: Int): NoticesListResponse {
        val response = NoticesListResponse.newBuilder()
        repeat(count) { index ->
            response.addNotices(
                ListedNotice.newBuilder()
                    .setKey(NoticeKey.newBuilder().setLimit("five_hour@${NOW + index}"))
                    .setState("limited")
                    .setTitle("Limit reached")
                    .setBody("")
                    .setBlock(NoticeBlock.getDefaultInstance())
                    .setTime(NOW - index)
                    .setRead(false),
            )
        }
        return response.build()
    }

    /** Draws the inbox with [unread] unread notices and waits until the bell counts them. */
    private fun setScreen(server: FakeOrreryServer, unread: Int) {
        server.respond("/orrery.v1.Host/Status", HostStatusResponse.newBuilder().setHost("example-host").build())
        server.respond("/orrery.v1.Panes/List", PanesListResponse.getDefaultInstance())
        server.respond("/orrery.v1.Sessions/ListHeadless", ListHeadlessResponse.getDefaultInstance())
        server.respond("/orrery.v1.Epics/List", EpicsListResponse.getDefaultInstance())
        server.respond("/orrery.v1.Notices/List", unreadNotices(unread))
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            PaneListScreen(
                client = client,
                onOpenPane = {},
                onOpenPaneId = {},
                onStartedSession = {},
                onOpenSettings = { settingsOpened++ },
                now = { NOW },
            )
        }
        composeRule.awaitUntil("the bell counts $unread") {
            nodes(hasTestTag(UNREAD) and hasText(unread.toString())).size == 1
        }
    }

    private fun nodes(matcher: SemanticsMatcher, unmerged: Boolean = true): List<SemanticsNode> =
        composeRule.onAllNodes(matcher, useUnmergedTree = unmerged).fetchSemanticsNodes()

    private val inBar = hasAnyAncestor(hasTestTag(HEAD))

    /** The action named [name] as drawn in the bar: an icon button by its content description, or New by its text. */
    private fun shownAction(name: String): SemanticsNode? =
        nodes((hasContentDescription(name) or hasText(name)) and hasClickAction() and inBar, unmerged = false).singleOrNull()

    private fun bar(): Rect = nodes(hasTestTag(HEAD)).single().boundsInRoot

    private fun assertInside(what: String, inner: Rect, outer: Rect) {
        assertTrue(
            "$what $inner lies inside $outer",
            inner.left >= outer.left - SLACK && inner.right <= outer.right + SLACK &&
                inner.top >= outer.top - SLACK && inner.bottom <= outer.bottom + SLACK,
        )
    }

    private fun textLayoutOf(node: SemanticsNode): TextLayoutResult {
        val layouts = mutableListOf<TextLayoutResult>()
        checkNotNull(node.config.getOrNull(SemanticsActions.GetTextLayoutResult)?.action).invoke(layouts)
        return layouts.single()
    }

    /**
     * The text of [node] lies on one line and draws whole: no break, no ellipsis, and its line within the text's own
     * box. The line's extent is measured, not hasVisualOverflow, which compares against the paragraph's layout width.
     */
    private fun assertOneWholeLine(node: SemanticsNode) {
        val layout = textLayoutOf(node)
        val text = layout.layoutInput.text.text
        assertEquals("\"$text\" is on one line", 1, layout.lineCount)
        assertFalse("\"$text\" is not cut short", layout.isLineEllipsized(0))
        assertFalse("\"$text\" is not cut at its foot", layout.didOverflowHeight)
        val line = layout.getLineRight(0) - layout.getLineLeft(0)
        assertTrue("\"$text\" draws $line wide inside its ${layout.size.width}", line <= layout.size.width + SLACK)
    }

    /**
     * Every text in the bar is one whole line inside it; each action drawn in the bar lies inside it; and each action not
     * drawn there is a whole line in the menu More opens. Answers the names folded into More.
     */
    private fun assertTheHeadFits(): List<String> {
        val bar = bar()
        nodes(SemanticsMatcher.keyIsDefined(SemanticsProperties.Text) and inBar).forEach { node ->
            assertInside("the text ${node.config[SemanticsProperties.Text]}", node.boundsInRoot, bar)
            assertOneWholeLine(node)
        }
        val folded = ACTIONS.filter { name ->
            val node = shownAction(name) ?: return@filter true
            assertInside(name, node.boundsInRoot, bar)
            false
        }
        nodes(hasTestTag(UNREAD)).single().let { badge ->
            val bell = checkNotNull(shownAction("Notices")) { "the bell is always in the bar" }
            assertInside("the bell's count", badge.boundsInRoot, bell.boundsInRoot)
        }
        if (folded.isNotEmpty()) {
            composeRule.onNode(hasContentDescription("More") and inBar).performClick()
            folded.forEach { name ->
                val item = nodes(hasText(name) and hasAnyAncestor(hasTestTag("inbox-more-menu"))).single()
                assertOneWholeLine(item)
            }
        }
        return folded
    }

    @Test
    @Config(qualifiers = "w320dp-h800dp")
    fun atThreeHundredTwentyEveryActionIsReachableWholeAndInsideTheBar() {
        FakeOrreryServer().use { server ->
            setScreen(server, unread = 20)

            val folded = assertTheHeadFits()

            assertTrue("something folds into More at 320: $folded", folded.isNotEmpty())
            assertTrue("the bell stays in the bar", "Notices" !in folded)
            assertTrue("New stays in the bar", "New" !in folded)
            composeRule.onNode(hasText(SETTINGS) and hasAnyAncestor(hasTestTag("inbox-more-menu"))).performClick()
            assertEquals(1, settingsOpened)
        }
    }

    @Test
    fun atFourHundredTwelveEveryActionSitsWholeInTheBarInRoundSixOrder() {
        FakeOrreryServer().use { server ->
            setScreen(server, unread = 20)

            val folded = assertTheHeadFits()

            assertEquals("nothing folds at 412", emptyList<String>(), folded)
            assertEquals(0, nodes(hasContentDescription("More") and inBar).size)
            val lefts = ACTIONS.map { checkNotNull(shownAction(it)).boundsInRoot.left }
            assertEquals("Search, Notices, Pools, Settings, New from left to right", lefts.sorted(), lefts)
        }
    }

    @Test
    fun theFilterChipsAreAControlHighWithTheirTextCentred() {
        composeRule.setContent {
            Column { InboxChips(Counts(waiting = 3, fresh = 5), Narrow.NONE, {}) }
        }
        val controlHigh = with(composeRule.density) { OrreryTokens.Size.controlDesk.toPx() }

        for ((tag, words) in listOf("chip-waiting" to "3 wait on you", "chip-new" to "5 new")) {
            val chip = nodes(hasTestTag(tag), unmerged = false).single().boundsInRoot
            val text = nodes(hasText(words)).single()
            val textBounds = text.boundsInRoot

            assertEquals("$tag is a control high", controlHigh, chip.height, SLACK)
            assertEquals("$tag's text is as high as its line", textLayoutOf(text).size.height.toFloat(), textBounds.height, SLACK)
            assertEquals("$tag's text is centred top to bottom", chip.center.y, textBounds.center.y, SLACK)
            assertInside("$tag's text", textBounds, chip)
        }
    }
}
