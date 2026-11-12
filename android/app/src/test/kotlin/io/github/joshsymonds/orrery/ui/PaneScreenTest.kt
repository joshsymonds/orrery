package io.github.joshsymonds.orrery.ui

import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performScrollToIndex
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipe
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import com.connectrpc.Code
import com.google.protobuf.ByteString
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationContext as ConversationContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStat as HostStatProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ScreenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ScreenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
import io.github.joshsymonds.orrery.ui.theme.MochaTerminal
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
private const val TIMEOUT_MILLIS = 5_000L
private const val WIDE_LINE_REPEATS = 30
private const val FIXED_NOW_SECONDS = 1_800_000_000L
private const val FIXTURE_SEEN = 4102444900L
// Made-up unix seconds for the seen-at-open tests: the pane's seen time sits between the two replies.
private const val OLDER_REPLY_TIME = 1_700_000_000L
private const val NEWER_REPLY_TIME = 1_700_001_000L
private const val SEEN_BETWEEN_REPLIES = 1_700_000_500L
private const val SEEN_AFTER_REPLIES = 1_700_002_000L
private const val STATUS_LINE_TAG = "status-line"
private const val SYNTHETIC_DRAFT = "synthetic draft"
private const val CLAUDE_HINT = "Message Claude…"
private const val SHELL_HINT = "Type a command…"
private const val GOAL_LINE_TAG = "goal-line"
private const val COMPOSER_TAG = "composer"
private const val LIVE_RETURN_TAG = "live-return"
private const val HOST_METERS_TAG = "host-meters"
private val HOST_BODY: HostStatusResponse = HostStatusResponse.newBuilder()
    .setHost("example-host")
    .addStats(HostStatProto.newBuilder().setLabel("cpu").setValue("12%").setPercent(12.0).setLevel("ok"))
    .build()
