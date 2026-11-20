package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import com.google.protobuf.MessageLite
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.InterruptRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.InterruptResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.OpenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ResumeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ResumeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsHoldRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsHoldResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StopResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val PANE_ID = "%3"
private const val PROJECT = "project"
private const val STEP_LABEL = "synthetic step"
private const val SESSION_REASON = "synthetic network"
private const val REFUSAL_REASON = "synthetic refusal"
private const val REPLY_TIME = 1_700_000_000L
private const val CLOCK_NOW = 1_700_010_000L
private const val FIXTURE_CHANGED = 4102444800L
private const val FIXTURE_SEEN = 4102444900L
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val INTERRUPT_PATH = "/orrery.v1.Control/Interrupt"
private const val STOP_PATH = "/orrery.v1.Control/Stop"
private const val RESUME_PATH = "/orrery.v1.Control/Resume"
private const val HOLD_PATH = "/orrery.v1.Steps/Hold"
private const val STEPS_STOP_PATH = "/orrery.v1.Steps/Stop"
private const val PANE_STREAM_PATH = "/orrery.v1.Panes/WatchConversation"
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val PANE_CONVERSATION_PATH = "/orrery.v1.Panes/Conversation"
private const val PANE_SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val PANE_KEYS_PATH = "/orrery.v1.Panes/SendKeys"
private const val DOCK_STATE = "dock-state"
private const val DOCK_WORD = "dock-word"
private const val DOCK_INTERRUPT = "dock-interrupt"
private const val DOCK_RESUME = "dock-resume"
private const val DOCK_MORE = "dock-more"
private const val DOCK_STOP_CONFIRM = "dock-stop-confirm"

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class DockStateTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private fun setSession(server: FakeOrreryServer, reason: String = "", step: StepMode? = null) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client, step = step, reason = reason, clock = { CLOCK_NOW }) }
        }
    }

    private fun serveStream(server: FakeOrreryServer, vararg streams: Reply) {
        val requests = AtomicInteger()
        server.route(WATCH_PATH) { _ ->
            val index = requests.getAndIncrement()
            if (index < streams.size) streams[index] else Reply.Stream(emptyList(), StreamEnd.Open)
        }
    }

    private fun streamOf(vararg messages: MessageLite): Reply.Stream = Reply.Stream(messages.toList(), StreamEnd.Open)

    private fun stateOf(
        state: String,
        interrupting: Boolean = false,
        request: OpenRequest? = null,
        turnSince: Long = 0L,
        interruptingSince: Long = REPLY_TIME,
    ): ConversationState {
        val builder = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setInterrupting(interrupting)
            .setInterruptingSince(if (interrupting) interruptingSince else 0L)
            .setTurnSince(turnSince)
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(SESSION_ID)
                    .setDir("/home/user/$PROJECT")
                    .setLabel("example-label")
                    .setState(state)
                    .setChanged(FIXTURE_CHANGED)
                    .setSeen(FIXTURE_SEEN),
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
            .setTranscript(SESSION_ID)
            .setSeq(seq)
            .addAllItems(items)
        state?.let { builder.setState(it) }
        return builder.build()
    }

    private fun item(kind: String, text: String = "", time: Long = 0L): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind(kind).setTime(time).setText(text).build()

    private fun toolCall(id: String, name: String, input: String): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind("tool").setTime(REPLY_TIME).setTool(name).setInput(input).setId(id).build()

    /** The dock's state line, matched by [inside]: the conversation draws a tool call's name and input too. */
    private fun dockState(inside: SemanticsMatcher): SemanticsNodeInteraction =
        composeRule.onNode(hasTestTag(DOCK_STATE) and inside)

    private fun countPosts(server: FakeOrreryServer, path: String): Int =
        server.requests.count { it.method == "POST" && it.rawPath == path }

    private fun requestBody(server: FakeOrreryServer, path: String): ByteArray =
        server.requests.single { it.method == "POST" && it.rawPath == path }.body

    @Test
    fun workingSessionDrawsItsToolAndInterruptCallsControlInterrupt() {
        FakeOrreryServer().use { server ->
            serveStream(
                server,
                streamOf(
                    delta(
                        seq = 1L,
                        items = listOf(toolCall("tool-1", "Bash", "go test ./internal/...")),
                        state = stateOf("working", turnSince = CLOCK_NOW - 3_725L),
                    ),
                ),
            )
            server.respond(INTERRUPT_PATH, InterruptResponse.getDefaultInstance())
            setSession(server)

            composeRule.awaitExactlyOne(hasTestTag(DOCK_INTERRUPT))
            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("working")
            dockState(hasAnyDescendant(hasText("Bash"))).assertIsDisplayed()
            dockState(hasAnyDescendant(hasText("go test ./internal/..."))).assertIsDisplayed()
            dockState(hasAnyDescendant(hasText("1:02:05"))).assertIsDisplayed()
            composeRule.onNodeWithTag(DOCK_INTERRUPT).performClick()
            composeRule.awaitUntil { countPosts(server, INTERRUPT_PATH) == 1 }
            assertEquals(
                InterruptRequest.newBuilder().setId(SESSION_ID).build(),
                InterruptRequest.parseFrom(requestBody(server, INTERRUPT_PATH)),
            )
        }
    }

    @Test
    fun interruptingIsDrawnFromTheStreamAndSpent() {
        FakeOrreryServer().use { server ->
            serveStream(
                server,
                streamOf(delta(seq = 1L, state = stateOf("working", interrupting = true, interruptingSince = CLOCK_NOW - 65L))),
            )
            setSession(server)

            composeRule.awaitExactlyOne(hasText("interrupting…"))
            dockState(hasAnyDescendant(hasText("1:05"))).assertIsDisplayed()
            composeRule.onNodeWithTag(DOCK_INTERRUPT).assertIsNotEnabled()
            composeRule.onNodeWithTag(DOCK_INTERRUPT).performClick()
            composeRule.waitForIdle()
            assertEquals(0, countPosts(server, INTERRUPT_PATH))
        }
    }

    @Test
    fun interruptedByYouComesFromTheStreamsNewestInterruptedItem() {
        FakeOrreryServer().use { server ->
            serveStream(
                server,
                streamOf(
                    delta(
                        seq = 1L,
                        items = listOf(item("prompt", "synthetic prompt"), item("interrupted", time = REPLY_TIME)),
                        state = stateOf("done"),
                    ),
                ),
            )
            setSession(server)

            composeRule.awaitExactlyOne(hasText("interrupted by you"))
            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("interrupted by you")
            composeRule.onAllNodesWithTag(DOCK_INTERRUPT).assertCountEquals(0)
            composeRule.onAllNodesWithTag(DOCK_RESUME).assertCountEquals(0)
        }
    }

    @Test
    fun cutOffSessionResumesThroughControlResume() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("interrupted"))))
            server.respond(RESUME_PATH, ResumeResponse.getDefaultInstance())
            setSession(server, reason = SESSION_REASON)

            composeRule.awaitExactlyOne(hasTestTag(DOCK_RESUME))
            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("cut off")
            composeRule.onNodeWithText(SESSION_REASON, substring = true).assertIsDisplayed()
            composeRule.onNodeWithTag(DOCK_RESUME).performClick()
            composeRule.awaitUntil { countPosts(server, RESUME_PATH) == 1 }
            assertEquals(
                ResumeRequest.newBuilder().setId(SESSION_ID).build(),
                ResumeRequest.parseFrom(requestBody(server, RESUME_PATH)),
            )
        }
    }

    @Test
    fun refusedResumeShowsTheServersReason() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("interrupted"))))
            server.fail(RESUME_PATH, Code.FAILED_PRECONDITION, REFUSAL_REASON)
            setSession(server)

            composeRule.awaitExactlyOne(hasTestTag(DOCK_RESUME))
            composeRule.onNodeWithTag(DOCK_RESUME).performClick()
            composeRule.awaitExactlyOne(hasText("Not resumed: $REFUSAL_REASON"))
        }
    }

    @Test
    fun failedSessionShowsTheListedReason() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("failed"))))
            setSession(server, reason = SESSION_REASON)

            composeRule.awaitExactlyOne(hasText("failed"))
            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("failed")
            composeRule.onNodeWithText(SESSION_REASON, substring = true).assertIsDisplayed()
            composeRule.onAllNodesWithTag(DOCK_INTERRUPT).assertCountEquals(0)
        }
    }

    @Test
    fun stoppedSessionDrawsNoControls() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("stopped"))))
            setSession(server)

            composeRule.awaitExactlyOne(hasText("stopped"))
            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("stopped")
            composeRule.onAllNodesWithTag(DOCK_INTERRUPT).assertCountEquals(0)
            composeRule.onAllNodesWithTag(DOCK_RESUME).assertCountEquals(0)
            composeRule.onAllNodesWithTag(DOCK_MORE).assertCountEquals(0)
        }
    }

    @Test
    fun sessionMoreAsksBeforeControlStopThenPostsOnce() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            server.respond(STOP_PATH, StopResponse.getDefaultInstance())
            setSession(server)

            composeRule.awaitExactlyOne(hasTestTag(DOCK_MORE))
            composeRule.onNodeWithTag(DOCK_MORE).performClick()
            composeRule.onNodeWithText("Stop $PROJECT?").assertIsDisplayed()
            composeRule.onNodeWithTag("dock-keep-running").assertIsDisplayed()
            composeRule.onNodeWithTag(DOCK_STOP_CONFIRM).assertTextEquals("Stop $PROJECT")
            assertEquals(0, countPosts(server, STOP_PATH))

            composeRule.onNodeWithTag(DOCK_STOP_CONFIRM).performClick()
            composeRule.awaitUntil { countPosts(server, STOP_PATH) == 1 }
            assertEquals(
                StopRequest.newBuilder().setId(SESSION_ID).build(),
                StopRequest.parseFrom(requestBody(server, STOP_PATH)),
            )
        }
    }

    @Test
    fun failedInterruptShowsCouldNotInterrupt() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            server.fail(INTERRUPT_PATH, Code.INTERNAL_ERROR, "synthetic failure")
            setSession(server)

            composeRule.awaitExactlyOne(hasTestTag(DOCK_INTERRUPT))
            composeRule.onNodeWithTag(DOCK_INTERRUPT).performClick()
            composeRule.awaitExactlyOne(hasText("could not interrupt"))
        }
    }

    @Test
    fun interruptedTheStateIsDrawnOnlyByTheDock() {
        FakeOrreryServer().use { server ->
            serveStream(
                server,
                streamOf(
                    delta(
                        seq = 1L,
                        items = listOf(item("interrupted", time = REPLY_TIME)),
                        state = stateOf("done"),
                    ),
                ),
            )
            setSession(server, reason = SESSION_REASON)

            composeRule.awaitExactlyOne(hasText("interrupted by you"))
            composeRule.onAllNodesWithText("interrupted by you").assertCountEquals(1)
            dockState(hasAnyDescendant(hasText("interrupted by you"))).assertIsDisplayed()
            composeRule.onAllNodesWithText("Interrupted").assertCountEquals(0)
        }
    }

    @Test
    fun dockControlsDrawTwentyFourHigh() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            setSession(server, step = StepMode(held = false, label = STEP_LABEL))

            composeRule.awaitExactlyOne(hasTestTag(DOCK_INTERRUPT))
            composeRule.onNodeWithTag(DOCK_INTERRUPT).assertHeightIsEqualTo(OrreryTokens.Space.twentyFour)
            composeRule.onNodeWithTag(DOCK_MORE).assertHeightIsEqualTo(OrreryTokens.Space.twentyFour)
        }
    }

    @Test
    fun stepInterruptHoldsTheStepAndShowsHoldLanded() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            server.respond(HOLD_PATH, StepsHoldResponse.getDefaultInstance())
            setSession(server, step = StepMode(held = false, label = STEP_LABEL))

            composeRule.awaitExactlyOne(hasTestTag(DOCK_INTERRUPT))
            composeRule.onNodeWithText("running").assertIsDisplayed()
            composeRule.onNodeWithTag(DOCK_INTERRUPT).performClick()
            composeRule.awaitExactlyOne(hasText("hold landed"))

            assertEquals(1, countPosts(server, HOLD_PATH))
            assertEquals(
                StepsHoldRequest.newBuilder().setId(SESSION_ID).build(),
                StepsHoldRequest.parseFrom(requestBody(server, HOLD_PATH)),
            )
            assertEquals(0, countPosts(server, INTERRUPT_PATH))
        }
    }

    @Test
    fun refusedHoldShowsTheRefusalReason() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            server.fail(HOLD_PATH, Code.FAILED_PRECONDITION, REFUSAL_REASON)
            setSession(server, step = StepMode(held = false, label = STEP_LABEL))

            composeRule.awaitExactlyOne(hasTestTag(DOCK_INTERRUPT))
            composeRule.onNodeWithTag(DOCK_INTERRUPT).performClick()
            composeRule.awaitExactlyOne(hasText("refused: $REFUSAL_REASON"))
        }
    }

    @Test
    fun droppedHoldShowsCouldNotReachTheStep() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            server.drop(HOLD_PATH)
            setSession(server, step = StepMode(held = false, label = STEP_LABEL))

            composeRule.awaitExactlyOne(hasTestTag(DOCK_INTERRUPT))
            composeRule.onNodeWithTag(DOCK_INTERRUPT).performClick()
            composeRule.awaitExactlyOne(hasText("could not reach the step"))
        }
    }

    @Test
    fun heldStepDrawsHeldAndNoInterrupt() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            setSession(server, step = StepMode(held = true, label = STEP_LABEL))

            composeRule.awaitExactlyOne(hasTestTag(DOCK_MORE))
            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("held")
            composeRule.onAllNodesWithTag(DOCK_INTERRUPT).assertCountEquals(0)
        }
    }

    @Test
    fun stepMoreAsksBeforeStepsStopThenPostsOnce() {
        FakeOrreryServer().use { server ->
            serveStream(server, streamOf(delta(seq = 1L, state = stateOf("working"))))
            server.respond(STEPS_STOP_PATH, StepsStopResponse.getDefaultInstance())
            setSession(server, step = StepMode(held = false, label = STEP_LABEL))

            composeRule.awaitExactlyOne(hasTestTag(DOCK_MORE))
            composeRule.onNodeWithTag(DOCK_MORE).performClick()
            composeRule.onNodeWithText("Stop $STEP_LABEL?").assertIsDisplayed()
            assertEquals(0, countPosts(server, STEPS_STOP_PATH))

            composeRule.onNodeWithTag(DOCK_STOP_CONFIRM).performClick()
            composeRule.awaitExactlyOne(hasText("stopped"))
            assertEquals(1, countPosts(server, STEPS_STOP_PATH))
            assertEquals(
                StepsStopRequest.newBuilder().setId(SESSION_ID).build(),
                StepsStopRequest.parseFrom(requestBody(server, STEPS_STOP_PATH)),
            )
            assertEquals(0, countPosts(server, STOP_PATH))
        }
    }

    @Test
    fun paneDrawsItsStateFromTheDockAndInterruptSendsEscape() {
        FakeOrreryServer().use { server ->
            val tool = """{"id":"synthetic-tool","name":"Bash","input":"echo synthetic","start":4102444700}"""
            server.respond(PANES_PATH, panes(state = "working", tool = tool))
            server.respond(
                PANE_CONVERSATION_PATH,
                ConversationResponse.newBuilder()
                    .setOrigin("synthetic origin")
                    .addItems(item("reply", "synthetic reply"))
                    .build(),
            )
            server.respond(PANE_SEEN_PATH, MarkSeenResponse.getDefaultInstance())
            server.respond(PANE_KEYS_PATH, SendKeysResponse.getDefaultInstance())
            server.route(PANE_STREAM_PATH) {
                Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
            }
            val client = OrreryClient(server.baseUrl)
            composeRule.setContent {
                MaterialTheme { PaneScreen(PANE_ID, client) }
            }

            composeRule.awaitExactlyOne(hasTestTag(DOCK_INTERRUPT))
            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("working")
            composeRule.onNodeWithText("Bash").assertIsDisplayed()
            composeRule.onAllNodesWithText("working").assertCountEquals(1)
            dockState(hasAnyDescendant(hasText("working"))).assertIsDisplayed()
            composeRule.onNodeWithTag(DOCK_INTERRUPT).performClick()
            composeRule.awaitUntil { countPosts(server, PANE_KEYS_PATH) == 1 }
        }
    }

    private fun panes(state: String, tool: String): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(
            PaneProto.newBuilder()
                .setId(PANE_ID)
                .setHost("example-host")
                .setSession("example-session")
                .setWindow(1)
                .setPane(2)
                .setCwd("/home/user/$PROJECT")
                .setCommand("claude")
                .setState(state)
                .setSummary("Example summary.")
                .setLabel("example-label")
                .setChanged(FIXTURE_CHANGED)
                .setSeen(FIXTURE_SEEN)
                .setTool(tool),
        )
        .build()
}
