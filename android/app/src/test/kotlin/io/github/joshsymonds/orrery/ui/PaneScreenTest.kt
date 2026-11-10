package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToIndex
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipe
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.MochaTerminal
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val PANE_ID = "%3"
private const val SCREEN_PATH = "/v1/panes/%253/screen"
private const val SEEN_PATH = "/v1/panes/%253/seen"
private const val PANES_PATH = "/v1/panes"
private const val KEYS_PATH = "/v1/panes/%253/keys"
private const val CONVERSATION_PATH = "/v1/panes/%253/conversation"
private const val HOST_PATH = "/v1/host"
private const val TIMEOUT_MILLIS = 5_000L
private const val WIDE_LINE_REPEATS = 30
private const val FIXED_NOW_SECONDS = 1_800_000_000L
private const val STATUS_LINE_TAG = "status-line"
private const val GOAL_LINE_TAG = "goal-line"
private const val COMPOSER_TAG = "composer"
private const val LIVE_RETURN_TAG = "live-return"
private const val HOST_METERS_TAG = "host-meters"
private const val HOST_BODY =
    """{"host":"example-host","stats":[{"label":"cpu","value":"12%","percent":12.0,"level":"ok"}]}"""
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

    private fun paneListJson(state: String, tool: String = "", question: String = ""): String = """
        [{"id":"%3","host":"example-host","session":"example-session","window":1,"pane":2,
          "cwd":"/home/user/project","command":"claude","state":"$state",
          "summary":"Example summary.","label":"example-label",
          "changed":4102444800,"seen":4102444900,"question":${JSONObject.quote(question)},
          "question_hash":"abc123","tool":${JSONObject.quote(tool)}}]
    """.trimIndent()

    private fun panesJson(question: String): String = paneListJson(state = "waiting", question = question)

    private fun serve(server: FakeOrreryServer, body: String, question: String = "") {
        server.respond("GET", SCREEN_PATH, 200, body)
        server.respond("GET", PANES_PATH, 200, panesJson(question))
        server.respond("POST", SEEN_PATH, 200)
    }

    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client) }
        }
    }

    private fun screenRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "GET" && it.rawPath == SCREEN_PATH }

    private fun seenRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == SEEN_PATH }

    private fun conversationRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "GET" && it.rawPath == CONVERSATION_PATH }

    /** Scripts the pane list and, when given, a 200 conversation. The screen and host routes stay unscripted. */
    private fun servePane(
        server: FakeOrreryServer,
        state: String = "waiting",
        tool: String = "",
        question: String = "",
        conversation: String? = null,
    ) {
        server.respond("GET", PANES_PATH, 200, paneListJson(state, tool, question))
        server.respond("POST", SEEN_PATH, 200)
        if (conversation != null) {
            server.respond("GET", CONVERSATION_PATH, 200, conversation)
        }
    }

    private fun setClockedScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client, clock = { FIXED_NOW_SECONDS }) }
        }
    }

    private fun conversationItem(kind: String, text: String): String =
        """{"kind":"$kind","time":0,"text":${JSONObject.quote(text)},"tool":"","input":"","id":"","error":false}"""

    private fun conversationBody(vararg items: String): String =
        """{"origin":"synthetic origin","items":[${items.joinToString(",")}]}"""

    private fun conversationBodyWithGoal(goal: String, vararg items: String): String =
        """{"origin":"synthetic origin","items":[${items.joinToString(",")}],"goal":${JSONObject.quote(goal)}}"""

    @Test
    fun openingShowsScreenBodyAndMarksSeenOnce() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic screen body")
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic screen body"), TIMEOUT_MILLIS)
            composeRule.waitForIdle()

            composeRule.onNodeWithText("synthetic screen body").assertIsDisplayed()
            assertEquals(1, seenRequests(server))
        }
    }

    @Test
    fun screenRefreshesEverySecondWhileVisible() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic first body")
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic first body"), TIMEOUT_MILLIS)
            assertEquals(1, screenRequests(server))

            server.respond("GET", SCREEN_PATH, 200, "synthetic second body")
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

            server.respond("GET", SCREEN_PATH, 500)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntilExactlyOneExists(hasText("could not refresh"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("synthetic kept body").assertIsDisplayed()

            server.respond("GET", SCREEN_PATH, 200, "synthetic recovered body")
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
            server.respond("POST", KEYS_PATH, 200)
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
            val request = server.requests.first { it.method == "GET" && it.rawPath == SCREEN_PATH }
            assertEquals("ansi=1&history=500", request.rawQuery)
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
            server.respond("GET", SCREEN_PATH, 200, "synthetic terminal body")
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
            server.respond("GET", SCREEN_PATH, 200, "synthetic flip terminal")
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)

            server.respond("GET", CONVERSATION_PATH, 404)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic flip terminal" }
            composeRule.onNodeWithText("synthetic reply").assertDoesNotExist()

            server.respond("GET", CONVERSATION_PATH, 200, replyBody)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onAllNodesWithTag(TERMINAL_TEXT_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun statusTapOpensLiveScreenAndLiveReturnRestoresConversation() {
        FakeOrreryServer().use { server ->
            servePane(server, conversation = conversationBody(conversationItem("reply", "synthetic reply")))
            server.respond("GET", SCREEN_PATH, 200, "synthetic live body")
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
            server.respond("GET", SCREEN_PATH, 200, "synthetic live body")
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
            server.respond("GET", SCREEN_PATH, 200, "synthetic live body")
            setClockedScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)
            composeRule.onNodeWithTag(STATUS_LINE_TAG).performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic live body" }

            server.respond("GET", CONVERSATION_PATH, 404)
            server.respond("GET", SCREEN_PATH, 200, "synthetic live after exit")
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic live after exit" }
            composeRule.onNodeWithTag(LIVE_RETURN_TAG).assertExists()

            server.respond("GET", CONVERSATION_PATH, 200, replyBody)
            server.respond("GET", SCREEN_PATH, 200, "synthetic live after restart")
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
            server.respond("GET", SCREEN_PATH, 200, "synthetic terminal body")
            setClockedScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic reply"), TIMEOUT_MILLIS)

            server.respond("GET", CONVERSATION_PATH, 500)
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
            server.respond("GET", HOST_PATH, 200, HOST_BODY)
            server.respond("GET", SCREEN_PATH, 200, "synthetic terminal body")
            setClockedScreen(server)

            composeRule.waitUntilExactlyOneExists(hasTestTag(HOST_METERS_TAG), TIMEOUT_MILLIS)
        }
    }

    @Test
    fun hostNotFoundRemovesMeters() {
        FakeOrreryServer().use { server ->
            servePane(server)
            server.respond("GET", HOST_PATH, 200, HOST_BODY)
            server.respond("GET", SCREEN_PATH, 200, "synthetic terminal body")
            setClockedScreen(server)
            composeRule.waitUntilExactlyOneExists(hasTestTag(HOST_METERS_TAG), TIMEOUT_MILLIS)

            server.respond("GET", HOST_PATH, 404)
            server.respond("GET", SCREEN_PATH, 200, "synthetic terminal after host")
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic terminal after host" }
            composeRule.onAllNodesWithTag(HOST_METERS_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun failedHostCallKeepsLastMeters() {
        FakeOrreryServer().use { server ->
            servePane(server)
            server.respond("GET", HOST_PATH, 200, HOST_BODY)
            server.respond("GET", SCREEN_PATH, 200, "synthetic terminal body")
            setClockedScreen(server)
            composeRule.waitUntilExactlyOneExists(hasTestTag(HOST_METERS_TAG), TIMEOUT_MILLIS)

            server.respond("GET", HOST_PATH, 500)
            server.respond("GET", SCREEN_PATH, 200, "synthetic terminal next")
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntil(TIMEOUT_MILLIS) { terminalText()?.text == "synthetic terminal next" }
            composeRule.onAllNodesWithTag(HOST_METERS_TAG).assertCountEquals(1)
            composeRule.onNodeWithText("could not refresh").assertDoesNotExist()
        }
    }

    private fun numberedReplies(count: Int): String =
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
            server.respond("GET", SCREEN_PATH, 200, body)
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
            server.respond("GET", SCREEN_PATH, 200, body)
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
}
