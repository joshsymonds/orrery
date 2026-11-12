package io.github.joshsymonds.orrery.ui

import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import com.google.protobuf.MessageLite
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.OpenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsHoldRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsHoldResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopResponse
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val STEP_SESSION_ID = "fedcba9876543210fedcba9876543210"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val INTERRUPT_PATH = "/orrery.v1.Control/Interrupt"
private const val SAY_PATH = "/orrery.v1.Control/Say"
private const val ANSWER_PATH = "/orrery.v1.Control/Answer"
private const val CONTROL_STOP_PATH = "/orrery.v1.Control/Stop"
private val SESSION_CONTROL_PATHS = listOf(INTERRUPT_PATH, SAY_PATH, ANSWER_PATH, CONTROL_STOP_PATH)
private const val HOLD_PATH = "/orrery.v1.Steps/Hold"
private const val NUDGE_PATH = "/orrery.v1.Steps/Nudge"
private const val STEPS_STOP_PATH = "/orrery.v1.Steps/Stop"
private const val INTERRUPT_TAG = "session-interrupt"
private const val CONTROL_STOP_TAG = "session-stop"
private const val REQUEST_TAG = "session-request"
private const val HOLD_TAG = "step-hold"
private const val STOP_TAG = "step-stop"
private const val CONFIRM_TAG = "step-stop-confirm"
private const val NUDGE_HINT = "Nudge the step…"
private const val NUDGE_NOTE = "A message to a step is a nudge."
private const val NUDGE_LANDED = "nudge landed"
private const val REFUSAL_REASON = "synthetic refusal"
private const val SYNTHETIC_DRAFT = "synthetic nudge draft"
private const val STEP_REPLY = "synthetic step reply"
private const val REQUEST_ID = "synthetic-request"
private const val QUESTION = "Pick a synthetic option?"
private const val OPTION_ONE = "Synthetic one"
private const val OPTION_TWO = "Synthetic two"
private const val TIMEOUT_MILLIS = 5_000L

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class SessionScreenStepTest {
    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

    private fun setStepScreen(server: FakeOrreryServer, held: Boolean = false) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(STEP_SESSION_ID, client, step = StepMode(held = held)) }
        }
    }

    /** Scripts the watch stream with one reply: the first request gets [stream], later ones a silent stream. */
    private fun serveStream(server: FakeOrreryServer, stream: Reply) {
        val requests = AtomicInteger()
        server.route(WATCH_PATH) { _ ->
            if (requests.getAndIncrement() == 0) stream else Reply.Stream(emptyList(), StreamEnd.Silent)
        }
    }

    private fun streamOf(vararg messages: MessageLite): Reply.Stream =
        Reply.Stream(messages.toList(), StreamEnd.Silent)

    private fun stateOf(state: String, request: OpenRequest? = null): ConversationState {
        val builder = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(STEP_SESSION_ID)
                    .setDir("/home/user/project")
                    .setLabel("example-label")
                    .setState(state)
                    .setChanged(4102444800L)
                    .setSeen(4102444900L),
            )
        request?.let { builder.setRequest(it) }
        return builder.build()
    }

    private fun delta(
        seq: Long,
        items: List<ConversationItemProto> = emptyList(),
        state: ConversationState? = null,
    ): SessionsWatchConversationResponse {
        val builder = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(STEP_SESSION_ID)
            .setSeq(seq)
            .addAllItems(items)
        state?.let { builder.setState(it) }
        return builder.build()
    }

    private fun item(kind: String, text: String): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind(kind).setTime(0L).setText(text).build()

    private fun countPosts(server: FakeOrreryServer, path: String): Int =
        server.requests.count { it.method == "POST" && it.rawPath == path }

    /** Session control calls the step screen must never make: Say, Answer, Interrupt and Stop on the session. */
    private fun sessionControlPosts(server: FakeOrreryServer): List<String> =
        server.requests
            .filter { it.method == "POST" && it.rawPath in SESSION_CONTROL_PATHS }
            .map { it.rawPath }

    private fun setDraft(text: String) {
        composeRule.runOnUiThread { editField().setText(text) }
        composeRule.waitForIdle()
    }

    private fun editField(): EditText =
        checkNotNull(findEditText(composeRule.activity.window.decorView)) {
            "SessionScreen must host the draft's EditText"
        }

    private fun findEditText(view: View): EditText? {
        if (view is EditText) {
            return view
        }
        if (view is ViewGroup) {
            for (index in 0 until view.childCount) {
                findEditText(view.getChildAt(index))?.let { return it }
            }
        }
        return null
    }

    @Test
    fun holdAndStopReplaceInterruptAndStopAndHoldPostsToSteps() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            server.respond(HOLD_PATH, StepsHoldResponse.getDefaultInstance())
            setStepScreen(server)

            composeRule.waitUntilExactlyOneExists(hasTestTag(HOLD_TAG), TIMEOUT_MILLIS)
            assertTrue(composeRule.onAllNodesWithTag(INTERRUPT_TAG).fetchSemanticsNodes().isEmpty())
            assertTrue(composeRule.onAllNodesWithTag(CONTROL_STOP_TAG).fetchSemanticsNodes().isEmpty())
            composeRule.onNodeWithTag(STOP_TAG).assertIsDisplayed()

            composeRule.onNodeWithTag(HOLD_TAG).performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { countPosts(server, HOLD_PATH) == 1 }

            val hold = server.requests.single { it.rawPath == HOLD_PATH }
            assertEquals(STEP_SESSION_ID, StepsHoldRequest.parseFrom(hold.body).id)
            assertTrue(sessionControlPosts(server).isEmpty())
        }
    }

    @Test
    fun stepStopAsksFirstThenPostsToStepsNotControl() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            server.respond(STEPS_STOP_PATH, StepsStopResponse.getDefaultInstance())
            setStepScreen(server)

            composeRule.waitUntilExactlyOneExists(hasTestTag(STOP_TAG), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag(STOP_TAG).performClick()
            composeRule.waitForIdle()
            composeRule.onNodeWithTag(CONFIRM_TAG).assertIsDisplayed()
            assertEquals(0, countPosts(server, STEPS_STOP_PATH))

            composeRule.onNodeWithTag(CONFIRM_TAG).performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { countPosts(server, STEPS_STOP_PATH) == 1 }

            val stop = server.requests.single { it.rawPath == STEPS_STOP_PATH }
            assertEquals(STEP_SESSION_ID, StepsStopRequest.parseFrom(stop.body).id)
            assertTrue(sessionControlPosts(server).isEmpty())
        }
    }

    @Test
    fun heldStepShowsNoHoldButtonButKeepsStop() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            setStepScreen(server, held = true)

            composeRule.waitUntilExactlyOneExists(hasTestTag(STOP_TAG), TIMEOUT_MILLIS)
            assertTrue(composeRule.onAllNodesWithTag(HOLD_TAG).fetchSemanticsNodes().isEmpty())
        }
    }

    @Test
    fun composeBoxHintsNudgeAndSaysAMessageIsANudge() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("done"))))
            setStepScreen(server)

            composeRule.waitUntilExactlyOneExists(hasTestTag("composer"), TIMEOUT_MILLIS)
            assertEquals(NUDGE_HINT, editField().hint.toString())
            composeRule.onNodeWithText(NUDGE_NOTE).assertIsDisplayed()
        }
    }

    @Test
    fun sendPostsANudgeWithTheTextAndShowsNudgeLanded() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("done"))))
            server.respond(NUDGE_PATH, StepsNudgeResponse.getDefaultInstance())
            setStepScreen(server)
            composeRule.waitUntilExactlyOneExists(hasTestTag("composer"), TIMEOUT_MILLIS)

            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.waitUntilExactlyOneExists(hasText(NUDGE_LANDED), TIMEOUT_MILLIS)

            val nudge = server.requests.single { it.rawPath == NUDGE_PATH }
            val sent = StepsNudgeRequest.parseFrom(nudge.body)
            assertEquals(STEP_SESSION_ID, sent.id)
            assertEquals(SYNTHETIC_DRAFT, sent.text)
            assertTrue(sessionControlPosts(server).isEmpty())
        }
    }

    @Test
    fun refusedNudgeShowsTheRefusalAndKeepsTheDraft() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("done"))))
            server.fail(NUDGE_PATH, Code.FAILED_PRECONDITION, REFUSAL_REASON)
            setStepScreen(server)
            composeRule.waitUntilExactlyOneExists(hasTestTag("composer"), TIMEOUT_MILLIS)

            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.waitUntilExactlyOneExists(hasText("refused: $REFUSAL_REASON"), TIMEOUT_MILLIS)

            assertEquals(SYNTHETIC_DRAFT, editField().text.toString())
        }
    }

    @Test
    fun noOpenRequestCardIsDrawnInStepMode() {
        FakeOrreryServer().use { server ->
            val request = OpenRequest.newBuilder()
                .setId(REQUEST_ID)
                .setKind("question")
                .setQuestion(QUESTION)
                .addAllOptions(listOf(OPTION_ONE, OPTION_TWO))
                .build()
            serveStream(
                server,
                streamOf(
                    delta(
                        seq = 1L,
                        items = listOf(item("reply", STEP_REPLY)),
                        state = stateOf("waiting", request),
                    ),
                ),
            )
            setStepScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText(STEP_REPLY), TIMEOUT_MILLIS)
            assertTrue(composeRule.onAllNodesWithTag(REQUEST_TAG).fetchSemanticsNodes().isEmpty())
        }
    }
}
