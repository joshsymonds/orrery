package io.github.joshsymonds.orrery.ui

import android.os.Looper
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performScrollToIndex
import androidx.lifecycle.Lifecycle
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import com.connectrpc.Code
import com.google.protobuf.ByteString
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitNone
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.awaitUntilAfterIdle
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationContext as ConversationContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStat as HostStatProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ScreenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val PANE_ID = "%3"
private const val SCREEN_PATH = "/orrery.v1.Panes/Screen"
private const val SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val KEYS_PATH = "/orrery.v1.Panes/SendKeys"
private const val SEND_PATH = "/orrery.v1.Panes/SendText"
private const val CONVERSATION_PATH = "/orrery.v1.Panes/Conversation"
private const val HOST_PATH = "/orrery.v1.Host/Status"
private const val STREAM_PATH = "/orrery.v1.Panes/WatchConversation"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val EPIC_ID = "example-repo/rota-export"
private const val FIXED_NOW_SECONDS = 1_800_000_000L
private const val FIXTURE_SEEN = 4102444900L
// Made-up unix seconds for the seen-at-open tests: the pane's seen time sits between the two replies.
private const val OLDER_REPLY_TIME = 1_700_000_000L
private const val NEWER_REPLY_TIME = 1_700_001_000L
private const val SEEN_BETWEEN_REPLIES = 1_700_000_500L
private const val SEEN_AFTER_REPLIES = 1_700_002_000L
private const val SYNTHETIC_DRAFT = "synthetic draft"
private const val CLAUDE_HINT = "Message project"
private const val SHELL_HINT = "Type a command…"
private const val WORKING_HINT = "Queue for project"
private const val WAITING_HINT = "Answer project"
private const val DOCK_STATE_TAG = "dock-state"
private const val DOCK_WORD_TAG = "dock-word"
private const val BAND_GOAL_TAG = "band-goal"
private const val NO_CONVERSATION_TAG = "no-conversation"
private const val HOST_METERS_TAG = "host-meters"
private val HOST_BODY: HostStatusResponse = HostStatusResponse.newBuilder()
    .setHost("example-host")
    .addStats(HostStatProto.newBuilder().setLabel("cpu").setValue("12%").setPercent(12.0).setLevel("ok"))
    .build()
