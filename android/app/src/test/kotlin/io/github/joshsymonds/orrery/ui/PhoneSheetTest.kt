package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

// The round 8 phone sheet a work link opens over a conversation: the design, contract and epic each open from their link
// under the head, the lit link closes it, "+N" lists every design and loose epic, and a design's kid switches the sheet.
// Read through the semantics tree alone, with the conversation's records invented here at a fixed clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class PhoneSheetTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val SESSION_ID = "synthetic-session"
        const val PANE_ID = "%7"
        const val ORRERY_ID = "synthetic-orrery-id"
        const val REPOSITORY = "/home/user/lighthouse"
        const val DESIGN_ID = "synthetic-design"
        const val DESIGN_TITLE = "Synthetic design"
        const val DESIGN_STATE = "0 of 1 accepted"
        const val LAMP_ID = "synthetic-lamp"
        const val CONTRACT_ID = "synthetic-owner/synthetic-contract"
        const val CONTRACT_NAME = "synthetic-contract"
        const val EPIC_ID = "synthetic-owner/synthetic-epic"
        const val EPIC_SLUG = "synthetic-epic"
        const val LOOSE_ID = "synthetic-owner/loose-epic"
        const val LOOSE_SLUG = "loose-epic"
        const val DESIGN_LINK_TAG = "work-link:design:$DESIGN_ID"
        const val EPIC_LINK_TAG = "work-link:epic:$EPIC_ID"
        const val CONTRACT_LINK_TAG = "work-link:contract:$CONTRACT_ID"
        const val LOOSE_LINK_TAG = "work-link:epic:$LOOSE_ID"
        const val ALL_LINK_TAG = "work-link-all"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val PANE_WATCH_PATH = "/orrery.v1.Panes/WatchConversation"
        const val PANES_PATH = "/orrery.v1.Panes/List"
        const val DESIGNS_PATH = "/orrery.v1.Designs/List"
        const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
        const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY_PATH = "/orrery.v1.Epics/History"
        const val CONTRACTS_PATH = "/orrery.v1.Contracts/List"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun setSession(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client, clock = { NOW }) }
        }
    }

    private fun setPane(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client, clock = { NOW }) }
        }
    }

    /** Streams the headless session [SESSION_ID], which has nothing running. */
    private fun serveSession(server: FakeOrreryServer) {
        val session = HeadlessSession.newBuilder()
            .setId(SESSION_ID)
            .setDir(REPOSITORY)
            .setLabel("synthetic session")
            .setState("working")
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        val state = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setSession(session)
            .build()
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .setState(state)
            .build()
        server.route(WATCH_PATH) { Reply.Stream(listOf(delta), StreamEnd.Open) }
    }

    /** Serves the pane [PANE_ID] with the orrery id [ORRERY_ID], and no stream, so its screen polls. */
    private fun servePane(server: FakeOrreryServer) {
        server.route(PANE_WATCH_PATH) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
        val pane = PaneProto.newBuilder()
            .setId(PANE_ID)
            .setOrreryId(ORRERY_ID)
            .setHost("example-host")
            .setCwd(REPOSITORY)
            .setCommand("claude")
            .setState("waiting")
            .setLabel("synthetic pane")
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        server.respond(PANES_PATH, PanesListResponse.newBuilder().addPanes(pane).build())
    }

    /** The designs, epics and contracts the conversation's head reads: [designs] and [epics] as the server lists them. */
    private fun serveWork(server: FakeOrreryServer, designs: List<Design>, epics: List<Epic>) {
        server.respond(DESIGNS_PATH, DesignsListResponse.newBuilder().addAllDesigns(designs).build())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addAllEpics(epics).build())
        server.respond(CONTRACTS_PATH, ContractsListResponse.getDefaultInstance())
    }

    /** Serves [epic] as its detail reads it, with no history, so its screen draws the epic's summary. */
    private fun serveEpicDetail(server: FakeOrreryServer, epic: Epic) {
        server.respond(EPICS_GET_PATH, EpicsGetResponse.newBuilder().setEpic(epic).setRepository(REPOSITORY).build())
        server.respond(EPICS_HISTORY_PATH, EpicsHistoryResponse.getDefaultInstance())
    }

    private fun design(
        id: String,
        title: String,
        conversation: String,
        ofOne: Boolean = false,
        contracts: List<DesignContract> = emptyList(),
    ): Design = Design.newBuilder()
        .setId(id)
        .setTitle(title)
        .setConversation(conversation)
        .setCreated(NOW - 600)
        .setOfOne(ofOne)
        .setState(DESIGN_STATE)
        .addAllContracts(contracts)
        .build()

    private fun designContract(id: String, epic: String = ""): DesignContract = DesignContract.newBuilder()
        .setId(id)
        .setTitle("Synthetic contract")
        .setRepository("synthetic-owner")
        .setState("proposed")
        .setPosition(1L)
        .setEpic(epic)
        .build()

    /** An epic [steering] names as its conversation, live, with the id [id] and the slug [slug]. */
    private fun epic(id: String, slug: String, steering: String): Epic = Epic.newBuilder()
        .setId(id)
        .setRepository(REPOSITORY)
        .setSlug(slug)
        .setSource("orreryd")
        .setOutcome("accepted")
        .setChanged(NOW - 30)
        .setLive(true)
        .setEndedSteps(0)
        .setSteering(steering)
        .build()

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil("the node tagged $tag") {
            composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    /** The phone sheet is drawn under the head's title row: its top is at or below the title row's bottom. */
    private fun assertSheetUnderHeadTitle() {
        composeRule.onNodeWithTag("head-title", useUnmergedTree = true).assertExists()
        val head = composeRule.onNodeWithTag("head-title", useUnmergedTree = true).fetchSemanticsNode()
        val sheet = composeRule.onNodeWithTag(PHONE_SHEET_TAG, useUnmergedTree = true).fetchSemanticsNode()
        assertTrue(
            "the sheet begins under the head's title row",
            sheet.boundsInRoot.top >= head.boundsInRoot.bottom,
        )
    }

    /** The node tagged [body] holds a node tagged [tag] somewhere under it. */
    private fun assertBodyHolds(body: String, tag: String) {
        composeRule.onNode(hasTestTag(body) and hasAnyDescendant(hasTestTag(tag)), useUnmergedTree = true).assertExists()
    }

    private fun assertDesignSheetUnderHead() {
        awaitTag(PHONE_SHEET_TAG)
        assertSheetUnderHeadTitle()
        composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals(DESIGN_TITLE)
        composeRule.onNodeWithTag(SHEET_KIND_TAG, useUnmergedTree = true).assertTextEquals("working on")
        composeRule.onNodeWithTag(SHEET_STATE_TAG, useUnmergedTree = true).assertTextEquals("design · $DESIGN_STATE")
        assertBodyHolds("sheet-body:design:$DESIGN_ID", "design-tab:$DESIGN_ID")
    }

    @Test
    fun phoneSheetOpensADesignUnderTheHeadFromItsLinkInASession() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, SESSION_ID)), epics = emptyList())
            setSession(server)
            awaitTag(DESIGN_LINK_TAG)

            composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()

            assertDesignSheetUnderHead()
        }
    }

    @Test
    fun phoneSheetOpensADesignUnderTheHeadFromItsLinkInAPane() {
        FakeOrreryServer().use { server ->
            servePane(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, ORRERY_ID)), epics = emptyList())
            setPane(server)
            awaitTag(DESIGN_LINK_TAG)

            composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()

            assertDesignSheetUnderHead()
        }
    }

    @Test
    fun phoneSheetOpensAnEpicFromItsLinkWithoutItsTitleRow() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            val epicRow = epic(EPIC_ID, EPIC_SLUG, steering = SESSION_ID)
            serveWork(server, designs = emptyList(), epics = listOf(epicRow))
            serveEpicDetail(server, epicRow)
            setSession(server)
            awaitTag(EPIC_LINK_TAG)

            composeRule.onNodeWithTag(EPIC_LINK_TAG).performClick()

            awaitTag(PHONE_SHEET_TAG)
            assertSheetUnderHeadTitle()
            composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals(EPIC_SLUG)
            awaitTag("epic-section:now")
            assertBodyHolds("sheet-body:epic:$EPIC_ID", "epic-section:now")
            composeRule.onAllNodesWithTag("title-back", useUnmergedTree = true).assertCountEquals(0)
        }
    }

    @Test
    fun phoneSheetOpensAContractFromItsLinkWithItsContractScreen() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            val lamp = design(LAMP_ID, "Synthetic lamp", SESSION_ID, ofOne = true, contracts = listOf(designContract(CONTRACT_ID)))
            serveWork(server, designs = listOf(lamp), epics = emptyList())
            setSession(server)
            awaitTag(CONTRACT_LINK_TAG)

            composeRule.onNodeWithTag(CONTRACT_LINK_TAG).performClick()

            awaitTag(PHONE_SHEET_TAG)
            assertSheetUnderHeadTitle()
            composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals(CONTRACT_NAME)
            assertBodyHolds("sheet-body:contract:$CONTRACT_ID", "contract-screen")
            composeRule.onAllNodesWithTag("title-back", useUnmergedTree = true).assertCountEquals(0)
        }
    }

    @Test
    fun phoneSheetClosesWhenItsLitLinkIsTappedAgain() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, SESSION_ID)), epics = emptyList())
            setSession(server)
            awaitTag(DESIGN_LINK_TAG)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()
            awaitTag(PHONE_SHEET_TAG)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertIsSelected()

            composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()

            composeRule.onAllNodesWithTag(PHONE_SHEET_TAG, useUnmergedTree = true).assertCountEquals(0)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertIsNotSelected()
        }
    }

    @Test
    fun phoneSheetPlusNOpensTheListOfAllAndAnEpicRowSwitchesTheSheetToIt() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            val looseRow = epic(LOOSE_ID, LOOSE_SLUG, steering = SESSION_ID)
            serveWork(server, designs = listOf(design(DESIGN_ID, DESIGN_TITLE, SESSION_ID)), epics = listOf(looseRow))
            serveEpicDetail(server, looseRow)
            setSession(server)
            awaitTag(ALL_LINK_TAG)

            composeRule.onNodeWithTag(ALL_LINK_TAG).performClick()

            awaitTag(PHONE_SHEET_TAG)
            composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals("2 things")
            composeRule.onNodeWithTag(ALL_LINK_TAG).assertIsSelected()
            assertBodyHolds("sheet-body:all", "design-tab:$DESIGN_ID")

            composeRule.onNodeWithTag("hung:epic:$LOOSE_ID", useUnmergedTree = true).performScrollTo().performClick()

            awaitTag("sheet-body:epic:$LOOSE_ID")
            composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals(LOOSE_SLUG)
            composeRule.onAllNodesWithTag("sheet-body:all", useUnmergedTree = true).assertCountEquals(0)
            composeRule.onAllNodesWithTag(PHONE_SHEET_TAG, useUnmergedTree = true).assertCountEquals(1)
        }
    }

    @Test
    fun phoneSheetKidSwitchesTheSheetToItsEpicAndUpReturnsToTheDesign() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            val epicRow = epic(EPIC_ID, EPIC_SLUG, steering = SESSION_ID)
            val tide = design(
                DESIGN_ID,
                DESIGN_TITLE,
                SESSION_ID,
                contracts = listOf(designContract(CONTRACT_ID, epic = EPIC_ID)),
            )
            serveWork(server, designs = listOf(tide), epics = listOf(epicRow))
            serveEpicDetail(server, epicRow)
            setSession(server)
            awaitTag(DESIGN_LINK_TAG)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()
            awaitTag("hung:epic:$EPIC_ID")

            composeRule.onNodeWithTag("hung:epic:$EPIC_ID", useUnmergedTree = true).performScrollTo().performClick()

            awaitTag("sheet-body:epic:$EPIC_ID")
            composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals(EPIC_SLUG)
            composeRule.onNodeWithTag(SHEET_UP_TAG, useUnmergedTree = true).assertTextEquals(DESIGN_TITLE)
            composeRule.onNodeWithTag(DESIGN_LINK_TAG).assertIsSelected()

            composeRule.onNodeWithTag(SHEET_UP_TAG, useUnmergedTree = true).performClick()

            awaitTag("sheet-body:design:$DESIGN_ID")
            composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals(DESIGN_TITLE)
            composeRule.onAllNodesWithTag(SHEET_UP_TAG, useUnmergedTree = true).assertCountEquals(0)
        }
    }
}
