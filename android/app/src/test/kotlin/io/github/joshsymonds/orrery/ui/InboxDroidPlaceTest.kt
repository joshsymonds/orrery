package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Session as SessionProto
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Six invented rows in one directory, each placed from serve's fields. Three are tmux panes whose ended and archived marks
 * come from their Session rows, and three are headless sessions whose come from their own rows. A row serve has neither
 * ended nor archived draws in the project's tree; one serve has ended draws behind the project's ended line and not in the
 * tree; one serve has archived draws only in the Archived view and in no inbox place.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxDroidPlaceTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ALPHA = "/home/user/alpha"
        const val HOST = "example-host"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val PROJECT_TAG = "project:|$ALPHA"
        const val ENDED_TAG = "ended:|$ALPHA"
        const val TREE_TMUX_PANE = "%1"
        const val ENDED_TMUX_PANE = "%2"
        const val ARCHIVED_TMUX_PANE = "%3"
        const val TREE_TMUX_ID = "10000000000000000000000000000001"
        const val ENDED_TMUX_ID = "10000000000000000000000000000002"
        const val ARCHIVED_TMUX_ID = "10000000000000000000000000000003"
        const val TREE_HEADLESS_ID = "20000000000000000000000000000001"
        const val ENDED_HEADLESS_ID = "20000000000000000000000000000002"
        const val ARCHIVED_HEADLESS_ID = "20000000000000000000000000000003"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun paneTag(paneId: String): String = "convo:pane:$HOST:$paneId"

    private fun sessionTag(sessionId: String): String = "convo:session:$sessionId"

    private fun tmuxPane(id: String, orreryId: String, state: String): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost(HOST)
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd(ALPHA)
        .setCommand("claude")
        .setState(state)
        .setSummary("Synthetic summary.")
        .setLabel("label-$id")
        .setChanged(NOW - 60)
        .setSeen(NOW - 60)
        .setQuestion("")
        .setQuestionHash("")
        .setNewestReply(NOW - 60)
        .setOrreryId(orreryId)
        .setClaudeSession("synthetic-claude-$id")
        .build()

    /** A tmux pane's Session row: its marks, which serve sends with the id of the pane's @orrery_id. */
    private fun tmuxSession(id: String, archived: Boolean = false, ended: Boolean = false): SessionProto =
        SessionProto.newBuilder().setId(id).setArchived(archived).setEnded(ended).build()

    private fun headless(id: String, state: String, archived: Boolean = false, ended: Boolean = false): HeadlessSession =
        HeadlessSession.newBuilder()
            .setId(id)
            .setDir(ALPHA)
            .setLabel("synthetic-$id")
            .setState(state)
            .setChanged(NOW - 120)
            .setSeen(NOW - 120)
            .setSummary("Synthetic $state session.")
            .setNewestReply(NOW - 120)
            .setArchived(archived)
            .setEnded(ended)
            .build()

    /**
     * The rows, with the host and no epics. The stopped headless session that is not ended stays in the tree, so its place
     * follows serve's ended flag and not its state.
     */
    private fun serveRows(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
        server.respond(
            PANES_LIST,
            PanesListResponse.newBuilder()
                .addPanes(tmuxPane(TREE_TMUX_PANE, TREE_TMUX_ID, "working"))
                .addPanes(tmuxPane(ENDED_TMUX_PANE, ENDED_TMUX_ID, "done"))
                .addPanes(tmuxPane(ARCHIVED_TMUX_PANE, ARCHIVED_TMUX_ID, "idle"))
                .build(),
        )
        server.respond(
            SESSIONS_LIST,
            ListResponse.newBuilder()
                .addSessions(tmuxSession(TREE_TMUX_ID))
                .addSessions(tmuxSession(ENDED_TMUX_ID, ended = true))
                .addSessions(tmuxSession(ARCHIVED_TMUX_ID, archived = true))
                .build(),
        )
        server.respond(
            LIST_HEADLESS,
            ListHeadlessResponse.newBuilder()
                .addSessions(headless(TREE_HEADLESS_ID, "stopped"))
                .addSessions(headless(ENDED_HEADLESS_ID, "stopped", ended = true))
                .addSessions(headless(ARCHIVED_HEADLESS_ID, "idle", archived = true))
                .build(),
        )
    }

    /** Serves the host as the rows' host, so the panes and the headless sessions of one directory share a card. */
    private fun setScreen(server: FakeOrreryServer) {
        // Made once, outside the composition, so a recomposition does not hand the screen a client it has not read with.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
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

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countNodes(matcher) > 0 }
    }

    private fun nodeTop(matcher: SemanticsMatcher): Float =
        composeRule.onNode(matcher, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot.top

    private fun assertAbsent(tag: String) {
        assertEquals("$tag is not drawn", 0, countNodes(hasTestTag(tag)))
    }

    /** The row is drawn in its project's tree, above the ended line. */
    private fun assertInTree(tag: String) {
        assertEquals("$tag in the tree", 1, countNodes(hasTestTag(tag) and hasAnyAncestor(hasTestTag(PROJECT_TAG))))
        assertTrue("$tag above the ended line", nodeTop(hasTestTag(tag)) < nodeTop(hasTestTag(ENDED_TAG)))
    }

    /** The row is drawn in its project, below the ended line that its ended conversations open under. */
    private fun assertBehindTheEndedLine(tag: String) {
        assertEquals("$tag behind the ended line", 1, countNodes(hasTestTag(tag) and hasAnyAncestor(hasTestTag(PROJECT_TAG))))
        assertTrue("$tag below the ended line", nodeTop(hasTestTag(tag)) > nodeTop(hasTestTag(ENDED_TAG)))
    }

    @Test
    fun inboxPlacesEachRowFromServesEndedAndArchivedFlags() {
        FakeOrreryServer().use { server ->
            serveRows(server)
            setScreen(server)
            awaitNode(hasTestTag(paneTag(TREE_TMUX_PANE)))
            awaitNode(hasTestTag(ENDED_TAG))

            // Ended false and archived false: in the tree, both the tmux pane and the headless session.
            assertInTree(paneTag(TREE_TMUX_PANE))
            assertInTree(sessionTag(TREE_HEADLESS_ID))
            // Two rows are ended behind the line; the archived row counts in neither the tree nor the line.
            assertEquals(1, countNodes(hasText("2 ended")))
            assertAbsent(paneTag(ENDED_TMUX_PANE))
            assertAbsent(sessionTag(ENDED_HEADLESS_ID))
            assertAbsent(paneTag(ARCHIVED_TMUX_PANE))
            assertAbsent(sessionTag(ARCHIVED_HEADLESS_ID))

            composeRule.onNodeWithTag(ENDED_TAG).performClick()

            // Ended true: behind the line, and not in the tree; the archived rows stay out of the line when it opens.
            assertBehindTheEndedLine(paneTag(ENDED_TMUX_PANE))
            assertBehindTheEndedLine(sessionTag(ENDED_HEADLESS_ID))
            assertAbsent(paneTag(ARCHIVED_TMUX_PANE))
            assertAbsent(sessionTag(ARCHIVED_HEADLESS_ID))
        }
    }

    @Test
    fun theArchivedViewHoldsTheArchivedRowsAndNoOtherRows() {
        FakeOrreryServer().use { server ->
            serveRows(server)
            setScreen(server)
            awaitNode(hasTestTag(paneTag(TREE_TMUX_PANE)))

            composeRule.onNodeWithTag("open-archived").performClick()

            // Archived true: the tmux pane and the headless session draw here, and nothing else does.
            awaitNode(hasTestTag(paneTag(ARCHIVED_TMUX_PANE)))
            awaitNode(hasTestTag(sessionTag(ARCHIVED_HEADLESS_ID)))
            assertAbsent(paneTag(TREE_TMUX_PANE))
            assertAbsent(paneTag(ENDED_TMUX_PANE))
            assertAbsent(sessionTag(TREE_HEADLESS_ID))
            assertAbsent(sessionTag(ENDED_HEADLESS_ID))
            assertAbsent(ENDED_TAG)

            composeRule.onNodeWithTag("back-to-inbox").performClick()

            awaitNode(hasTestTag(paneTag(TREE_TMUX_PANE)))
            assertAbsent(paneTag(ARCHIVED_TMUX_PANE))
            assertAbsent(sessionTag(ARCHIVED_HEADLESS_ID))
        }
    }
}
