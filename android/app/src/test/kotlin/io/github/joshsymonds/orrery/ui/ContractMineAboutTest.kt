package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.content.Intent
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
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
import io.github.joshsymonds.orrery.gen.orrery.v1.SayRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SayResponse
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
import org.robolectric.android.controller.ActivityController
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val NOW = 1_700_000_000L
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val SAY_PATH = "/orrery.v1.Control/Say"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val CONTRACTS_LIST_PATH = "/orrery.v1.Contracts/List"
private const val CONTRACT_GET_PATH = "/orrery.v1.Contracts/Get"
private const val ABOUT_ROW = "R1"
private const val ABOUT_TEXT = "Synthetic requirement text"
private const val DRAFT = "Synthetic draft"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class ContractMineAboutTest {
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
        server.respond(SAY_PATH, SayResponse.getDefaultInstance())
        serveSession()
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
        server.close()
    }

    /** The contract as the list holds it: proposed by [CONTRACT_SESSION], the conversation the tests open it from. */
    private fun listed(): Contract = Contract.newBuilder()
        .setId(RECORD_ID)
        .setTitle("Synthetic contract title")
        .setCare("limited")
        .setState("proposed")
        .setVersion(1L)
        .setConversation(CONTRACT_SESSION)
        .build()

    /** Streams the live headless session [CONTRACT_SESSION]. */
    private fun serveSession() {
        val session = HeadlessSession.newBuilder()
            .setId(CONTRACT_SESSION)
            .setDir("/home/user/alpha")
            .setLabel("alpha-session")
            .setState("waiting")
            .setChanged(NOW - 30)
            .setSeen(NOW - 10)
            .build()
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(CONTRACT_SESSION)
            .setSeq(1L)
            .setState(ConversationState.newBuilder().setOrigin("synthetic origin").setSession(session))
            .build()
        server.route(WATCH_PATH) { Reply.Stream(listOf(delta), StreamEnd.Open) }
    }

    private fun launch() {
        val intent = Intent(context, MainActivity::class.java).putExtra(PushNotifier.EXTRA_SESSION, CONTRACT_SESSION)
        launched += Robolectric.buildActivity(MainActivity::class.java, intent).setup()
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

    /** The texts Control.Say sent to the session, oldest first. */
    private fun said(): List<String> = server.requests
        .filter { it.rawPath == SAY_PATH }
        .map { SayRequest.parseFrom(it.body).text }

    /** Opens the live contract from its tile, then presses the row control of [ABOUT_ROW], which returns to the conversation. */
    private fun pressRowControlFromTheContract() {
        awaitNode(hasTestTag("tile:contract:$RECORD_ID"))
        composeRule.onNodeWithTag("tile:contract:$RECORD_ID").performClick()
        awaitNode(hasTestTag("contract-talk:$ABOUT_ROW"))
        composeRule.onNodeWithTag("contract-talk:$ABOUT_ROW").performClick()
        awaitNode(hasTestTag("dock-about"))
        awaitNone(hasTestTag("contract-record"))
    }

    private fun editField(): EditText =
        checkNotNull(findEditText(launched.last().get().window.decorView)) { "the dock must host the draft's EditText" }

    private fun findEditText(view: View): EditText? {
        if (view is EditText) return view
        if (view is ViewGroup) {
            for (index in 0 until view.childCount) {
                findEditText(view.getChildAt(index))?.let { return it }
            }
        }
        return null
    }

    /** Puts [text] in the dock's draft and presses Send. */
    private fun typeAndSend(text: String) {
        composeRule.runOnUiThread { editField().setText(text) }
        composeRule.onNodeWithText("Send").performClick()
    }

    @Test
    fun aRowControlReturnsToTheConversationWithTheAboutLine() {
        launch()
        awaitNode(hasTestTag("head-text"))

        pressRowControlFromTheContract()

        composeRule.onNodeWithTag("dock-about").assertTextEquals("about $ABOUT_ROW $ABOUT_TEXT")
    }

    @Test
    fun theSentTextIsTheAboutThenABlankLineThenTheDraft() {
        launch()
        awaitNode(hasTestTag("head-text"))
        pressRowControlFromTheContract()

        typeAndSend(DRAFT)

        composeRule.awaitUntil { said() == listOf("About $ABOUT_ROW: $ABOUT_TEXT\n\n$DRAFT") }
        awaitNone(hasTestTag("dock-about"))
    }

    @Test
    fun notAboutThisClearsTheAboutAndTheNextSendIsPlain() {
        launch()
        awaitNode(hasTestTag("head-text"))
        pressRowControlFromTheContract()

        composeRule.onNodeWithTag("dock-not-about").performClick()
        awaitNone(hasTestTag("dock-about"))
        typeAndSend(DRAFT)

        composeRule.awaitUntil { said() == listOf(DRAFT) }
    }

}
