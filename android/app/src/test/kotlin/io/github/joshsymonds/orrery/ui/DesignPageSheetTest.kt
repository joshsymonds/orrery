package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignStage
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

// A design's page on the round 8 phone sheet: the design's link opens the sheet on the page, drawn from Designs.Get, and a
// contract row of the page switches the sheet to that contract. Read through the semantics tree alone, with the records
// invented here at a fixed clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class DesignPageSheetTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val SESSION_ID = "synthetic-session"
        const val DESIGN_ID = "synthetic-design"
        const val DESIGN_TITLE = "Synthetic design"
        const val CONTRACT_ID = "synthetic-owner/synthetic-contract"
        const val CONTRACT_NAME = "synthetic-contract"
        const val QUESTION_KEY = "Q1"
        const val QUESTION_TEXT = "Synthetic question text"
        const val DESIGN_LINK_TAG = "work-link:design:$DESIGN_ID"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val DESIGNS_PATH = "/orrery.v1.Designs/List"
        const val DESIGN_GET_PATH = "/orrery.v1.Designs/Get"
        const val EPICS_PATH = "/orrery.v1.Epics/List"
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

    /** Streams the headless session [SESSION_ID], which has nothing running. */
    private fun serveSession(server: FakeOrreryServer) {
        val session = HeadlessSession.newBuilder()
            .setId(SESSION_ID)
            .setDir("/home/user/lighthouse")
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

    /**
     * One design the conversation works, with its one proposed contract, and its stage with one open question about that
     * contract. Designs.List lists it, and Designs.Get reads it with its stage.
     */
    private fun serveDesign(server: FakeOrreryServer) {
        val contract = DesignContract.newBuilder()
            .setId(CONTRACT_ID)
            .setTitle("Synthetic contract")
            .setRepository("synthetic-owner")
            .setState("proposed")
            .setPosition(1L)
            .build()
        val design = Design.newBuilder()
            .setId(DESIGN_ID)
            .setTitle(DESIGN_TITLE)
            .setConversation(SESSION_ID)
            .setCreated(NOW - 600)
            .setOfOne(false)
            .setState("0 of 1 accepted")
            .addContracts(contract)
            .build()
        val question = DesignQuestion.newBuilder()
            .setId(1L)
            .setContract(CONTRACT_ID)
            .setText(QUESTION_TEXT)
            .addOptions("Synthetic option")
            .setRecommendation("Synthetic option")
            .setAsked(NOW - 120)
            .build()
        server.respond(DESIGNS_PATH, DesignsListResponse.newBuilder().addDesigns(design).build())
        server.respond(
            DESIGN_GET_PATH,
            DesignsGetResponse.newBuilder()
                .setDesign(design)
                .setStage(DesignStage.newBuilder().addQuestions(question))
                .build(),
        )
        server.respond(EPICS_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(CONTRACTS_PATH, ContractsListResponse.getDefaultInstance())
    }

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil("the node tagged $tag") {
            composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    /** The node tagged [body] holds a node tagged [tag] somewhere under it. */
    private fun assertBodyHolds(body: String, tag: String) {
        composeRule.onNode(hasTestTag(body) and hasAnyDescendant(hasTestTag(tag)), useUnmergedTree = true).assertExists()
    }

    /** Opens the design's page in the sheet, which waits for Designs.Get to answer and draw it. */
    private fun openDesignPage() {
        awaitTag(DESIGN_LINK_TAG)
        composeRule.onNodeWithTag(DESIGN_LINK_TAG).performClick()
        awaitTag("design-page")
    }

    @Test
    fun aDesignLinkOpensTheSheetOnTheDesignsPageDrawnFromDesignsGet() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveDesign(server)
            setSession(server)

            openDesignPage()

            awaitTag(PHONE_SHEET_TAG)
            composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals(DESIGN_TITLE)
            assertBodyHolds("sheet-body:design:$DESIGN_ID", "design-page")
            // The question's thread and the contract row are the stage's and the design's, which only Designs.Get carries.
            awaitTag("question-thread:$QUESTION_KEY")
            composeRule.onNodeWithTag("design-contract:$CONTRACT_ID", useUnmergedTree = true).assertExists()
        }
    }

    @Test
    fun aContractRowOfTheDesignPageSwitchesTheSheetToThatContract() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveDesign(server)
            setSession(server)
            openDesignPage()

            composeRule.onNodeWithTag("design-contract:$CONTRACT_ID", useUnmergedTree = true).performScrollTo().performClick()

            awaitTag("sheet-body:contract:$CONTRACT_ID")
            composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals(CONTRACT_NAME)
            composeRule.onNodeWithTag(SHEET_UP_TAG, useUnmergedTree = true).assertTextEquals(DESIGN_TITLE)
            composeRule.onAllNodesWithTag("sheet-body:design:$DESIGN_ID", useUnmergedTree = true).assertCountEquals(0)
        }
    }
}
