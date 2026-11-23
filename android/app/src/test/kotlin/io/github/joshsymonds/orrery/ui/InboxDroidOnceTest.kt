package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
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
 * Invented panes and epics in one directory, read through serve. A conversation in the tree has two ended epics that
 * steer it: they fold under its row, and draw once the fold opens. A conversation behind the ended line has one ended
 * epic, which hangs under it there. An archived conversation draws only in the Archived view. No conversation key draws
 * twice in either view.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxDroidOnceTest {
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
        const val FOLDED_PANE = "%1"
        const val BEHIND_PANE = "%2"
        const val ARCHIVED_PANE = "%3"
        const val FOLDED_ID = "10000000000000000000000000000001"
        const val BEHIND_ID = "10000000000000000000000000000002"
        const val ARCHIVED_ID = "10000000000000000000000000000003"
        const val NEWER_EPIC = "lighthouse/newer-export"
        const val OLDER_EPIC = "lighthouse/older-sweep"
        const val BEHIND_EPIC = "lighthouse/behind-cleanup"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun keyOf(paneId: String): String = "pane:$HOST:$paneId"

    private fun convoTag(key: String): String = "convo:$key"

    private fun foldTag(key: String): String = "fold:$key"

    private fun hungTag(epicId: String): String = "hung:epic:$epicId"

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

    /** An epic that has ended, steering the conversation with [steering]. */
    private fun endedEpic(id: String, steering: String, changed: Long): Epic = Epic.newBuilder()
        .setId(id)
        .setRepository("lighthouse")
        .setSlug(id.substringAfter('/'))
        .setOutcome("released")
        .setChanged(changed)
        .setLive(false)
        .setEndedSteps(0)
        .setProblem("")
        .setSteering(steering)
        .build()

    /** Serves the host, the three panes with their marks, no headless session, and the three ended epics. */
    private fun serveRows(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(
            PANES_LIST,
            PanesListResponse.newBuilder()
                .addPanes(tmuxPane(FOLDED_PANE, FOLDED_ID, "working"))
                .addPanes(tmuxPane(BEHIND_PANE, BEHIND_ID, "done"))
                .addPanes(tmuxPane(ARCHIVED_PANE, ARCHIVED_ID, "idle"))
                .build(),
        )
        server.respond(
            SESSIONS_LIST,
            ListResponse.newBuilder()
                .addSessions(tmuxSession(FOLDED_ID))
                .addSessions(tmuxSession(BEHIND_ID, ended = true))
                .addSessions(tmuxSession(ARCHIVED_ID, archived = true))
                .build(),
        )
        server.respond(LIST_HEADLESS, ListHeadlessResponse.getDefaultInstance())
        server.respond(
            EPICS_LIST,
            EpicsListResponse.newBuilder()
                .addEpics(endedEpic(NEWER_EPIC, FOLDED_ID, changed = NOW - 120))
                .addEpics(endedEpic(OLDER_EPIC, FOLDED_ID, changed = NOW - 600))
                .addEpics(endedEpic(BEHIND_EPIC, BEHIND_ID, changed = NOW - 300))
                .build(),
        )
    }

    /** Serves the host as the rows' host, so the panes of one directory share a card. */
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

    /** The conversation rows the screen draws, by their tags. */
    private fun drawnConvoTags(): List<String> = composeRule.onAllNodes(
        SemanticsMatcher("a conversation row") { node ->
            node.config.getOrNull(SemanticsProperties.TestTag)?.startsWith("convo:") == true
        },
        useUnmergedTree = true,
    ).fetchSemanticsNodes().map { it.config[SemanticsProperties.TestTag] }

    /** Exactly the [expected] conversation rows are drawn, each once: a key drawn twice shows up twice here. */
    private fun assertDrawnOnce(expected: List<String>) {
        assertEquals("each conversation draws once", expected.sorted(), drawnConvoTags().sorted())
    }

    @Test
    fun theInboxDrawsEachConversationOnceAndFoldsItsEndedEpicsUnderItsRow() {
        FakeOrreryServer().use { server ->
            serveRows(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag(keyOf(FOLDED_PANE))))
            awaitNode(hasTestTag(ENDED_TAG))

            // The tree's conversation draws once, and the one behind the ended line does not until the line opens.
            assertDrawnOnce(listOf(convoTag(keyOf(FOLDED_PANE))))

            // Its two ended epics fold under its own row, shut, under their count.
            assertEquals(1, countNodes(hasText("2 ended")))
            composeRule.onNode(hasTestTag(foldTag(keyOf(FOLDED_PANE))), useUnmergedTree = true)
                .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "shut"))
            assertAbsent(hungTag(NEWER_EPIC))
            assertAbsent(hungTag(OLDER_EPIC))
            assertTrue(
                "the fold line hangs under its row",
                nodeTop(hasTestTag(convoTag(keyOf(FOLDED_PANE)))) < nodeTop(hasTestTag(foldTag(keyOf(FOLDED_PANE)))),
            )
            assertTrue(
                "the fold line is above the ended line",
                nodeTop(hasTestTag(foldTag(keyOf(FOLDED_PANE)))) < nodeTop(hasTestTag(ENDED_TAG)),
            )

            composeRule.onNodeWithTag(foldTag(keyOf(FOLDED_PANE)), useUnmergedTree = true).performClick()
            awaitNode(hasTestTag(hungTag(NEWER_EPIC)))

            // Open, each epic draws once in the tree, above the ended line, the newer one first.
            assertEquals(1, countNodes(hasTestTag(hungTag(NEWER_EPIC)) and hasAnyAncestor(hasTestTag(PROJECT_TAG))))
            assertEquals(1, countNodes(hasTestTag(hungTag(OLDER_EPIC)) and hasAnyAncestor(hasTestTag(PROJECT_TAG))))
            assertTrue(
                "the newer ended epic comes first",
                nodeTop(hasTestTag(hungTag(NEWER_EPIC))) < nodeTop(hasTestTag(hungTag(OLDER_EPIC))),
            )
            assertTrue(
                "the folded epics are above the ended line",
                nodeTop(hasTestTag(hungTag(OLDER_EPIC))) < nodeTop(hasTestTag(ENDED_TAG)),
            )
            composeRule.onNode(hasTestTag(foldTag(keyOf(FOLDED_PANE))), useUnmergedTree = true)
                .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "open"))
            assertDrawnOnce(listOf(convoTag(keyOf(FOLDED_PANE))))

            // Opening the ended line draws the conversation behind it, with its own ended epic, and the tree's
            // conversation still draws once.
            composeRule.onNodeWithTag(ENDED_TAG).performClick()
            awaitNode(hasTestTag(convoTag(keyOf(BEHIND_PANE))))
            assertDrawnOnce(listOf(convoTag(keyOf(FOLDED_PANE)), convoTag(keyOf(BEHIND_PANE))))
            assertEquals(1, countNodes(hasTestTag(hungTag(BEHIND_EPIC)) and hasAnyAncestor(hasTestTag(PROJECT_TAG))))
            assertTrue(
                "the conversation behind the line keeps its ended epic behind the line",
                nodeTop(hasTestTag(hungTag(BEHIND_EPIC))) > nodeTop(hasTestTag(ENDED_TAG)),
            )
            assertEquals(1, countNodes(hasTestTag(hungTag(NEWER_EPIC))))
            assertEquals(1, countNodes(hasTestTag(hungTag(OLDER_EPIC))))
            assertEquals(1, countNodes(hasTestTag(foldTag(keyOf(FOLDED_PANE)))))
            assertEquals(1, countNodes(hasText("2 ended")))
        }
    }

    @Test
    fun theArchivedViewDrawsEachConversationOnce() {
        FakeOrreryServer().use { server ->
            serveRows(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag(keyOf(FOLDED_PANE))))

            composeRule.onNodeWithTag("open-archived").performClick()
            awaitNode(hasTestTag(convoTag(keyOf(ARCHIVED_PANE))))

            assertDrawnOnce(listOf(convoTag(keyOf(ARCHIVED_PANE))))
            assertAbsent(foldTag(keyOf(FOLDED_PANE)))

            composeRule.onNodeWithTag("back-to-inbox").performClick()
            awaitNode(hasTestTag(convoTag(keyOf(FOLDED_PANE))))

            assertDrawnOnce(listOf(convoTag(keyOf(FOLDED_PANE))))
        }
    }
}
