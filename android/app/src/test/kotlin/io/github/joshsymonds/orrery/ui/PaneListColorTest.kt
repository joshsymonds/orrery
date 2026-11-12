package io.github.joshsymonds.orrery.ui

import android.graphics.Bitmap
import android.graphics.Canvas
import androidx.activity.ComponentActivity
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.text.TextLayoutResult
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.ui.theme.OrreryTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class PaneListColorTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val TIMEOUT_MS = 5_000L
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val MAUVE_ARGB = 0xFFCBA6F7.toInt()
        val YELLOW = Color(0xFFF9E2AF)
        val PEACH = Color(0xFFFAB387)
        val GREEN = Color(0xFFA6E3A1)
    }

    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

    private fun pane(
        id: String,
        label: String,
        state: String,
        changed: Long,
        seen: Long,
        newestReply: Long,
    ): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost("example-host")
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd("/home/user/alpha")
        .setCommand("claude")
        .setState(state)
        .setSummary("Synthetic summary.")
        .setLabel(label)
        .setChanged(changed)
        .setSeen(seen)
        .setQuestion("")
        .setQuestionHash("")
        .setNewestReply(newestReply)
        .build()

    private fun panesBody(): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(pane("%1", "working-pane", "working", NOW - 30, NOW - 300, NOW - 30))
        .addPanes(pane("%2", "waiting-pane", "waiting", NOW - 300, NOW - 600, NOW - 300))
        .addPanes(pane("%3", "done-pane", "done", NOW - 7200, NOW - 7200, NOW - 7200))
        .addPanes(pane("%4", "idle-pane", "idle", 0, 0, 0))
        .build()

    private fun setScreen(server: FakeOrreryServer) {
        composeRule.setContent {
            OrreryTheme {
                PaneListScreen(
                    client = OrreryClient(server.baseUrl),
                    onOpenPane = {},
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                    now = { NOW },
                )
            }
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitRow(id: String) {
        composeRule.waitUntil(TIMEOUT_MS) { countNodes(hasTestTag("pane-row:$id")) == 1 }
    }

    private fun renderedColor(node: SemanticsNode): Color {
        val layouts = mutableListOf<TextLayoutResult>()
        val action = node.config[SemanticsActions.GetTextLayoutResult].action
        assertTrue("text layout action", action?.invoke(layouts) == true)
        return layouts.single().layoutInput.style.color
    }

    private fun stateWordColor(id: String, word: String): Color = renderedColor(
        composeRule.onNode(
            hasText(word) and hasAnyAncestor(hasTestTag("pane-row:$id")),
            useUnmergedTree = true,
        ).fetchSemanticsNode(),
    )

    private fun labelColor(id: String, label: String): Color = renderedColor(
        composeRule.onNode(
            hasText(label) and hasAnyAncestor(hasTestTag("pane-row:$id")),
            useUnmergedTree = true,
        ).fetchSemanticsNode(),
    )

    @Test
    fun stateWordsRenderTheirMochaColours() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            assertEquals("working", YELLOW, stateWordColor("%1", "working"))
            assertEquals("waiting", PEACH, stateWordColor("%2", "waiting"))
            assertEquals("done", GREEN, stateWordColor("%3", "done"))
        }
    }

    @Test
    fun otherStateWordKeepsDefaultTextColour() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%4")

            assertEquals(labelColor("%4", "idle-pane"), stateWordColor("%4", "idle"))
        }
    }

    @Test
    fun unseenDotRendersMauve() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            val bounds = composeRule.onNode(
                hasContentDescription("unseen") and hasAnyAncestor(hasTestTag("pane-row:%1")),
                useUnmergedTree = true,
            ).fetchSemanticsNode().boundsInWindow
            val decor = composeRule.activity.window.decorView
            val bitmap = Bitmap.createBitmap(decor.width, decor.height, Bitmap.Config.ARGB_8888)
            decor.draw(Canvas(bitmap))

            val x = bounds.center.x.toInt()
            val y = bounds.center.y.toInt()
            assertEquals(MAUVE_ARGB, bitmap.getPixel(x, y))
        }
    }
}
