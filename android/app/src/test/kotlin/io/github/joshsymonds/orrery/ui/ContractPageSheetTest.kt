package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRowWrite
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractVersion
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsKeepRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsKeepResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAddition
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.util.concurrent.atomic.AtomicBoolean

// A proposed contract's page on the round 8 phone sheet: the contract's link opens the sheet on the page with its accept
// bar, and a Keep on an addition sends the keep and reads the contract again. Read through the semantics tree alone, with
// the records invented here at a fixed clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ContractPageSheetTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val SESSION_ID = "synthetic-session"
        const val LAMP_ID = "synthetic-lamp"
        const val CONTRACT_ID = "synthetic-owner/synthetic-contract"
        const val CONTRACT_NAME = "synthetic-contract"
        const val ADDITION_ID = "A1"
        const val CONTRACT_LINK_TAG = "work-link:contract:$CONTRACT_ID"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val DESIGNS_PATH = "/orrery.v1.Designs/List"
        const val EPICS_PATH = "/orrery.v1.Epics/List"
        const val CONTRACTS_PATH = "/orrery.v1.Contracts/List"
        const val CONTRACT_GET_PATH = "/orrery.v1.Contracts/Get"
        const val KEEP_PATH = "/orrery.v1.Contracts/Keep"
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

    /** A design of one that the conversation works, whose one contract is [CONTRACT_ID], so the contract is a work link. */
    private fun serveDesignOfOne(server: FakeOrreryServer) {
        val contract = DesignContract.newBuilder()
            .setId(CONTRACT_ID)
            .setTitle("Synthetic contract")
            .setRepository("synthetic-owner")
            .setState("proposed")
            .setPosition(1L)
            .build()
        val lamp = Design.newBuilder()
            .setId(LAMP_ID)
            .setTitle("Synthetic lamp")
            .setConversation(SESSION_ID)
            .setCreated(NOW - 600)
            .setOfOne(true)
            .setState("0 of 1 accepted")
            .addContracts(contract)
            .build()
        server.respond(DESIGNS_PATH, DesignsListResponse.newBuilder().addDesigns(lamp).build())
        server.respond(EPICS_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(CONTRACTS_PATH, ContractsListResponse.getDefaultInstance())
    }

    /** The proposed contract, at its one version, with the addition [ADDITION_ID] that the owner is asked to keep or strike. */
    private fun proposedRecord(): ContractsGetResponse = ContractsGetResponse.newBuilder()
        .setContract(
            Contract.newBuilder()
                .setId(CONTRACT_ID)
                .setTitle("Synthetic contract title")
                .setCare("limited")
                .setState("proposed")
                .setVersion(1L)
                .setConversation(SESSION_ID)
                .build(),
        )
        .addVersions(
            ContractVersion.newBuilder()
                .setVersion(1L)
                .setContract(
                    EpicContract.newBuilder()
                        .setTitle("Synthetic contract title")
                        .setIntent("Synthetic intent text.")
                        .setCare("limited")
                        .addAdditions(
                            EpicAddition.newBuilder()
                                .setLocation("synthetic location")
                                .setWhat("Synthetic addition text")
                                .setWhy("Synthetic addition why")
                                .setCost("Synthetic addition cost")
                                .build(),
                        )
                        .setApproach("Synthetic approach text.")
                        .setChecks("Synthetic checks text.")
                        .build(),
                )
                .addRequirements(
                    EpicRequirement.newBuilder()
                        .setId("R1")
                        .setText("Synthetic requirement text")
                        .setEvidence("Synthetic requirement evidence")
                        .setKind("check")
                        .build(),
                )
                .setWriter("synthetic writer")
                .setTime(NOW - 300)
                .build(),
        )
        .build()

    /** The same contract once the owner has kept [ADDITION_ID]: the stage records the keep, which the row reads as kept. */
    private fun keptRecord(): ContractsGetResponse = proposedRecord().toBuilder()
        .addRows(
            ContractRowWrite.newBuilder()
                .setSeq(1L)
                .setVersion(1L)
                .setRow(ADDITION_ID)
                .setAction("keep")
                .build(),
        )
        .build()

    /**
     * Serves the contract's record: proposed until a Keep lands, then kept. Keep answers the contract as it stands after it.
     */
    private fun serveContract(server: FakeOrreryServer) {
        val kept = AtomicBoolean(false)
        server.route(CONTRACT_GET_PATH) { Reply.Message(if (kept.get()) keptRecord() else proposedRecord(), 0L) }
        server.route(KEEP_PATH) {
            kept.set(true)
            Reply.Message(ContractsKeepResponse.newBuilder().setContract(proposedRecord().contract).build(), 0L)
        }
    }

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil("the node tagged $tag") {
            composeRule.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    /** The node tagged [body] holds a node tagged [tag] somewhere under it. */
    private fun assertBodyHolds(body: String, tag: String) {
        composeRule.onNode(hasTestTag(body) and hasAnyDescendant(hasTestTag(tag)), useUnmergedTree = true).assertExists()
    }

    @Test
    fun aProposedContractsLinkOpensItsPageWithTheAcceptBar() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveDesignOfOne(server)
            serveContract(server)
            setSession(server)
            awaitTag(CONTRACT_LINK_TAG)

            composeRule.onNodeWithTag(CONTRACT_LINK_TAG).performClick()

            awaitTag(CONTRACT_PAGE_TAG)
            assertBodyHolds("sheet-body:contract:$CONTRACT_ID", CONTRACT_PAGE_TAG)
            assertBodyHolds("sheet-body:contract:$CONTRACT_ID", ACCEPT_BAR_TAG)
            composeRule.onNodeWithTag(SHEET_NAME_TAG, useUnmergedTree = true).assertTextEquals(CONTRACT_NAME)
        }
    }

    @Test
    fun keepOnAnAdditionSendsOneKeepNamingItAndTheReloadedRowReadsKept() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            serveDesignOfOne(server)
            serveContract(server)
            setSession(server)
            awaitTag(CONTRACT_LINK_TAG)
            composeRule.onNodeWithTag(CONTRACT_LINK_TAG).performClick()
            awaitTag("addition-keep:$ADDITION_ID")

            composeRule.onNodeWithTag("addition-keep:$ADDITION_ID", useUnmergedTree = true).performScrollTo().performClick()

            composeRule.awaitUntil("the addition $ADDITION_ID reads kept") {
                composeRule.onAllNodes(
                    hasTestTag("$ADDITION_ROW_TAG_PREFIX$ADDITION_ID") and hasAnyDescendant(hasText("kept")),
                    useUnmergedTree = true,
                ).fetchSemanticsNodes().isNotEmpty()
            }
            val keeps = server.requests.filter { it.rawPath == KEEP_PATH }
            assertEquals(1, keeps.size)
            assertEquals(ADDITION_ID, ContractsKeepRequest.parseFrom(keeps.single().body).addition)
        }
    }
}
