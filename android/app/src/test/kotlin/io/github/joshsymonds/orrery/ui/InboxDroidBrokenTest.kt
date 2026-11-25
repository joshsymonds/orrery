package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The inbox draws a failed conversation and an unbound pane with the broken glyph, and a failed conversation's page shows
// the reason the inbox passes it. Rows come from invented panes and sessions served by FakeOrreryServer, at a synthetic
// clock. Queries read the unmerged tree, since a clickable row merges its children.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxDroidBrokenTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ALPHA = "/home/user/alpha"
        const val HOST = "example-host"
        const val HEADLESS_ID = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
        const val HEADLESS_LABEL = "alpha-agent · a1b2c3d4"
        const val REASON = "synthetic crash: the worker ran out of memory"
        const val BOUND = "%3"
        const val UNBOUND = "%4"
        const val BROKEN = "broken"
        const val FAILED = "failed"
        const val WORKING = "working"
        const val DOCK_STATE = "dock-state"
        const val DOCK_WORD = "dock-word"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun convoTag(paneId: String): String = "convo:pane:$HOST:$paneId"

    private fun sessionTag(sessionId: String): String = "convo:session:$sessionId"

    private fun tmuxPane(id: String, label: String, state: String, claudeSession: String, unbound: Boolean): PaneProto =
        PaneProto.newBuilder()
            .setId(id)
            .setHost(HOST)
            .setSession("example-session")
            .setWindow(1)
            .setPane(0)
            .setCwd(ALPHA)
            .setCommand("claude")
            .setState(state)
            .setLabel(label)
            .setChanged(NOW - 120)
            .setSeen(NOW)
            .setQuestion("")
            .setQuestionHash("")
            .setNewestReply(0)
            .setClaudeSession(claudeSession)
            .setUnbound(unbound)
            .build()

    /** A bound pane that is working, and an unbound pane: claude runs in it with no Claude session bound. */
    private fun panesBody(): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(tmuxPane(BOUND, "bound-pane", WORKING, claudeSession = "synthetic-claude-session", unbound = false))
        .addPanes(tmuxPane(UNBOUND, "unbound-pane", "idle", claudeSession = "", unbound = true))
        .build()

    /** A failed headless session, unread since its last reply, so it stays in the tree rather than behind the ended line. */
    private fun failedHeadlessBody(): ListHeadlessResponse = ListHeadlessResponse.newBuilder()
        .addSessions(
            HeadlessSession.newBuilder()
                .setId(HEADLESS_ID)
                .setDir(ALPHA)
                .setLabel(HEADLESS_LABEL)
                .setState("failed")
                .setChanged(NOW - 3_600)
                .setSeen(NOW - 7_200)
                .setSummary(REASON)
                .setNewestReply(NOW - 3_600),
        )
        .build()

    /** The failed session's stream: its state, as the session's page reads it. */
    private fun failedStream(): Reply.Stream {
        val state = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(HEADLESS_ID)
                    .setDir(ALPHA)
                    .setLabel(HEADLESS_LABEL)
                    .setState("failed")
                    .setChanged(NOW - 3_600)
                    .setSeen(NOW - 7_200),
            )
            .build()
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(HEADLESS_ID)
            .setSeq(1L)
            .setState(state)
            .build()
        return Reply.Stream(listOf(delta), StreamEnd.Open)
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countNodes(matcher) > 0 }
    }

    @Test
    fun aFailedConversationAndAnUnboundPaneDrawTheBrokenIconAndAFailedPageShowsItsReason() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
            server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
            server.respond(PANES_LIST, panesBody())
            server.respond(LIST_HEADLESS, failedHeadlessBody())
            server.route(WATCH_PATH) { failedStream() }
            val client = OrreryClient(server.baseUrl)
            composeRule.setContent {
                var opened by remember { mutableStateOf<Pane?>(null) }
                MaterialTheme {
                    val pane = opened
                    if (pane == null) {
                        PaneListScreen(
                            client = client,
                            onOpenPane = { opened = it },
                            onOpenPaneId = {},
                            onStartedSession = {},
                            onOpenSettings = {},
                            now = { NOW },
                        )
                    } else {
                        // The inbox opens a headless conversation with its summary as the reason, as MainActivity does.
                        SessionScreen(sessionId = pane.id, client = client, reason = pane.summary, clock = { NOW })
                    }
                }
            }

            awaitNode(hasTestTag(sessionTag(HEADLESS_ID)))
            awaitNode(hasTestTag(convoTag(UNBOUND)))
            assertEquals(
                "the failed conversation's row draws the broken glyph",
                1,
                countNodes(hasContentDescription(BROKEN) and hasAnyAncestor(hasTestTag(sessionTag(HEADLESS_ID)))),
            )
            assertEquals(
                "the unbound pane's row draws the broken glyph",
                1,
                countNodes(hasContentDescription(BROKEN) and hasAnyAncestor(hasTestTag(convoTag(UNBOUND)))),
            )
            assertEquals("only those two rows draw the broken glyph", 2, countNodes(hasContentDescription(BROKEN)))
            assertEquals(
                "the failed conversation draws the broken glyph, not the failed one",
                0,
                countNodes(hasContentDescription(FAILED)),
            )
            assertEquals(
                "the bound working pane keeps its own glyph",
                1,
                countNodes(hasContentDescription(WORKING) and hasAnyAncestor(hasTestTag(convoTag(BOUND)))),
            )

            composeRule.onNodeWithTag(sessionTag(HEADLESS_ID)).performClick()

            composeRule.awaitUntil {
                countNodes(hasTestTag(DOCK_STATE) and hasAnyDescendant(hasText("failed"))) == 1
            }
            assertEquals(1, countNodes(hasTestTag(DOCK_WORD) and hasText("failed")))
            assertEquals(
                "the failed page shows its crash reason",
                1,
                countNodes(hasTestTag(DOCK_STATE) and hasAnyDescendant(hasText(REASON, substring = true))),
            )
        }
    }
}
