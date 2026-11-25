package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryMono
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The inbox draws a tmux pane only while a Claude session is bound to it or claude runs in it unbound. A plain terminal
// draws no row; an unbound pane draws the broken glyph and the line that tells its owner to restart it. Rows come from
// invented panes served by FakeOrreryServer, at a synthetic clock. Queries read the unmerged tree, since a clickable row
// merges its children.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxDroidPanesTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ALPHA = "/home/user/alpha"
        const val HOST = "example-host"
        const val BOUND = "%7"
        const val UNBOUND = "%6"
        const val TERMINAL = "%5"
        const val BOUND_LABEL = "bound-pane"
        const val UNBOUND_LABEL = "unbound-pane"
        const val TERMINAL_LABEL = "terminal-pane"
        const val BROKEN = "broken"
        const val RESTART_LINE = "Not bound to a conversation. Restart it with claude --resume."
        const val RESTART_COMMAND = "claude --resume"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun convoTag(paneId: String): String = "convo:pane:$HOST:$paneId"

    private fun tmuxPane(id: String, label: String, claudeSession: String, unbound: Boolean): PaneProto =
        PaneProto.newBuilder()
            .setId(id)
            .setHost(HOST)
            .setSession("example-session")
            .setWindow(1)
            .setPane(0)
            .setCwd(ALPHA)
            .setCommand(if (unbound || claudeSession.isNotEmpty()) "claude" else "zsh")
            .setState("idle")
            .setLabel(label)
            .setChanged(NOW - 120)
            .setSeen(NOW - 120)
            .setQuestion("")
            .setQuestionHash("")
            .setNewestReply(0)
            .setClaudeSession(claudeSession)
            .setUnbound(unbound)
            .build()

    /** A bound pane as a control, an unbound pane, and a plain terminal with no Claude session and nothing running in it. */
    private fun panesBody(): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(tmuxPane(BOUND, BOUND_LABEL, claudeSession = "synthetic-claude-session", unbound = false))
        .addPanes(tmuxPane(UNBOUND, UNBOUND_LABEL, claudeSession = "", unbound = true))
        .addPanes(tmuxPane(TERMINAL, TERMINAL_LABEL, claudeSession = "", unbound = false))
        .build()

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    @Test
    fun aTerminalWithNoClaudeSessionDrawsNoRowAndAnUnboundPaneDrawsTheBrokenGlyphAndTheRestartLine() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
            server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
            server.respond(PANES_LIST, panesBody())
            server.respond(LIST_HEADLESS, ListHeadlessResponse.getDefaultInstance())
            val client = OrreryClient(server.baseUrl)
            composeRule.setContent {
                MaterialTheme {
                    PaneListScreen(
                        client = client,
                        onOpenPane = {},
                        onOpenPaneId = {},
                        onStartedSession = {},
                        onOpenSettings = {},
                        now = { NOW },
                    )
                }
            }

            // The bound pane's row is the control: once it draws, the list has loaded and the terminal has had its chance.
            composeRule.awaitUntil { countNodes(hasTestTag(convoTag(BOUND))) > 0 }
            assertEquals(
                "a terminal with no Claude session draws no row",
                0,
                countNodes(hasTestTag(convoTag(TERMINAL))),
            )
            assertEquals(
                "a terminal with no Claude session draws no label",
                0,
                countNodes(hasText(TERMINAL_LABEL, substring = true)),
            )

            assertEquals("the unbound pane draws its row", 1, countNodes(hasTestTag(convoTag(UNBOUND))))
            assertEquals(
                "the unbound pane's row draws the broken glyph",
                1,
                countNodes(hasContentDescription(BROKEN) and hasAnyAncestor(hasTestTag(convoTag(UNBOUND)))),
            )
            assertEquals(
                "the unbound pane's row says to restart it with claude --resume",
                1,
                countNodes(hasText(RESTART_LINE) and hasAnyAncestor(hasTestTag(convoTag(UNBOUND)))),
            )

            val line = composeRule
                .onNode(hasText(RESTART_LINE) and hasAnyAncestor(hasTestTag(convoTag(UNBOUND))), useUnmergedTree = true)
                .fetchSemanticsNode()
                .config[SemanticsProperties.Text]
                .single()
            val command = line.spanStyles.single { it.item.fontFamily == OrreryMono }
            assertEquals(
                "the command is set in the mono family",
                RESTART_COMMAND,
                line.text.substring(command.start, command.end),
            )

            assertEquals("the bound pane keeps its row", 1, countNodes(hasTestTag(convoTag(BOUND))))
            assertEquals(
                "the bound pane's row does not restart it",
                0,
                countNodes(hasText(RESTART_LINE) and hasAnyAncestor(hasTestTag(convoTag(BOUND)))),
            )
        }
    }
}