private val wideLine = "synthetic-wide-line ".repeat(WIDE_LINE_REPEATS).trimEnd()
private const val PANE_HEADER_TAG = "pane-header"
private const val CONVERSATION_LIST_TAG = "conversation-list"
private const val REPLY_COUNT = 40
private const val SHORT_REPLY_COUNT = 3
private const val TERMINAL_LINES = 200
private const val OLDER_DRAG_DP = 300f
private const val NEWER_DRAG_DP = 100f
// Robolectric's touch slop is 16 px at density 1, so a 20 dp drag delivers only about 4 px of
// scroll, under the 10 dp show threshold. A 40 dp drag delivers about 24 dp. (A phone's slop is 8 dp.)
private const val SHORT_DRAG_DP = 40f
private const val SLOW_SWIPE_MILLIS = 1_000L
private const val EDGE_TOLERANCE_PX = 0.5f

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PaneScreenTest {
    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

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

    private fun serve(server: FakeOrreryServer, body: String, question: String = "") {
        server.respond(SCREEN_PATH, screenOf(body))
        server.respond(PANES_PATH, paneList(state = "waiting", question = question))
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
    }

    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client) }
        }
    }

    private fun screenRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == SCREEN_PATH }

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

            composeRule.waitUntilExactlyOneExists(hasTestTag("context-meter"), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag("context-meter").assertTextEquals("▰▰▱▱▱ 40%")
        }
    }

    @Test
    fun openingShowsScreenBodyAndSendsNoSeenRequest() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic screen body")
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic screen body"), TIMEOUT_MILLIS)
            composeRule.waitForIdle()

            composeRule.onNodeWithText("synthetic screen body").assertIsDisplayed()
            assertEquals(0, seenRequests(server))
        }
    }

    private fun twoReplies(): ConversationResponse = conversationBody(
        timedReply(OLDER_REPLY_TIME, "synthetic older reply"),
        timedReply(NEWER_REPLY_TIME, "synthetic newer reply"),
    )

    /** The divider sits between the bottom of entry row [below] - 1 and the top of entry row [below]. */
    private fun assertDividerBeforeRow(below: Int) {
        composeRule.onAllNodesWithTag("new-divider").assertCountEquals(1)
        val divider = composeRule.onNodeWithTag("new-divider").fetchSemanticsNode().boundsInRoot
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

            composeRule.waitUntilExactlyOneExists(hasTestTag("new-divider"), TIMEOUT_MILLIS)
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
            composeRule.waitUntilExactlyOneExists(hasTestTag("new-divider"), TIMEOUT_MILLIS)
            assertDividerBeforeRow(below = 2)

            // The later poll also turns the pane to working. Its working mark is drawn from the same
            // state write that would move the seen time, so the mark shows the later poll has landed.
            server.respond(PANES_PATH, paneList(state = "working", seen = SEEN_AFTER_REPLIES))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntilExactlyOneExists(hasTestTag("working-mark"), TIMEOUT_MILLIS)

            assertDividerBeforeRow(below = 2)
        }
    }

    @Test
    fun seenTimeAtTheNewestReplyShowsNoDivider() {
        FakeOrreryServer().use { server ->
            servePane(server, seen = NEWER_REPLY_TIME, conversation = twoReplies())
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic newer reply"), TIMEOUT_MILLIS)
            composeRule.onAllNodesWithTag("new-divider").assertCountEquals(0)
        }
    }

    @Test
    fun aTimedReplyShownForASecondSendsItsTimeAfterThePaneRead() {
        FakeOrreryServer().use { server ->
            servePane(server, seen = SEEN_BETWEEN_REPLIES, conversation = twoReplies())
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic newer reply"), TIMEOUT_MILLIS)

            composeRule.mainClock.advanceTimeBy(2_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { readMarks(server).size == 1 }

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
            composeRule.waitUntilExactlyOneExists(hasText("synthetic older reply"), TIMEOUT_MILLIS)
            composeRule.mainClock.advanceTimeBy(2_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { readMarks(server).size == 1 }

            server.respond(CONVERSATION_PATH, twoReplies())
            composeRule.waitUntilExactlyOneExists(hasText("synthetic newer reply"), TIMEOUT_MILLIS)
            composeRule.mainClock.advanceTimeBy(2_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { readMarks(server).size == 2 }

            assertEquals(listOf(OLDER_REPLY_TIME, NEWER_REPLY_TIME), readMarks(server))
        }
    }

    @Test
    fun aFailingReadMarkLeavesTheScreenWorking() {
        FakeOrreryServer().use { server ->
            servePane(server, seen = SEEN_BETWEEN_REPLIES, conversation = twoReplies())
            server.fail(SEEN_PATH, Code.INTERNAL_ERROR)
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic newer reply"), TIMEOUT_MILLIS)

            composeRule.mainClock.advanceTimeBy(2_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { readMarks(server).size == 1 }
            composeRule.waitForIdle()
            assertEquals(listOf(NEWER_REPLY_TIME), readMarks(server))
            composeRule.onNodeWithText("synthetic newer reply").assertIsDisplayed()
            composeRule.onAllNodesWithText("could not refresh").assertCountEquals(0)

            server.respond(PANES_PATH, paneList(state = "working"))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntilExactlyOneExists(hasTestTag("working-mark"), TIMEOUT_MILLIS)
        }
    }

    @Test
    fun screenRefreshesEverySecondWhileVisible() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic first body")
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic first body"), TIMEOUT_MILLIS)
            assertEquals(1, screenRequests(server))

            server.respond(SCREEN_PATH, screenOf("synthetic second body"))
            composeRule.mainClock.advanceTimeBy(999)
            composeRule.waitForIdle()
            assertEquals(1, screenRequests(server))

            composeRule.mainClock.advanceTimeBy(1)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic second body"), TIMEOUT_MILLIS)
            assertEquals(2, screenRequests(server))
        }
    }

    @Test
    fun noRequestsBelowStartedAndPollingResumesOnReturn() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic visible body")
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic visible body"), TIMEOUT_MILLIS)

            composeRule.activityRule.scenario.moveToState(Lifecycle.State.CREATED)
            composeRule.waitForIdle()
            val stoppedAt = screenRequests(server)
            composeRule.mainClock.advanceTimeBy(3_000)
            composeRule.waitForIdle()
            assertEquals(stoppedAt, screenRequests(server))

            composeRule.activityRule.scenario.moveToState(Lifecycle.State.RESUMED)
            composeRule.waitUntil(TIMEOUT_MILLIS) { screenRequests(server) > stoppedAt }
        }
    }

    @Test
    fun failedTickKeepsTextAndNextGoodTickClearsMessage() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic kept body")
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic kept body"), TIMEOUT_MILLIS)

            server.fail(SCREEN_PATH, Code.INTERNAL_ERROR)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntilExactlyOneExists(hasText("could not refresh"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("synthetic kept body").assertIsDisplayed()

            server.respond(SCREEN_PATH, screenOf("synthetic recovered body"))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic recovered body"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("could not refresh").assertDoesNotExist()
        }
    }

    @Test
    fun paneWithQuestionShowsCardButtons() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic body", question = toolQuestion)
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("1 Yes"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("Bash: echo synthetic").assertIsDisplayed()
            composeRule.onNodeWithText("2 Yes, don't ask again").assertIsDisplayed()
            composeRule.onNodeWithText("3 No").assertIsDisplayed()
        }
    }

    @Test
    fun paneWithoutQuestionShowsNoCard() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic body")
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic body"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("1 Yes").assertDoesNotExist()
            composeRule.onNodeWithText("3 No").assertDoesNotExist()
        }
    }

    @Test
    fun cardRefreshFetchesScreenImmediately() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic body", question = toolQuestion)
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("1 Yes"), TIMEOUT_MILLIS)
            assertEquals(1, screenRequests(server))

            composeRule.mainClock.autoAdvance = false
            composeRule.onNodeWithText("1 Yes").performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { screenRequests(server) == 2 }
        }
    }

    @Test
    fun sendAndQuickKeysAreShown() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic body")
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic body"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("Send").assertIsDisplayed()
            composeRule.onNodeWithText("Esc").assertExists()
        }
    }

    @Test
    fun quickKeysHideWhileTheDraftHasTextAndReturnWhenItClears() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic body")
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic body"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("Esc").assertExists()

            setDraft(SYNTHETIC_DRAFT)
            composeRule.waitUntilDoesNotExist(hasText("Esc"), TIMEOUT_MILLIS)

            setDraft("")
            composeRule.waitUntilExactlyOneExists(hasText("Esc"), TIMEOUT_MILLIS)
        }
    }

    @Test
    fun claudePaneHintNamesClaude() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationBody(conversationItem("reply", "synthetic reply")))
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.waitForIdle()

            assertEquals(CLAUDE_HINT, editField().hint?.toString())
        }
    }

    @Test
    fun shellPaneHintNamesACommand() {
        FakeOrreryServer().use { server ->
            servePane(server, state = "")
            server.respond(SCREEN_PATH, screenOf("synthetic terminal body"))
            setScreen(server)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic terminal body" }
            composeRule.waitForIdle()

            assertEquals(SHELL_HINT, editField().hint?.toString())
        }
    }

    @Test
    fun shellPaneDrawsNoStatusLine() {
        FakeOrreryServer().use { server ->
            servePane(server, state = "")
            server.respond(SCREEN_PATH, screenOf("synthetic terminal body"))
            setScreen(server)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic terminal body" }
            composeRule.onNodeWithTag(PANE_HEADER_TAG).assertExists()

            composeRule.onAllNodesWithTag(STATUS_LINE_TAG).assertCountEquals(0)
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

    private fun terminalText(): AnnotatedString? {
        val node = composeRule.onAllNodesWithTag(TERMINAL_TEXT_TAG)
            .fetchSemanticsNodes(atLeastOneRootRequired = false)
            .singleOrNull() ?: return null
        return node.config.getOrNull(SemanticsProperties.Text)?.single()
    }

    private fun terminalLayout(): TextLayoutResult {
        val node = composeRule.onNodeWithTag(TERMINAL_TEXT_TAG).fetchSemanticsNode()
        val results = mutableListOf<TextLayoutResult>()
        node.config[SemanticsActions.GetTextLayoutResult].action!!.invoke(results)
        return results.single()
    }

    @Test
    fun screenBodyShowsColourAsSpanAndNoEscapes() {
        FakeOrreryServer().use { server ->
            serve(server, "\u001B[31msynthetic red\u001B[0m plain")
            setScreen(server)

            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic red plain" }
            val annotated = terminalText()!!
            val span = annotated.spanStyles.single()
            assertEquals(0, span.start)
            assertEquals("synthetic red".length, span.end)
            assertEquals(MochaTerminal.Ansi[1], span.item.color)
        }
    }

    @Test
    fun lineWiderThanScreenStaysOneLine() {
        FakeOrreryServer().use { server ->
            serve(server, wideLine)
            setScreen(server)

            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == wideLine }
            val viewportWidth = composeRule.onNodeWithTag(TERMINAL_VIEWPORT_TAG).fetchSemanticsNode().size.width
            val textWidth = composeRule.onNodeWithTag(TERMINAL_TEXT_TAG).fetchSemanticsNode().size.width
            assertEquals(1, terminalLayout().lineCount)
            assertTrue(textWidth > viewportWidth)
        }
    }

    @Test
    fun plainTextBodyRendersUnchanged() {
        FakeOrreryServer().use { server ->
            val body = "synthetic plain one\nsynthetic plain two"
            serve(server, body)
            setScreen(server)

            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == body }
            assertTrue(terminalText()!!.spanStyles.isEmpty())
        }
    }

    @Test
    fun screenRequestAsksForAnsiAndHistory() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic query body")
            setScreen(server)

            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic query body" }
            val request = server.requests.first { it.method == "POST" && it.rawPath == SCREEN_PATH }
            assertEquals(
                ScreenRequest.newBuilder().setPane(PANE_ID).setAnsi(true).setHistory(500).build(),
                ScreenRequest.parseFrom(request.body),
            )
        }
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

            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("synthetic prompt").assertIsDisplayed()
            composeRule.onAllNodesWithTag(TERMINAL_TEXT_TAG).assertCountEquals(0)
            assertEquals(0, screenRequests(server))
        }
    }

    @Test
    fun paneWithEmptyStateShowsTerminalAndNeverRequestsConversation() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "",
                conversation = conversationBody(conversationItem("reply", "synthetic reply")),
            )
            server.respond(SCREEN_PATH, screenOf("synthetic terminal body"))
            setScreen(server)

            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic terminal body" }
            composeRule.onNodeWithTag("pane-header").assertExists()
            composeRule.onAllNodesWithTag(STATUS_LINE_TAG).assertCountEquals(0)
            assertEquals(0, conversationRequests(server))
        }
    }

    @Test
    fun paneFlipsBetweenConversationAndTerminalOnEachPoll() {
        FakeOrreryServer().use { server ->
            val replyBody = conversationBody(conversationItem("reply", "synthetic reply"))
            servePane(server, conversation = replyBody)
            server.respond(SCREEN_PATH, screenOf("synthetic flip terminal"))
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)

            server.fail(CONVERSATION_PATH, Code.NOT_FOUND)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic flip terminal" }
            composeRule.onNodeWithText("synthetic reply").assertDoesNotExist()

            server.respond(CONVERSATION_PATH, replyBody)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onAllNodesWithTag(TERMINAL_TEXT_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun statusTapOpensLiveScreenAndLiveReturnRestoresConversation() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationBody(conversationItem("reply", "synthetic reply")))
            server.respond(SCREEN_PATH, screenOf("synthetic live body"))
            setClockedScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)

            composeRule.onNodeWithTag(STATUS_LINE_TAG).performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic live body" }
            composeRule.onNodeWithTag(LIVE_RETURN_TAG).assertExists()
            composeRule.onNodeWithText("synthetic reply").assertDoesNotExist()

            composeRule.onNodeWithTag(LIVE_RETURN_TAG).performClick()
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onAllNodesWithTag(TERMINAL_TEXT_TAG).assertCountEquals(0)
            composeRule.onAllNodesWithTag(LIVE_RETURN_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun secondStatusTapReturnsFromLiveScreen() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationBody(conversationItem("reply", "synthetic reply")))
            server.respond(SCREEN_PATH, screenOf("synthetic live body"))
            setClockedScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)

            composeRule.onNodeWithTag(STATUS_LINE_TAG).performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic live body" }
            composeRule.onNodeWithTag(STATUS_LINE_TAG).performClick()
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onAllNodesWithTag(TERMINAL_TEXT_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun liveScreenStaysOpenWhenClaudeExitsAndRestarts() {
        FakeOrreryServer().use { server ->
            val replyBody = conversationBody(conversationItem("reply", "synthetic reply"))
            servePane(server, conversation = replyBody)
            server.respond(SCREEN_PATH, screenOf("synthetic live body"))
            setClockedScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag(STATUS_LINE_TAG).performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic live body" }

            server.fail(CONVERSATION_PATH, Code.NOT_FOUND)
            server.respond(SCREEN_PATH, screenOf("synthetic live after exit"))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic live after exit" }
            composeRule.onNodeWithTag(LIVE_RETURN_TAG).assertExists()

            server.respond(CONVERSATION_PATH, replyBody)
            server.respond(SCREEN_PATH, screenOf("synthetic live after restart"))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic live after restart" }
            composeRule.onNodeWithTag(LIVE_RETURN_TAG).assertExists()
            composeRule.onNodeWithText("synthetic reply").assertDoesNotExist()
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun questionCardSitsAfterTheLastConversationItem() {
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

            composeRule.waitUntilExactlyOneExists(hasText("1 Yes"), TIMEOUT_MILLIS)
            val reply = composeRule.onNodeWithText("synthetic last reply").fetchSemanticsNode().boundsInRoot
            val card = composeRule.onNodeWithText("1 Yes").fetchSemanticsNode().boundsInRoot
            assertTrue(card.top >= reply.bottom)
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

            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("0:12", useUnmergedTree = true).assertExists()
        }
    }

    @Test
    fun conversationFailureKeepsConversationAndSaysCouldNotRefresh() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationBody(conversationItem("reply", "synthetic reply")))
            server.respond(SCREEN_PATH, screenOf("synthetic terminal body"))
            setClockedScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)

            server.fail(CONVERSATION_PATH, Code.INTERNAL_ERROR)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntilExactlyOneExists(hasText("could not refresh"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("synthetic reply").assertIsDisplayed()
            composeRule.onAllNodesWithTag(TERMINAL_TEXT_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun hostStatsShowMeters() {
        FakeOrreryServer().use { server ->
            servePane(server)
            server.respond(HOST_PATH, HOST_BODY)
            server.respond(SCREEN_PATH, screenOf("synthetic terminal body"))
            setClockedScreen(server)

            composeRule.waitUntilExactlyOneExists(hasTestTag(HOST_METERS_TAG), TIMEOUT_MILLIS)
        }
    }

    @Test
    fun hostNotFoundRemovesMeters() {
        FakeOrreryServer().use { server ->
            servePane(server)
            server.respond(HOST_PATH, HOST_BODY)
            server.respond(SCREEN_PATH, screenOf("synthetic terminal body"))
            setClockedScreen(server)
            composeRule.waitUntilExactlyOneExists(hasTestTag(HOST_METERS_TAG), TIMEOUT_MILLIS)

            server.fail(HOST_PATH, Code.NOT_FOUND)
            server.respond(SCREEN_PATH, screenOf("synthetic terminal after host"))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic terminal after host" }
            composeRule.onAllNodesWithTag(HOST_METERS_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun failedHostCallKeepsLastMeters() {
        FakeOrreryServer().use { server ->
            servePane(server)
            server.respond(HOST_PATH, HOST_BODY)
            server.respond(SCREEN_PATH, screenOf("synthetic terminal body"))
            setClockedScreen(server)
            composeRule.waitUntilExactlyOneExists(hasTestTag(HOST_METERS_TAG), TIMEOUT_MILLIS)

            server.fail(HOST_PATH, Code.INTERNAL_ERROR)
            server.respond(SCREEN_PATH, screenOf("synthetic terminal next"))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic terminal next" }
            composeRule.onAllNodesWithTag(HOST_METERS_TAG).assertCountEquals(1)
            composeRule.onNodeWithText("could not refresh").assertDoesNotExist()
        }
    }

    private fun numberedReplies(count: Int): ConversationResponse =
        conversationBody(*(1..count).map { conversationItem("reply", "synthetic reply $it") }.toTypedArray())

    private fun terminalLines(count: Int): String =
        (1..count).joinToString("\n") { "synthetic line $it" }

    private fun headerBounds(): Rect =
        composeRule.onNodeWithTag(PANE_HEADER_TAG).fetchSemanticsNode().boundsInRoot

    private fun assertHeaderShown() {
        assertTrue("header should be on screen, bounds ${headerBounds()}", headerBounds().bottom > 0f)
    }

    private fun assertHeaderHidden() {
        assertTrue("header should have slid away, bounds ${headerBounds()}", headerBounds().bottom <= 0f)
    }

    /** A slow drag over the node. A positive distance moves the finger down, towards older content. */
    private fun drag(tag: String, fingerDownDp: Float) {
        val dy = with(composeRule.density) { fingerDownDp.dp.toPx() }
        composeRule.onNodeWithTag(tag).performTouchInput {
            swipe(
                start = Offset(center.x, center.y - dy / 2),
                end = Offset(center.x, center.y + dy / 2),
                durationMillis = SLOW_SWIPE_MILLIS,
            )
        }
        composeRule.waitForIdle()
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun headerIsShownAtNewestReply() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply $REPLY_COUNT"), TIMEOUT_MILLIS)

            composeRule.onNodeWithTag(PANE_HEADER_TAG).assertIsDisplayed()
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun headerOverlaysTheTopOfTheConversation() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply $REPLY_COUNT"), TIMEOUT_MILLIS)

            val list = composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).fetchSemanticsNode().boundsInRoot
            assertEquals(headerBounds().top, list.top, EDGE_TOLERANCE_PX)
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun headerSlidesAwayOnDragTowardNewerContent() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply $REPLY_COUNT"), TIMEOUT_MILLIS)

            drag(CONVERSATION_LIST_TAG, OLDER_DRAG_DP)
            drag(CONVERSATION_LIST_TAG, -NEWER_DRAG_DP)

            assertHeaderHidden()
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun shortDragTowardOlderContentShowsHeaderAgain() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply $REPLY_COUNT"), TIMEOUT_MILLIS)

            drag(CONVERSATION_LIST_TAG, OLDER_DRAG_DP)
            drag(CONVERSATION_LIST_TAG, -NEWER_DRAG_DP)
            assertHeaderHidden()

            drag(CONVERSATION_LIST_TAG, SHORT_DRAG_DP)

            assertHeaderShown()
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun hiddenHeaderReturnsWhenScrolledToNewestReply() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply $REPLY_COUNT"), TIMEOUT_MILLIS)
            drag(CONVERSATION_LIST_TAG, OLDER_DRAG_DP)
            drag(CONVERSATION_LIST_TAG, -NEWER_DRAG_DP)
            assertHeaderHidden()

            composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(REPLY_COUNT - 1)
            composeRule.waitForIdle()

            assertHeaderShown()
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun hiddenHeaderReturnsWhenScrolledToTopOfConversation() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply $REPLY_COUNT"), TIMEOUT_MILLIS)
            drag(CONVERSATION_LIST_TAG, OLDER_DRAG_DP)
            drag(CONVERSATION_LIST_TAG, -NEWER_DRAG_DP)
            assertHeaderHidden()

            composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(0)
            composeRule.waitForIdle()

            assertHeaderShown()
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun liveScreenReopensAtItsNewestLineAfterTheReaderScrolledItUp() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(SHORT_REPLY_COUNT))
            val body = terminalLines(TERMINAL_LINES)
            server.respond(SCREEN_PATH, screenOf(body))
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply $SHORT_REPLY_COUNT"), TIMEOUT_MILLIS)

            composeRule.onNodeWithTag(STATUS_LINE_TAG).performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == body }
            drag(TERMINAL_VIEWPORT_TAG, OLDER_DRAG_DP)
            assertTrue("the drag should leave the newest line", terminalBottomGap() > 1f)

            composeRule.onNodeWithTag(LIVE_RETURN_TAG).performClick()
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply $SHORT_REPLY_COUNT"), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag(STATUS_LINE_TAG).performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == body }
            composeRule.waitForIdle()

            assertEquals(0f, terminalBottomGap(), 1f)
        }
    }

    /** How far the terminal text's last line sits below the bottom of its viewport. */
    private fun terminalBottomGap(): Float {
        val viewport = composeRule.onNodeWithTag(TERMINAL_VIEWPORT_TAG).fetchSemanticsNode()
        val text = composeRule.onNodeWithTag(TERMINAL_TEXT_TAG).fetchSemanticsNode()
        val viewportBottom = viewport.positionInRoot.y + viewport.size.height
        val textBottom = text.positionInRoot.y + text.size.height
        return textBottom - viewportBottom
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun terminalHeaderSlidesAwayAndReturnsLikeConversation() {
        FakeOrreryServer().use { server ->
            servePane(server, state = "")
            val body = terminalLines(TERMINAL_LINES)
            server.respond(SCREEN_PATH, screenOf(body))
            setScreen(server)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == body }

            drag(TERMINAL_VIEWPORT_TAG, OLDER_DRAG_DP)
            drag(TERMINAL_VIEWPORT_TAG, -NEWER_DRAG_DP)
            assertHeaderHidden()

            drag(TERMINAL_VIEWPORT_TAG, SHORT_DRAG_DP)
            assertHeaderShown()
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun firstConversationItemLiesBelowHeaderAtTop() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(SHORT_REPLY_COUNT))
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply 1"), TIMEOUT_MILLIS)

            val header = headerBounds()
            val first = composeRule.onNodeWithText("synthetic reply 1").fetchSemanticsNode().boundsInRoot
            assertTrue(
                "first item top ${first.top} should be below header bottom ${header.bottom}",
                first.top >= header.bottom,
            )
        }
    }

    @Test
    fun goalShowsInTheComposerBandJustAboveTheStatusLine() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "working",
                conversation = conversationBodyWithGoal(
                    "synthetic goal text",
                    conversationItem("reply", "synthetic reply"),
                ),
            )
            setClockedScreen(server)

            composeRule.waitUntilExactlyOneExists(hasTestTag(GOAL_LINE_TAG), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("synthetic goal text", useUnmergedTree = true).assertIsDisplayed()
            val goal = composeRule.onNodeWithTag(GOAL_LINE_TAG).fetchSemanticsNode().boundsInRoot
            val status = composeRule.onNodeWithTag(STATUS_LINE_TAG).fetchSemanticsNode().boundsInRoot
            val composer = composeRule.onNodeWithTag(COMPOSER_TAG).fetchSemanticsNode().boundsInRoot
            assertTrue(goal.bottom <= status.top + EDGE_TOLERANCE_PX)
            assertTrue(goal.top >= composer.top - EDGE_TOLERANCE_PX)
        }
    }

    @Test
    fun workingPaneShowsWorkingMarkThenWaitingRowsBelowTheLastItem() {
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

            composeRule.waitUntilExactlyOneExists(hasText("synthetic queued message"), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag("working-mark").assertIsDisplayed()
            composeRule.onNodeWithTag("waiting-row").assertIsDisplayed()
            composeRule.onNodeWithTag(STATUS_LINE_TAG).assertIsDisplayed()
            val reply = composeRule.onNodeWithText("synthetic reply").fetchSemanticsNode().boundsInRoot
            val mark = composeRule.onNodeWithTag("working-mark").fetchSemanticsNode().boundsInRoot
            val waiting = composeRule.onNodeWithTag("waiting-row").fetchSemanticsNode().boundsInRoot
            assertTrue(mark.top >= reply.bottom - EDGE_TOLERANCE_PX)
            assertTrue(waiting.top >= mark.bottom - EDGE_TOLERANCE_PX)
        }
    }

    @Test
    fun donePaneShowsNoWorkingMark() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "done",
                conversation = conversationBody(conversationItem("reply", "synthetic reply")),
            )
            setClockedScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onAllNodesWithTag("working-mark").assertCountEquals(0)
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

            composeRule.waitUntilExactlyOneExists(hasText("not delivered"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("synthetic dropped message").assertIsDisplayed()
        }
    }

    @Test
    fun conversationWithoutGoalDrawsNoGoalLine() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "working",
                conversation = conversationBody(conversationItem("reply", "synthetic reply")),
            )
            setClockedScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag(STATUS_LINE_TAG).assertIsDisplayed()
            composeRule.onAllNodesWithTag(GOAL_LINE_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun emptyGoalDrawsNoGoalLine() {
        FakeOrreryServer().use { server ->
            servePane(
                server,
                state = "working",
                conversation = conversationBodyWithGoal("", conversationItem("reply", "synthetic reply")),
            )
            setClockedScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onAllNodesWithTag(GOAL_LINE_TAG).assertCountEquals(0)
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

            composeRule.waitUntilExactlyOneExists(hasText("3 tool calls"), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag("notice:task").assertIsDisplayed()
            composeRule.onNodeWithText("synthetic task notice").assertIsDisplayed()
            composeRule.onAllNodesWithTag("tool-row:synthetic-tool-1").assertCountEquals(0)
            composeRule.onAllNodesWithTag("tool-row:synthetic-tool-2").assertCountEquals(0)
            composeRule.onAllNodesWithTag("tool-row:synthetic-tool-3").assertCountEquals(0)
            val group = composeRule.onNodeWithTag("group-row:synthetic-tool-1").fetchSemanticsNode().boundsInRoot
            val notice = composeRule.onNodeWithTag("notice:task").fetchSemanticsNode().boundsInRoot
            assertTrue(notice.top >= group.bottom - EDGE_TOLERANCE_PX)

            composeRule.onNodeWithTag("group-row:synthetic-tool-1").performClick()
            composeRule.onNodeWithTag("tool-row:synthetic-tool-1").performScrollTo().assertIsDisplayed()
            composeRule.onNodeWithTag("tool-row:synthetic-tool-2").performScrollTo().assertIsDisplayed()
            composeRule.onNodeWithTag("tool-row:synthetic-tool-3").performScrollTo().assertIsDisplayed()
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
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply $REPLY_COUNT"), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(0)
            composeRule.waitForIdle()
            assertFalse(replyInView("synthetic reply $REPLY_COUNT"))

            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()

            composeRule.waitUntil(TIMEOUT_MILLIS) { replyInView("synthetic reply $REPLY_COUNT") }
        }
    }

    @Config(sdk = [35], qualifiers = "w400dp-h1000dp")
    @Test
    fun replyArrivingAfterASendComesIntoViewOnItsOwn() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = numberedReplies(REPLY_COUNT))
            server.respond(SEND_PATH, SendTextResponse.getDefaultInstance())
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply $REPLY_COUNT"), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(0)
            composeRule.waitForIdle()
            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { replyInView("synthetic reply $REPLY_COUNT") }

            server.respond(CONVERSATION_PATH, numberedReplies(REPLY_COUNT + 1))
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { replyInView("synthetic reply ${REPLY_COUNT + 1}") }
        }
    }
}
