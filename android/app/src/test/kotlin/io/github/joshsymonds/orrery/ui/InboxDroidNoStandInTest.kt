package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Session as SessionProto
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Invented panes, epics and contracts in one project. An epic and a proposed contract whose conversation is not listed
 * each draw on their own row in the project, and an epic whose conversation is archived does too. No row in the inbox or
 * the Archived view reads as a stand-in: no text says its conversation is not listed or archived, and no node carries a
 * standin tag. Every tmux pane has a Claude session bound to it.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxDroidNoStandInTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ALPHA = "/home/user/alpha"
        const val ALPHA_KEY = "|$ALPHA"
        const val PROJECT_TAG = "project:$ALPHA_KEY"
        const val HOST = "example-host"
        const val REPOSITORY = "example-repo"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val CONTRACTS_LIST = "/orrery.v1.Contracts/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val LISTED_PANE = "%1"
        const val ARCHIVED_PANE = "%2"
        const val LISTED_ID = "10000000000000000000000000000001"
        const val ARCHIVED_ID = "10000000000000000000000000000002"
        const val UNLISTED_ID = "10000000000000000000000000000099"
        const val UNLISTED_EPIC = "$REPOSITORY/unlisted-epic"
        const val UNLISTED_CONTRACT = "$REPOSITORY/unlisted-contract"
        const val ARCHIVED_CONVERSATION_EPIC = "$REPOSITORY/archived-conversation-epic"
        const val ARCHIVED_EPIC = "$REPOSITORY/archived-epic"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

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

    private fun epic(id: String, steering: String, archived: Boolean = false): Epic = Epic.newBuilder()
        .setId(id)
        .setRepository(REPOSITORY)
        .setPath(ALPHA)
        .setSlug(id.substringAfter('/'))
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .setArchived(archived)
        .setEndedSteps(0)
        .setSteering(steering)
        .build()

    /** A contract proposed in [ALPHA] by the conversation [conversation] names as the one that wrote it. */
    private fun proposed(id: String, conversation: String): Contract = Contract.newBuilder()
        .setId(id)
        .setRepository(ALPHA)
        .setTitle("Synthetic contract title")
        .setCare("serious")
        .setState("proposed")
        .setVersion(1L)
        .setOpenDecisions(0L)
        .setConversation(conversation)
        .setCreated(NOW - 600)
        .setChanged(NOW - 600)
        .build()

    /**
     * Serves one listed conversation, %1, and one archived conversation, %2, in [ALPHA], with the epics given. The
     * contract steered by the unlisted conversation is proposed, and each epic's detail answers its own read.
     */
    private fun serveRows(server: FakeOrreryServer, epics: List<Epic>) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(
            PANES_LIST,
            PanesListResponse.newBuilder()
                .addPanes(tmuxPane(LISTED_PANE, LISTED_ID, "working"))
                .addPanes(tmuxPane(ARCHIVED_PANE, ARCHIVED_ID, "idle"))
                .build(),
        )
        server.respond(
            SESSIONS_LIST,
            ListResponse.newBuilder()
                .addSessions(SessionProto.newBuilder().setId(ARCHIVED_ID).setArchived(true))
                .build(),
        )
        server.respond(LIST_HEADLESS, ListHeadlessResponse.newBuilder().build())
        server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addAllEpics(epics).build())
        val byId = epics.associateBy { it.id }
        server.route(EPICS_GET) { request ->
            val epic = byId.getValue(EpicsGetRequest.parseFrom(request.body).id)
            Reply.Message(EpicsGetResponse.newBuilder().setEpic(epic).build(), 0L)
        }
        server.respond(
            CONTRACTS_LIST,
            ContractsListResponse.newBuilder().addContracts(proposed(UNLISTED_CONTRACT, UNLISTED_ID)).build(),
        )
    }

    private fun serveDefaultRows(server: FakeOrreryServer) = serveRows(
        server,
        listOf(
            epic(UNLISTED_EPIC, steering = UNLISTED_ID),
            epic(ARCHIVED_CONVERSATION_EPIC, steering = ARCHIVED_ID),
            epic(ARCHIVED_EPIC, steering = LISTED_ID, archived = true),
        ),
    )

    /** Serves the host as the rows' host, so the panes and the epics of one directory share a project. */
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

    private fun hungTag(key: String): String = "hung:$key"

    private fun convoTag(paneId: String): String = "convo:pane:$HOST:$paneId"

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countNodes(matcher) > 0 }
    }

    /** The row is drawn once, in the project's tree. */
    private fun assertDrawnInProject(tag: String) {
        assertEquals("$tag in the project", 1, countNodes(hasTestTag(tag) and hasAnyAncestor(hasTestTag(PROJECT_TAG))))
    }

    /** No node says its conversation is not listed or archived, and no node carries a standin tag. */
    private fun assertNoStandIn() {
        assertEquals(
            "no node says its conversation is not listed",
            0,
            countNodes(hasText("not listed", substring = true, ignoreCase = true)),
        )
        assertEquals(
            "no node says its conversation is archived",
            0,
            countNodes(hasText("conversation is archived", substring = true, ignoreCase = true)),
        )
        assertEquals(
            "no node carries a standin tag",
            0,
            countNodes(
                SemanticsMatcher("a standin tag") { node ->
                    node.config.getOrNull(SemanticsProperties.TestTag)?.startsWith("standin:") == true
                },
            ),
        )
    }

    @Test
    fun inboxDroidNoStandInDrawsAnEpicAndAContractWhoseConversationIsNotListedAsTheirOwnRows() {
        FakeOrreryServer().use { server ->
            serveDefaultRows(server)
            setScreen(server)
            awaitNode(hasTestTag(hungTag("contract:$UNLISTED_CONTRACT")))

            assertDrawnInProject(hungTag("epic:$UNLISTED_EPIC"))
            assertDrawnInProject(hungTag("contract:$UNLISTED_CONTRACT"))
            assertDrawnInProject(hungTag("epic:$ARCHIVED_CONVERSATION_EPIC"))
            // The archived epic is not drawn in the inbox at all.
            assertEquals(0, countNodes(hasTestTag(hungTag("epic:$ARCHIVED_EPIC"))))
            assertNoStandIn()
        }
    }

    @Test
    fun inboxDroidNoStandInArchivedViewDrawsAnArchivedEpicOnItsOwnRowAndNoStandIn() {
        FakeOrreryServer().use { server ->
            serveDefaultRows(server)
            setScreen(server)
            awaitNode(hasTestTag(hungTag("contract:$UNLISTED_CONTRACT")))

            composeRule.onNodeWithTag("open-archived").performClick()

            awaitNode(hasTestTag(hungTag("epic:$ARCHIVED_EPIC")))
            assertDrawnInProject(hungTag("epic:$ARCHIVED_EPIC"))
            // The listed conversation is not archived, so the archived view does not draw it as the epic's parent.
            assertEquals(0, countNodes(hasTestTag(convoTag(LISTED_PANE))))
            assertNoStandIn()
        }
    }
}
