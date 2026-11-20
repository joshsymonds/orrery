package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.OpenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// A waiting pane's and a headless session's screens draw the Ask in place of the old cards: who, age, question and
// options, a tap answers through AskHost, and a put-off question draws Bring it back. The screens run on the injected
// clock NOW; the Ask's age is read from the item's changed time, four minutes before NOW.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class AskStatesScreenTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val CHANGED = NOW - 240L
        const val PANE_ID = "%3"
        const val SESSION_ID = "00000000000000000000000000000005"
        const val REQUEST_ID = "synthetic-request"
        const val PANES_PATH = "/orrery.v1.Panes/List"
        const val KEYS_PATH = "/orrery.v1.Panes/SendKeys"
        const val STREAM_PATH = "/orrery.v1.Panes/WatchConversation"
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val ANSWER_PATH = "/orrery.v1.Control/Answer"
        const val BASH_QUESTION = """{"tool":"Bash","input":{"command":"echo synthetic"}}"""
        const val SESSION_QUESTION = "Pick a synthetic option?"
        const val OPTION_ONE = "Synthetic one"
        const val OPTION_TWO = "Synthetic two"
        const val WHO = "project · example-label"
    }

    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private fun paneList(putOff: Boolean = false): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(
            PaneProto.newBuilder()
                .setId(PANE_ID)
                .setHost("example-host")
                .setSession("example-session")
                .setWindow(1)
                .setPane(2)
                .setCwd("/home/user/project")
                .setCommand("claude")
                .setState("waiting")
                .setSummary("")
                .setLabel("example-label")
                .setChanged(CHANGED)
                .setSeen(0L)
                .setQuestion(BASH_QUESTION)
                .setQuestionHash("abc123")
                .setPutOff(putOff),
        )
        .build()

    private fun setPaneScreen(server: FakeOrreryServer, putOff: Boolean = false) {
        server.respond(PANES_PATH, paneList(putOff))
        // The pane screen polls at once when its stream reports the pane has none.
        server.route(STREAM_PATH) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client, clock = { NOW }) }
        }
    }

    private fun sessionState(request: OpenRequest): ConversationState = ConversationState.newBuilder()
        .setOrigin("synthetic origin")
        .setSession(
            HeadlessSession.newBuilder()
                .setId(SESSION_ID)
                .setDir("/home/user/project")
                .setLabel("example-label")
                .setState("waiting")
                .setChanged(CHANGED)
                .setSeen(0L),
        )
        .setRequest(request)
        .build()

    private fun setSessionScreen(server: FakeOrreryServer, request: OpenRequest) {
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .setState(sessionState(request))
            .build()
        // The first stream carries the open request; a reconnect gets a stream that stays open and quiet.
        val streams = AtomicInteger()
        server.route(WATCH_PATH) {
            if (streams.getAndIncrement() == 0) Reply.Stream(listOf(delta), StreamEnd.Open)
            else Reply.Stream(emptyList(), StreamEnd.Open)
        }
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client, clock = { NOW }) }
        }
    }

    private fun sessionRequest(putOff: Boolean = false): OpenRequest = OpenRequest.newBuilder()
        .setId(REQUEST_ID)
        .setKind("question")
        .setQuestion(SESSION_QUESTION)
        .addAllOptions(listOf(OPTION_ONE, OPTION_TWO))
        .setPutOff(putOff)
        .build()

    @Test
    fun waitingPaneDrawsItsAskWithWhoAgeQuestionAndOptions() {
        FakeOrreryServer().use { server ->
            setPaneScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("ask"))
            composeRule.onNodeWithTag("ask-who").assertTextEquals(WHO)
            composeRule.onNodeWithTag("ask-age").assertTextEquals("· 4m ago")
            composeRule.onNodeWithTag("ask-question").assertTextEquals("Run this command?")
            composeRule.onNodeWithTag("ask-option:1").assertTextContains("Yes")
            composeRule.onNodeWithTag("ask-option:2").assertTextContains("Yes, and don't ask again")
            composeRule.onNodeWithTag("ask-option:3").assertTextContains("No")
        }
    }

    @Test
    fun paneAskTapPostsItsDigitWithTheQuestionHash() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setPaneScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("ask-option:2"))

            composeRule.onNodeWithTag("ask-option:2").performClick()
            composeRule.awaitUntil { server.requests.any { it.rawPath == KEYS_PATH } }

            val sent = SendKeysRequest.parseFrom(server.requests.single { it.rawPath == KEYS_PATH }.body)
            assertEquals(PANE_ID, sent.pane)
            assertEquals(listOf("2"), sent.keysList.toList())
            assertEquals("abc123", sent.expectQuestion)
        }
    }

    @Test
    fun putOffPaneAskDrawsBringBackAndNoNotNow() {
        FakeOrreryServer().use { server ->
            setPaneScreen(server, putOff = true)

            composeRule.awaitExactlyOne(hasTestTag("ask-bring-back"))
            composeRule.onNodeWithTag("ask-label").assertIsDisplayed()
            composeRule.onNodeWithTag("ask-question").assertIsDisplayed()
            composeRule.onAllNodesWithTag("ask-not-now").assertCountEquals(0)
        }
    }

    @Test
    fun headlessSessionDrawsItsAskWithWhoQuestionAndOptions() {
        FakeOrreryServer().use { server ->
            setSessionScreen(server, sessionRequest())

            composeRule.awaitExactlyOne(hasTestTag("ask"))
            composeRule.onNodeWithTag("ask-who").assertTextEquals(WHO)
            composeRule.onNodeWithTag("ask-age").assertTextEquals("· 4m ago")
            composeRule.onNodeWithTag("ask-question").assertTextEquals(SESSION_QUESTION)
            composeRule.onNodeWithTag("ask-option:1").assertTextContains(OPTION_ONE)
            composeRule.onNodeWithTag("ask-option:2").assertTextContains(OPTION_TWO)
        }
    }

    @Test
    fun headlessSessionAskTapPostsItsLabelWithTheRequestId() {
        FakeOrreryServer().use { server ->
            server.respond(ANSWER_PATH, AnswerResponse.getDefaultInstance())
            setSessionScreen(server, sessionRequest())
            composeRule.awaitExactlyOne(hasTestTag("ask-option:2"))

            composeRule.onNodeWithTag("ask-option:2").performClick()
            composeRule.awaitUntil { server.requests.any { it.rawPath == ANSWER_PATH } }

            val sent = AnswerRequest.parseFrom(server.requests.single { it.rawPath == ANSWER_PATH }.body)
            assertEquals(
                AnswerRequest.newBuilder()
                    .setId(SESSION_ID)
                    .setRequestId(REQUEST_ID)
                    .setAnswer(OPTION_TWO)
                    .build(),
                sent,
            )
        }
    }

    @Test
    fun putOffHeadlessSessionAskDrawsBringBackAndNoNotNow() {
        FakeOrreryServer().use { server ->
            setSessionScreen(server, sessionRequest(putOff = true))

            composeRule.awaitExactlyOne(hasTestTag("ask-bring-back"))
            composeRule.onAllNodesWithTag("ask-not-now").assertCountEquals(0)
            composeRule.onNodeWithTag("ask-question").assertIsDisplayed()
        }
    }
}
