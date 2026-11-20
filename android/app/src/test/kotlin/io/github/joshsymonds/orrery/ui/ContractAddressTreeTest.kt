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
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadEmptyComposeRule
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

private const val TREE_SESSION = "synthetic-tree-session"
private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val HEADLESS_PATH = "/orrery.v1.Sessions/ListHeadless"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val CONTRACTS_LIST_PATH = "/orrery.v1.Contracts/List"
private const val CONTRACT_GET_PATH = "/orrery.v1.Contracts/Get"
private const val CONTRACT_TITLE = "Synthetic contract title"
private const val TREE_ROW_TAG = "hung:contract:$RECORD_ID"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class ContractAddressTreeTest {
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
        server.respond(PANES_PATH, PanesListResponse.getDefaultInstance())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(CONTRACTS_LIST_PATH, ContractsListResponse.newBuilder().addContracts(listed()).build())
        server.respond(CONTRACT_GET_PATH, contractRecordProto(state = "proposed", versions = listOf(contractVersionProto(1))))
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
        server.close()
    }

    /** The contract as the list holds it: proposed by [TREE_SESSION], the conversation the inbox tree hangs it under. */
    private fun listed(): Contract = Contract.newBuilder()
        .setId(RECORD_ID)
        .setTitle(CONTRACT_TITLE)
        .setCare("limited")
        .setState("proposed")
        .setVersion(1L)
        .setConversation(TREE_SESSION)
        .build()

    /**
     * Lists the headless session [TREE_SESSION] in [state], so the inbox tree holds its conversation, and streams it when
     * its screen opens.
     */
    private fun serveSession(state: String) {
        val session = HeadlessSession.newBuilder()
            .setId(TREE_SESSION)
            .setDir("/home/user/alpha")
            .setLabel("alpha-session")
            .setState(state)
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        server.respond(HEADLESS_PATH, ListHeadlessResponse.newBuilder().addSessions(session).build())
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(TREE_SESSION)
            .setSeq(1L)
            .setState(ConversationState.newBuilder().setOrigin("synthetic origin").setSession(session))
            .build()
        server.route(WATCH_PATH) { Reply.Stream(listOf(delta), StreamEnd.Open) }
    }

    /** Opens the app on its inbox, with no screen asked for, so the tree is the first thing shown. */
    private fun launch(): ActivityController<MainActivity> {
        val intent = Intent(context, MainActivity::class.java)
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

    @Test
    fun theTreeRowOpensTheContractLiveAndBackReturnsToTheList() {
        serveSession(state = "waiting")
        val controller = launch()
        awaitNode(hasTestTag(TREE_ROW_TAG))

        composeRule.onNodeWithTag(TREE_ROW_TAG).performClick()

        awaitContractScreen()
        awaitNode(hasTestTag("contract-talk:R1"))
        assertAbsent(hasTestTag("contract-no-live"))

        controller.get().onBackPressedDispatcher.onBackPressed()
        awaitNode(hasTestTag(TREE_ROW_TAG))
        awaitNone(hasTestTag("contract-record"))
    }

    @Test
    fun aStoppedConversationsTreeRowOpensTheContractWithTheNoLiveLineAndNoRowControls() {
        serveSession(state = "stopped")
        launch()
        awaitNode(hasTestTag(TREE_ROW_TAG))

        composeRule.onNodeWithTag(TREE_ROW_TAG).performClick()

        awaitContractScreen()
        awaitNode(hasTestTag("contract-no-live"))
        assertAbsent(hasTestTag("contract-talk:R1"))
    }

    @Test
    fun aRowsTalkControlOpensItsConversationWithTheAboutSet() {
        serveSession(state = "waiting")
        val controller = launch()
        awaitNode(hasTestTag(TREE_ROW_TAG))
        composeRule.onNodeWithTag(TREE_ROW_TAG).performClick()
        awaitContractScreen()
        awaitNode(hasTestTag("contract-talk:R1"))

        composeRule.onNodeWithTag("contract-talk:R1").performClick()

        awaitNode(hasTestTag("dock-about"))
        awaitNode(hasText("about R1", substring = true))
        awaitNone(hasTestTag("contract-record"))
        controller.get().onBackPressedDispatcher.onBackPressed()
        awaitNode(hasTestTag(TREE_ROW_TAG))
    }
}
