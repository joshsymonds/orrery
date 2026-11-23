package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.content.Intent
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.MainActivity
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadEmptyComposeRule
import io.github.joshsymonds.orrery.push.PushNotifier
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.android.controller.ActivityController

internal const val CONTRACT_SESSION = "synthetic-session"
private const val NOW = 1_700_000_000L
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val CONTRACTS_LIST_PATH = "/orrery.v1.Contracts/List"
private const val CONTRACT_GET_PATH = "/orrery.v1.Contracts/Get"
private const val DESIGNS_LIST_PATH = "/orrery.v1.Designs/List"
private const val CONTRACT_TITLE = "Synthetic contract title"
private const val DESIGN_ID = "synthetic-design"
private const val CALL_ID = "synthetic-call"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class ContractAddressTest {
    @get:Rule
    val composeRule = mainThreadEmptyComposeRule()

    private lateinit var context: Context
    private lateinit var server: FakeOrreryServer
    private val launched = mutableListOf<ActivityController<MainActivity>>()

    @Before
    fun pointSettingsAtFakeServer() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
        server = FakeOrreryServer()
        ServerSettings(context).saveServerUrl(server.baseUrl)
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(CONTRACTS_LIST_PATH, ContractsListResponse.newBuilder().addContracts(listed()).build())
        server.respond(CONTRACT_GET_PATH, contractRecordProto(state = "proposed", versions = listOf(contractVersionProto(1))))
        server.respond(DESIGNS_LIST_PATH, DesignsListResponse.newBuilder().addDesigns(ofOneDesign()).build())
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
        server.close()
    }

    /**
     * A design of one the conversation works: its one contract, [RECORD_ID], with no epic made from it, so the design's
     * work link opens the contract.
     */
    private fun ofOneDesign(): Design = Design.newBuilder()
        .setId(DESIGN_ID)
        .setTitle("Synthetic design")
        .setConversation(CONTRACT_SESSION)
        .setCreated(NOW - 120)
        .setOfOne(true)
        .setState("proposed")
        .addContracts(
            DesignContract.newBuilder()
                .setId(RECORD_ID)
                .setTitle(CONTRACT_TITLE)
                .setRepository("synthetic-owner")
                .setState("proposed")
                .setPosition(1L),
        )
        .build()

    /** The contract as the list holds it: proposed by [CONTRACT_SESSION], the conversation the tests open it from. */
    private fun listed(): Contract = Contract.newBuilder()
        .setId(RECORD_ID)
        .setTitle(CONTRACT_TITLE)
        .setCare("limited")
        .setState("proposed")
        .setVersion(1L)
        .setConversation(CONTRACT_SESSION)
        .build()

    /** A tool call that writes the contract, with its result, as the transcript holds it. */
    private fun contractCall(): List<ConversationItemProto> = listOf(
        ConversationItemProto.newBuilder()
            .setKind("tool")
            .setTime(NOW - 60)
            .setTool("contract_propose")
            .setInput("synthetic input")
            .setId(CALL_ID)
            .setContract(RECORD_ID)
            .build(),
        ConversationItemProto.newBuilder()
            .setKind("result")
            .setTime(NOW - 59)
            .setText("Synthetic result")
            .setId(CALL_ID)
            .build(),
    )

    /** Streams the headless session [CONTRACT_SESSION] in [state], with the contract call in its transcript. */
    private fun serveSession(state: String) {
        val session = HeadlessSession.newBuilder()
            .setId(CONTRACT_SESSION)
            .setDir("/home/user/alpha")
            .setLabel("alpha-session")
            .setState(state)
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(CONTRACT_SESSION)
            .setSeq(1L)
            .addAllItems(contractCall())
            .setState(ConversationState.newBuilder().setOrigin("synthetic origin").setSession(session))
            .build()
        server.route(WATCH_PATH) { Reply.Stream(listOf(delta), StreamEnd.Open) }
    }

    private fun launch(): ActivityController<MainActivity> {
        val intent = Intent(context, MainActivity::class.java).putExtra(PushNotifier.EXTRA_SESSION, CONTRACT_SESSION)
        return Robolectric.buildActivity(MainActivity::class.java, intent).setup().also { launched += it }
    }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    private fun awaitNone(matcher: SemanticsMatcher) {
        composeRule.awaitUntil {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isEmpty()
        }
    }

    private fun assertAbsent(matcher: SemanticsMatcher) {
        assertTrue(
            "expected no node matching $matcher",
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isEmpty(),
        )
    }

    /** Waits until the contract screen has read its record and drawn the record's title. */
    private fun awaitContractScreen() {
        awaitNode(hasTestTag("contract-record"))
        awaitNode(hasText(CONTRACT_TITLE))
    }

    /** Waits until the conversation is drawn again, with its head, and the contract screen is gone. */
    private fun awaitConversationAgain() {
        awaitNode(hasTestTag("head-text"))
        awaitNone(hasTestTag("contract-record"))
    }

    @Test
    fun theTileOpensTheContractLiveAndBackReturnsToTheConversation() {
        serveSession(state = "waiting")
        val controller = launch()
        awaitNode(hasTestTag("work-link:contract:$RECORD_ID"))

        composeRule.onNodeWithTag("work-link:contract:$RECORD_ID").performClick()

        awaitContractScreen()
        awaitNode(hasTestTag("contract-talk:R1"))
        assertAbsent(hasTestTag("contract-no-live"))

        controller.get().onBackPressedDispatcher.onBackPressed()
        awaitConversationAgain()
    }

    @Test
    fun theBandLineOpensTheContractLiveAndBackReturnsToTheConversation() {
        serveSession(state = "waiting")
        val controller = launch()
        awaitNode(hasTestTag("band-contract:$RECORD_ID"))

        composeRule.onNodeWithTag("band-contract:$RECORD_ID").performClick()

        awaitContractScreen()
        awaitNode(hasTestTag("contract-talk:R1"))

        controller.get().onBackPressedDispatcher.onBackPressed()
        awaitConversationAgain()
    }

    @Test
    fun theTranscriptLineOpensTheContractLiveAndBackReturnsToTheConversation() {
        serveSession(state = "waiting")
        val controller = launch()
        // The band line shows once the conversation's contracts are listed, which a transcript line needs to be live.
        awaitNode(hasTestTag("band-contract:$RECORD_ID"))
        awaitNode(hasTestTag("contract-line:$RECORD_ID"))

        composeRule.onNodeWithTag("contract-line:$RECORD_ID").performClick()

        awaitContractScreen()
        awaitNode(hasTestTag("contract-talk:R1"))

        controller.get().onBackPressedDispatcher.onBackPressed()
        awaitConversationAgain()
    }

    @Test
    fun aStoppedConversationOpensTheContractWithTheNoLiveLineAndNoRowControls() {
        serveSession(state = "stopped")
        launch()
        awaitNode(hasTestTag("head-text"))
        awaitNode(hasTestTag("work-link:contract:$RECORD_ID"))

        composeRule.onNodeWithTag("work-link:contract:$RECORD_ID").performClick()

        awaitContractScreen()
        awaitNode(hasTestTag("contract-no-live"))
        assertAbsent(hasTestTag("contract-talk:R1"))
    }
}