private const val HEAD_TAG = "head"
private const val CONVERSATION_LIST_TAG = "conversation-list"
private const val REPLY_COUNT = 40
private const val SHORT_REPLY_COUNT = 3
private const val EDGE_TOLERANCE_PX = 0.5f

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PaneScreenTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val toolQuestion = """{"tool":"Bash","input":{"command":"echo synthetic"}}"""

    private fun paneList(
        state: String,
        tool: String = "",
        question: String = "",
        seen: Long = FIXTURE_SEEN,
    ): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(
            PaneProto.newBuilder()
                .setId("%3")
                .setHost("example-host")
                .setSession("example-session")
                .setWindow(1)
                .setPane(2)
                .setCwd("/home/user/project")
                .setCommand("claude")
                .setState(state)
                .setSummary("Example summary.")
                .setLabel("example-label")
                .setChanged(4102444800L)
                .setSeen(seen)
                .setQuestion(question)
                .setQuestionHash("abc123")
                .setTool(tool),
        )
        .build()

    private fun screenOf(text: String): ScreenResponse =
        ScreenResponse.newBuilder().setScreen(ByteString.copyFromUtf8(text)).build()

    private fun serve(server: FakeOrreryServer, question: String = "") {
        server.respond(PANES_PATH, paneList(state = "waiting", question = question))
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
    }

    private fun setScreen(server: FakeOrreryServer) {
        // These tests cover the polled screen. The stream is answered as orreryd answers a pane it does not stream, so
        // the screen polls at once; an unanswered stream would be retried three times a second apart first, and the
        // first render would wait out those reconnect delays.
        server.route(STREAM_PATH) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client) }
        }
    }

    private fun screenRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == SCREEN_PATH }

    private fun paneListRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == PANES_PATH }

    private fun seenRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == SEEN_PATH }

    /** The read marks sent so far, as their `read_up_to` times, oldest first. */
    private fun readMarks(server: FakeOrreryServer): List<Long> =
        server.requests
            .filter { it.method == "POST" && it.rawPath == SEEN_PATH }
            .map { mark ->
                MarkSeenRequest.parseFrom(mark.body).let { request ->
                    if (request.hasReadUpTo()) request.readUpTo else -1L
                }
            }

    private fun timedReply(time: Long, text: String): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind("reply").setTime(time).setText(text).build()

    private fun conversationRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == CONVERSATION_PATH }

    /** Scripts the pane list and, when given, a 200 conversation. The screen and host routes stay unscripted. */
    private fun servePane(
        server: FakeOrreryServer,
        state: String = "waiting",
        tool: String = "",
        question: String = "",
        conversation: ConversationResponse? = null,
        seen: Long = FIXTURE_SEEN,
    ) {
        server.respond(PANES_PATH, paneList(state, tool, question, seen))
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
        if (conversation != null) {
            server.respond(CONVERSATION_PATH, conversation)
        }
    }

    private fun setClockedScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client, clock = { FIXED_NOW_SECONDS }) }
        }
    }

    private fun conversationItem(kind: String, text: String): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind(kind).setTime(0).setText(text).build()

    private fun conversationBody(vararg items: ConversationItemProto): ConversationResponse =
        ConversationResponse.newBuilder().setOrigin("synthetic origin").addAllItems(items.toList()).build()

    private fun conversationBodyWithGoal(goal: String, vararg items: ConversationItemProto): ConversationResponse =
        conversationBody(*items).toBuilder().setGoal(goal).build()

    private fun waitingItem(text: String, canceled: Boolean = false): WaitingMessage =
        WaitingMessage.newBuilder().setText(text).setTime(0).setCanceled(canceled).build()

    private fun conversationBodyWithWaiting(
        waiting: List<WaitingMessage>,
        vararg items: ConversationItemProto,
    ): ConversationResponse = conversationBody(*items).toBuilder().addAllWaiting(waiting).build()

    private fun toolItem(id: String, name: String): ConversationItemProto =
        ConversationItemProto.newBuilder()
            .setKind("tool")
            .setTime(0)
            .setTool(name)
            .setInput("echo synthetic")
            .setId(id)
            .build()

    private fun noticeItem(sort: String, text: String): ConversationItemProto =
        ConversationItemProto.newBuilder()
            .setKind("notice")
            .setTime(0)
            .setText(text)
            .setNotice(sort)
            .build()

    @Test
    fun answerCarryingContextShowsTheContextMeterInTheHeader() {
        FakeOrreryServer().use { server ->
            val body = conversationBody(conversationItem("reply", "synthetic reply"))
                .toBuilder()
                .setContext(ConversationContextProto.newBuilder().setTokens(400000L).setWindow(1000000L))
                .build()
            servePane(server, state = "done", conversation = body)
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("head-context"))
            composeRule.onNodeWithTag("head-context").assertTextEquals("▰▰▱▱▱ 40%")
        }
    }

    @Test
    fun openingShowsNoConversationAndSendsNoSeenRequest() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))
            composeRule.waitForIdle()

            composeRule.onNodeWithTag(NO_CONVERSATION_TAG).assertIsDisplayed()
            assertEquals(0, seenRequests(server))
        }
    }

    private fun twoReplies(): ConversationResponse = conversationBody(
        timedReply(OLDER_REPLY_TIME, "synthetic older reply"),
        timedReply(NEWER_REPLY_TIME, "synthetic newer reply"),
    )

    /** The divider sits between the bottom of entry row [below] - 1 and the top of entry row [below]. */
    private fun assertDividerBeforeRow(below: Int) {
        composeRule.onAllNodesWithTag("rule:new").assertCountEquals(1)
        val divider = composeRule.onNodeWithTag("rule:new").fetchSemanticsNode().boundsInRoot
        val row = composeRule.onNodeWithTag("entry-row:$below").fetchSemanticsNode().boundsInRoot
        assertTrue("the divider ends above entry row $below", divider.bottom <= row.top + 1f)
        val previous = composeRule.onNodeWithTag("entry-row:${below - 1}").fetchSemanticsNode().boundsInRoot
        assertTrue("the divider starts below entry row ${below - 1}", divider.top >= previous.bottom - 1f)
    }

    @Test
    fun dividerSitsBeforeTheFirstReplyNewerThanTheSeenTimeReadOnOpen() {
        FakeOrreryServer().use { server ->
            servePane(server, seen = SEEN_BETWEEN_REPLIES, conversation = twoReplies())
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("rule:new"))
            assertDividerBeforeRow(below = 2)
            composeRule.onNodeWithTag("entry-row:2")
                .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "new"))
            composeRule.onNodeWithTag("entry-row:1")
                .assert(SemanticsMatcher.keyNotDefined(SemanticsProperties.StateDescription))
        }
    }

    @Test
    fun laterPollWithNewerSeenTimeLeavesTheDividerWhereItWasOnOpen() {
        FakeOrreryServer().use { server ->
            servePane(server, seen = SEEN_BETWEEN_REPLIES, conversation = twoReplies())
            setScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("rule:new"))
            assertDividerBeforeRow(below = 2)

            // The later poll also turns the pane to working. Its composer edge is drawn from the same
            // state write that would move the seen time, so the edge shows the later poll has landed.
            server.respond(PANES_PATH, paneList(state = "working", seen = SEEN_AFTER_REPLIES))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitExactlyOne(hasTestTag("composer-edge:working"))

            assertDividerBeforeRow(below = 2)
        }
    }

    @Test
    fun seenTimeAtTheNewestReplyShowsNoDivider() {
        FakeOrreryServer().use { server ->
            servePane(server, seen = NEWER_REPLY_TIME, conversation = twoReplies())
            setScreen(server)

            composeRule.awaitExactlyOne(hasText("synthetic newer reply"))
            composeRule.onAllNodesWithTag("rule:new").assertCountEquals(0)
        }
    }

    @Test
    fun aTimedReplyShownForASecondSendsItsTimeAfterThePaneRead() {
        FakeOrreryServer().use { server ->
            servePane(server, seen = SEEN_BETWEEN_REPLIES, conversation = twoReplies())
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic newer reply"))

            composeRule.mainClock.advanceTimeBy(2_000)
            composeRule.awaitUntil { readMarks(server).size == 1 }

            assertEquals(listOf(NEWER_REPLY_TIME), readMarks(server))
            val firstPaneRead = server.requests.indexOfFirst { it.method == "POST" && it.rawPath == PANES_PATH }
            val mark = server.requests.indexOfFirst { it.method == "POST" && it.rawPath == SEEN_PATH }
            assertTrue("the pane is read before the mark is sent", firstPaneRead in 0 until mark)
        }
    }

    @Test
    fun aLaterNewerReadSendsTheNewerTime() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                seen = SEEN_AFTER_REPLIES,
                conversation = conversationBody(timedReply(OLDER_REPLY_TIME, "synthetic older reply")),
            )
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic older reply"))
            composeRule.mainClock.advanceTimeBy(2_000)
            composeRule.awaitUntil { readMarks(server).size == 1 }

            server.respond(CONVERSATION_PATH, twoReplies())
            composeRule.awaitExactlyOne(hasText("synthetic newer reply"))
            composeRule.mainClock.advanceTimeBy(2_000)
            composeRule.awaitUntil { readMarks(server).size == 2 }

            assertEquals(listOf(OLDER_REPLY_TIME, NEWER_REPLY_TIME), readMarks(server))
        }
    }

    @Test
    fun aFailingReadMarkLeavesTheScreenWorking() {
        FakeOrreryServer().use { server ->
            servePane(server, seen = SEEN_BETWEEN_REPLIES, conversation = twoReplies())
            server.fail(SEEN_PATH, Code.INTERNAL_ERROR)
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic newer reply"))

            composeRule.mainClock.advanceTimeBy(2_000)
            composeRule.awaitUntil { readMarks(server).size == 1 }
            composeRule.waitForIdle()
            assertEquals(listOf(NEWER_REPLY_TIME), readMarks(server))
            composeRule.onNodeWithText("synthetic newer reply").assertIsDisplayed()
            composeRule.onAllNodesWithText("could not refresh").assertCountEquals(0)

            server.respond(PANES_PATH, paneList(state = "working"))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitExactlyOne(hasTestTag("composer-edge:working"))
        }
    }

    @Test
    fun paneRefreshesEverySecondWhileVisible() {
        FakeOrreryServer().use { server ->
            serve(server)
            // The read's last call is the host's status; the main thread's first state write after that call arrives
            // is the read's result, and the refresh's one-second timer starts in that same main-thread step.
            val main = Looper.getMainLooper()
            val mainWrites = AtomicInteger()
            val writesWhenTheReadEnded = AtomicInteger(-1)
            server.route(HOST_PATH) {
                if (paneListRequests(server) == 1) writesWhenTheReadEnded.compareAndSet(-1, mainWrites.get())
                Reply.Failure(Code.NOT_FOUND, "", 0L)
            }
            val countMainWrites = Snapshot.registerGlobalWriteObserver {
                if (Looper.myLooper() == main) mainWrites.incrementAndGet()
            }
            try {
                // Only the test moves the clock, from before the screen starts, so the timer starts at the time the
                // test then advances from.
                composeRule.mainClock.autoAdvance = false
                setScreen(server)
                composeRule.awaitUntilAfterIdle {
                    writesWhenTheReadEnded.get() >= 0 && mainWrites.get() > writesWhenTheReadEnded.get()
                }
            } finally {
                countMainWrites.dispose()
            }
            assertEquals(1, paneListRequests(server))

            // ignoreFrameDuration: a plain advance rounds up to whole 16 ms frames, so "999" would be 1008.
            composeRule.mainClock.advanceTimeBy(999, ignoreFrameDuration = true)
            composeRule.waitForIdle()
            composeRule.onNodeWithTag(NO_CONVERSATION_TAG).assertExists()
            assertEquals(1, paneListRequests(server))

            composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
            composeRule.awaitUntilAfterIdle { paneListRequests(server) == 2 }
        }
    }

    @Test
    fun noRequestsBelowStartedAndPollingResumesOnReturn() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))

            composeRule.activityRule.scenario.moveToState(Lifecycle.State.CREATED)
            composeRule.waitForIdle()
            val stoppedAt = paneListRequests(server)
            composeRule.mainClock.advanceTimeBy(3_000)
            composeRule.waitForIdle()
            assertEquals(stoppedAt, paneListRequests(server))

            composeRule.activityRule.scenario.moveToState(Lifecycle.State.RESUMED)
            composeRule.awaitUntil { paneListRequests(server) > stoppedAt }
        }
    }

    @Test
    fun failedTickKeepsTheLineAndNextGoodTickClearsMessage() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))

            server.fail(PANES_PATH, Code.INTERNAL_ERROR)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitExactlyOne(hasText("could not refresh"))
            composeRule.onNodeWithTag(NO_CONVERSATION_TAG).assertIsDisplayed()

            server.respond(PANES_PATH, paneList(state = "waiting"))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitNone(hasText("could not refresh"))
            composeRule.onNodeWithTag(NO_CONVERSATION_TAG).assertIsDisplayed()
        }
    }

    @Test
    fun paneWithQuestionDrawsItsAsk() {
        FakeOrreryServer().use { server ->
            serve(server, question = toolQuestion)
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("ask"))
            composeRule.onNodeWithTag("ask-question").assertTextEquals("Run this command?")
        }
    }

    @Test
    fun paneWithoutQuestionDrawsNoAsk() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))
            composeRule.onAllNodesWithTag("ask").assertCountEquals(0)
        }
    }

    @Test
    fun askAnswerRefreshesPaneImmediately() {
        FakeOrreryServer().use { server ->
            serve(server, question = toolQuestion)
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("ask-option:1"))
            assertEquals(1, paneListRequests(server))

            composeRule.mainClock.autoAdvance = false
            composeRule.onNodeWithTag("ask-option:1").performClick()
            composeRule.awaitUntilAfterIdle { paneListRequests(server) == 2 }
        }
    }

    @Test
    fun sendAndQuickKeysAreShown() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))
            composeRule.onNodeWithText("Send").assertIsDisplayed()
            composeRule.onNodeWithText("Esc").assertExists()
        }
    }

    @Test
    fun quickKeysHideWhileTheDraftHasTextAndReturnWhenItClears() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))
            composeRule.onNodeWithText("Esc").assertExists()

            setDraft(SYNTHETIC_DRAFT)
            composeRule.awaitNone(hasText("Esc"))

            setDraft("")
            composeRule.awaitExactlyOne(hasText("Esc"))
        }
    }

    /** Opens a pane in [state] and asserts the composer's hint once the pane has loaded. */
    private fun assertPaneHint(state: String, expected: String) {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = state,
                conversation = conversationBody(conversationItem("reply", "synthetic reply")),
            )
            setScreen(server)
            composeRule.awaitExactlyOne(hasTestTag(HEAD_TAG))
            composeRule.waitForIdle()

            assertEquals(expected, editField().hint?.toString())
        }
    }

    @Test
    fun noPaneHintIsTheShellHint() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_PATH, PanesListResponse.getDefaultInstance())
            setScreen(server)
            composeRule.awaitUntil { paneListRequests(server) >= 1 }
            composeRule.waitForIdle()

            assertEquals(SHELL_HINT, editField().hint?.toString().orEmpty())
        }
    }

    @Test
    fun donePaneHintMessagesClaude() = assertPaneHint(state = "done", expected = CLAUDE_HINT)

    @Test
    fun workingPaneHintQueuesForClaude() = assertPaneHint(state = "working", expected = WORKING_HINT)

    @Test
    fun waitingPaneHintAnswersClaude() = assertPaneHint(state = "waiting", expected = WAITING_HINT)

    @Test
    fun shellPaneHintNamesACommand() = assertPaneHint(state = "", expected = SHELL_HINT)

    @Test
    fun shellPaneDocksAsIdle() {
        FakeOrreryServer().use { server ->
            servePane(server, state = "")
            setScreen(server)
            composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))
            composeRule.onNodeWithTag(HEAD_TAG).assertExists()

            composeRule.onNodeWithTag(DOCK_WORD_TAG).assertTextEquals("idle")
        }
    }

    private fun setDraft(text: String) {
        composeRule.runOnUiThread { editField().setText(text) }
        composeRule.waitForIdle()
    }

    private fun editField(): EditText =
        checkNotNull(findEditText(composeRule.activity.window.decorView)) {
            "PaneScreen must host the draft's EditText"
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
    fun claudePaneShowsItsConversationAndRequestsNoScreen() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                conversation = conversationBody(
                    conversationItem("prompt", "synthetic prompt"),
                    conversationItem("reply", "synthetic reply"),
                ),
            )
            setScreen(server)

            composeRule.awaitExactlyOne(hasText("synthetic reply"))
            composeRule.onNodeWithText("synthetic prompt").assertIsDisplayed()
            composeRule.onAllNodesWithTag(NO_CONVERSATION_TAG).assertCountEquals(0)
            assertEquals(0, screenRequests(server))
        }
    }

    @Test
    fun paneWithEmptyStateShowsNoConversationAndNeverRequestsConversation() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "",
                conversation = conversationBody(conversationItem("reply", "synthetic reply")),
            )
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))
            composeRule.onNodeWithTag(HEAD_TAG).assertExists()
            composeRule.onNodeWithTag(DOCK_WORD_TAG).assertTextEquals("idle")
            assertEquals(0, conversationRequests(server))
        }
    }

    @Test
    fun emptyStatePaneShowsNoConversationAndRequestsNoScreen() {
        FakeOrreryServer().use { server ->
            servePane(server, state = "")
            server.respond(SCREEN_PATH, screenOf("synthetic terminal body"))
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))
            composeRule.onNodeWithTag(NO_CONVERSATION_TAG).assertTextEquals("no conversation")
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitUntil { paneListRequests(server) >= 2 }

            assertEquals(0, screenRequests(server))
            composeRule.onAllNodesWithText("synthetic terminal body").assertCountEquals(0)
        }
    }

    @Test
    fun paneFlipsBetweenConversationAndNoConversationOnEachPoll() {
        FakeOrreryServer().use { server ->
            val replyBody = conversationBody(conversationItem("reply", "synthetic reply"))
            servePane(server, conversation = replyBody)
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply"))

            server.fail(CONVERSATION_PATH, Code.NOT_FOUND)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))
            composeRule.onNodeWithText("synthetic reply").assertDoesNotExist()

            server.respond(CONVERSATION_PATH, replyBody)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitExactlyOne(hasText("synthetic reply"))
            composeRule.onAllNodesWithTag(NO_CONVERSATION_TAG).assertCountEquals(0)
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun askSitsAfterTheLastConversationItem() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                question = toolQuestion,
                conversation = conversationBody(
                    conversationItem("prompt", "synthetic prompt"),
                    conversationItem("reply", "synthetic last reply"),
                ),
            )
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("ask"))
            val reply = composeRule.onNodeWithText("synthetic last reply").fetchSemanticsNode().boundsInRoot
            val ask = composeRule.onNodeWithTag("ask").fetchSemanticsNode().boundsInRoot
            assertTrue(ask.top >= reply.bottom)
        }
    }

    @Test
    fun workingStatusShowsElapsedTimeSinceCurrentToolStarted() {
        FakeOrreryServer().use { server ->
            val started = FIXED_NOW_SECONDS - 12
            val tool = """{"id":"synthetic-tool","name":"Bash","input":"echo synthetic","start":$started}"""
            servePane(
                server,
                state = "working",
                tool = tool,
                conversation = conversationBody(conversationItem("reply", "synthetic reply")),
            )
            setClockedScreen(server)

            composeRule.awaitExactlyOne(hasText("synthetic reply"))
            composeRule.onNodeWithText("0:12", useUnmergedTree = true).assertExists()
        }
    }

    @Test
    fun conversationFailureKeepsConversationAndSaysCouldNotRefresh() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationBody(conversationItem("reply", "synthetic reply")))
            setClockedScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply"))

            server.fail(CONVERSATION_PATH, Code.INTERNAL_ERROR)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitExactlyOne(hasText("could not refresh"))
            composeRule.onNodeWithText("synthetic reply").assertIsDisplayed()
            composeRule.onAllNodesWithTag(NO_CONVERSATION_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun hostStatsShowMeters() {
        FakeOrreryServer().use { server ->
            servePane(server)
            server.respond(HOST_PATH, HOST_BODY)
            setClockedScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(HOST_METERS_TAG))
        }
    }

    @Test
    fun hostNotFoundRemovesMeters() {
        FakeOrreryServer().use { server ->
            servePane(server)
            server.respond(HOST_PATH, HOST_BODY)
            setClockedScreen(server)
            composeRule.awaitExactlyOne(hasTestTag(HOST_METERS_TAG))

            server.fail(HOST_PATH, Code.NOT_FOUND)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitNone(hasTestTag(HOST_METERS_TAG))
        }
    }

    @Test
    fun failedHostCallKeepsLastMeters() {
        FakeOrreryServer().use { server ->
            servePane(server)
            server.respond(HOST_PATH, HOST_BODY)
            setClockedScreen(server)
            composeRule.awaitExactlyOne(hasTestTag(HOST_METERS_TAG))

            server.fail(HOST_PATH, Code.INTERNAL_ERROR)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitUntil { paneListRequests(server) == 2 }
            // A read starts only after the read before it has returned its host call, so the third
            // read means the failed host call has been handled.
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitUntil { paneListRequests(server) == 3 }
            composeRule.onAllNodesWithTag(HOST_METERS_TAG).assertCountEquals(1)
            composeRule.onNodeWithText("could not refresh").assertDoesNotExist()
        }
    }

    private fun numberedReplies(count: Int): ConversationResponse =
        conversationBody(*(1..count).map { conversationItem("reply", "synthetic reply $it") }.toTypedArray())

    private fun headerBounds(): Rect =
        composeRule.onNodeWithTag(HEAD_TAG).fetchSemanticsNode().boundsInRoot

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun headerIsShownAtNewestReply() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply $REPLY_COUNT"))

            composeRule.onNodeWithTag(HEAD_TAG).assertIsDisplayed()
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun headerOverlaysTheTopOfTheConversation() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply $REPLY_COUNT"))

            val list = composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).fetchSemanticsNode().boundsInRoot
            assertEquals(headerBounds().top, list.top, EDGE_TOLERANCE_PX)
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun firstConversationItemLiesBelowHeaderAtTop() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(SHORT_REPLY_COUNT))
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply 1"))

            val header = headerBounds()
            val first = composeRule.onNodeWithText("synthetic reply 1").fetchSemanticsNode().boundsInRoot
            assertTrue(
                "first item top ${first.top} should be below header bottom ${header.bottom}",
                first.top >= header.bottom,
            )
        }
    }

    @Test
    fun workingPaneDrawsNoWorkingMarkAndWaitingRowsSitBelowTheLastItem() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "working",
                conversation = conversationBodyWithWaiting(
                    listOf(waitingItem("synthetic queued message")),
                    conversationItem("reply", "synthetic reply"),
                ),
            )
            setClockedScreen(server)

            composeRule.awaitExactlyOne(hasText("synthetic queued message"))
            composeRule.onAllNodesWithText("Claude is still working").assertCountEquals(0)
            composeRule.onAllNodesWithTag("composer-edge:working").assertCountEquals(1)
            composeRule.onNodeWithTag("dock-tray-row").assertIsDisplayed()
            composeRule.onNodeWithTag(DOCK_STATE_TAG).assertIsDisplayed()
            val reply = composeRule.onNodeWithText("synthetic reply").fetchSemanticsNode().boundsInRoot
            val waiting = composeRule.onNodeWithTag("dock-tray-row").fetchSemanticsNode().boundsInRoot
            assertTrue(waiting.top >= reply.bottom - EDGE_TOLERANCE_PX)
        }
    }

    @Test
    fun donePaneDrawsNoWorkingMarkAndCarriesTheIdleEdge() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "done",
                conversation = conversationBody(conversationItem("reply", "synthetic reply")),
            )
            setClockedScreen(server)

            composeRule.awaitExactlyOne(hasText("synthetic reply"))
            composeRule.onAllNodesWithText("Claude is still working").assertCountEquals(0)
            composeRule.onAllNodesWithTag("composer-edge:idle").assertCountEquals(1)
        }
    }

    @Test
    fun waitingPaneCarriesTheWaitingComposerEdge() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "waiting",
                conversation = conversationBody(conversationItem("reply", "synthetic reply")),
            )
            setClockedScreen(server)

            composeRule.awaitExactlyOne(hasText("synthetic reply"))
            composeRule.onAllNodesWithTag("composer-edge:waiting").assertCountEquals(1)
        }
    }

    @Test
    fun canceledWaitingRowReadsNotDelivered() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "working",
                conversation = conversationBodyWithWaiting(
                    listOf(waitingItem("synthetic dropped message", canceled = true)),
                    conversationItem("reply", "synthetic reply"),
                ),
            )
            setClockedScreen(server)

            composeRule.awaitExactlyOne(hasText("NOT DELIVERED"))
            composeRule.onNodeWithText("synthetic dropped message").assertIsDisplayed()
        }
    }

    @Test
    fun conversationWithoutGoalDrawsNoGoalBand() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "working",
                conversation = conversationBody(conversationItem("reply", "synthetic reply")),
            )
            setClockedScreen(server)

            composeRule.awaitExactlyOne(hasText("synthetic reply"))
            composeRule.onNodeWithTag(DOCK_STATE_TAG).assertIsDisplayed()
            composeRule.onAllNodesWithTag(BAND_GOAL_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun emptyGoalDrawsNoGoalBand() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "working",
                conversation = conversationBodyWithGoal("", conversationItem("reply", "synthetic reply")),
            )
            setClockedScreen(server)

            composeRule.awaitExactlyOne(hasText("synthetic reply"))
            composeRule.onAllNodesWithTag(BAND_GOAL_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun noticeInsideARunOfToolCallsFollowsTheGroupAsASlimRow() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                conversation = conversationBody(
                    toolItem("synthetic-tool-1", "Read"),
                    noticeItem("task", "synthetic task notice"),
                    toolItem("synthetic-tool-2", "Grep"),
                    toolItem("synthetic-tool-3", "Read"),
                ),
            )
            setScreen(server)

            composeRule.awaitExactlyOne(hasText("3 tool calls"))
            composeRule.onNodeWithTag("line:notice").assertIsDisplayed()
            composeRule.onNodeWithText("synthetic task notice", useUnmergedTree = true).assertIsDisplayed()
            composeRule.onAllNodesWithTag("line:tool", useUnmergedTree = true).assertCountEquals(0)
            val group = composeRule.onNodeWithTag("line:run").fetchSemanticsNode().boundsInRoot
            val notice = composeRule.onNodeWithTag("line:notice").fetchSemanticsNode().boundsInRoot
            assertTrue(notice.top >= group.bottom - EDGE_TOLERANCE_PX)

            composeRule.onNodeWithTag("line:run").performClick()
            composeRule.onAllNodesWithTag("line:tool")[0].performScrollTo().assertIsDisplayed()
            composeRule.onAllNodesWithTag("line:tool")[1].performScrollTo().assertIsDisplayed()
            composeRule.onAllNodesWithTag("line:tool")[2].performScrollTo().assertIsDisplayed()
        }
    }

    /** Whether the reply's whole text sits inside the conversation's visible list. */
    private fun replyInView(text: String): Boolean {
        val reply = composeRule.onAllNodesWithText(text)
            .fetchSemanticsNodes(atLeastOneRootRequired = false)
            .singleOrNull()?.boundsInRoot ?: return false
        val list = composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).fetchSemanticsNode().boundsInRoot
        return reply.top >= list.top && reply.bottom <= list.bottom
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun sendScrollsConversationToItsEndFromTheTop() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            server.respond(SEND_PATH, SendTextResponse.getDefaultInstance())
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply $REPLY_COUNT"))
            composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(0)
            composeRule.waitForIdle()
            assertFalse(replyInView("synthetic reply $REPLY_COUNT"))

            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()

            composeRule.awaitUntil { replyInView("synthetic reply $REPLY_COUNT") }
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun replyArrivingAfterASendComesIntoViewOnItsOwn() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            server.respond(SEND_PATH, SendTextResponse.getDefaultInstance())
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply $REPLY_COUNT"))
            composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(0)
            composeRule.waitForIdle()
            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.awaitUntil { replyInView("synthetic reply $REPLY_COUNT") }

            server.respond(CONVERSATION_PATH, numberedReplies(REPLY_COUNT + 1))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitUntil { replyInView("synthetic reply ${REPLY_COUNT + 1}") }
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun aMessageSentFromTheComposerDrawsASendingBlockUntilItsPromptIsStored() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationBody(conversationItem("reply", "synthetic reply 1")))
            server.respond(SEND_PATH, SendTextResponse.getDefaultInstance())
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply 1"))

            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.awaitExactlyOne(hasText("SENDING"))

            server.respond(
                CONVERSATION_PATH,
                conversationBody(
                    conversationItem("reply", "synthetic reply 1"),
                    conversationItem("prompt", SYNTHETIC_DRAFT),
                ),
            )
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitExactlyOne(hasText("YOU"))
            composeRule.onAllNodesWithText("SENDING").assertCountEquals(0)
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun aSentMessageIsForgottenOnceItsPromptHasLeftTheWindow() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationBody(conversationItem("reply", "synthetic reply 1")))
            server.respond(SEND_PATH, SendTextResponse.getDefaultInstance())
            setScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply 1"))

            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.awaitExactlyOne(hasText("SENDING"))

            server.respond(
                CONVERSATION_PATH,
                conversationBody(
                    conversationItem("reply", "synthetic reply 1"),
                    conversationItem("prompt", SYNTHETIC_DRAFT),
                ),
            )
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitExactlyOne(hasText("YOU"))

            // The window moves on past the prompt, which no longer holds it.
            server.respond(CONVERSATION_PATH, numberedReplies(REPLY_COUNT + 1))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.awaitExactlyOne(hasText("synthetic reply ${REPLY_COUNT + 1}"))
            composeRule.onAllNodesWithText("SENDING").assertCountEquals(0)
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun aSettledQuestionOfAnEpicThePaneDoesNotSteerDrawsTheSettledLine() {
        FakeOrreryServer().use { server ->
            val question = ConversationItemProto.newBuilder()
                .setKind("prompt")
                .setSender("event")
                .setText("<orrery-event kind=\"question\" id=\"epic:$EPIC_ID:Q1\">\nBump the embedded tz database? More.\n</orrery-event>")
                .build()
            servePane(server, conversation = conversationBody(question))
            val epic = Epic.newBuilder()
                .setId(EPIC_ID)
                .setRepository("example-repo")
                .setSlug("rota-export")
                .setLive(false)
                .build()
            server.respond(EPICS_GET_PATH, EpicsGetResponse.newBuilder().setEpic(epic).build())
            setScreen(server)

            composeRule.awaitExactlyOne(
                hasTestTag("block:epicevent") and hasAnyDescendant(hasTestTag("ask-settled")),
            )
        }
    }
}
