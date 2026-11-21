package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToIndex
import androidx.compose.ui.test.performTextInput
import com.connectrpc.Code
import com.google.protobuf.MessageLite
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val SESSION_SEEN_PATH = "/orrery.v1.Control/Seen"
private const val PANE_ID = "%3"
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val PANE_CONVERSATION_PATH = "/orrery.v1.Panes/Conversation"
private const val PANE_STREAM_PATH = "/orrery.v1.Panes/WatchConversation"
private const val PANE_SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val FIXTURE_CHANGED = 4102444800L
private const val FIXTURE_SEEN = 4102444900L
private const val SYNTHETIC_ORIGIN = "synthetic origin"
private const val CONVERSATION_LIST_TAG = "conversation-list"
private const val FIND_BAR_TAG = "find-bar"
private const val FIND_COUNT_TAG = "find-count"
private const val FIND_NEXT_TAG = "find-next"
private const val FIND_PREVIOUS_TAG = "find-previous"
private const val HEAD_MORE_TAG = "head-more"
private const val HEAD_MENU_FIND_TAG = "head-menu-find"
private const val QUERY = "build"
private const val PROMPT_TEXT = "synthetic build"
private const val REPLY_TEXT = "a build, then build again"
private const val LATE_REPLY_TEXT = "synthetic build again"
private const val READER_AT = 29
private const val REPLY_COUNT = 40
private const val TARGET_REPLY = "synthetic reply 5"
private const val TALL_PARAGRAPHS = 60
private const val TAIL_QUERY = "tailmark"
private const val TAIL_TEXT = "synthetic paragraph with tailmark"
private const val ENGINE_HEAD = "synthetic engine line"
private const val ENGINE_HIDDEN = "hidden detail"
private const val ENGINE_TEXT = "$ENGINE_HEAD\nsynthetic $ENGINE_HIDDEN"
private const val CODE_BODY = "let build = 1"
private const val REPLY_WITH_CODE = "Here it is:\n\n```\n$CODE_BODY\n```"
private const val NOTICE_KIND = "build"
private const val NOTICE_TEXT = "a build here"
private val KIND_LABEL = NOTICE_KIND.inLabelCase()

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class FindScreenTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private fun stateOf(state: String): ConversationState = ConversationState.newBuilder()
        .setOrigin(SYNTHETIC_ORIGIN)
        .setSession(
            HeadlessSession.newBuilder()
                .setId(SESSION_ID)
                .setDir("/home/user/project")
                .setLabel("example-label")
                .setState(state)
                .setChanged(FIXTURE_CHANGED)
                .setSeen(FIXTURE_SEEN),
        )
        .build()

    private fun delta(seq: Long, items: List<ConversationItemProto>): SessionsWatchConversationResponse =
        SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(seq)
            .addAllItems(items)
            .setState(stateOf("done"))
            .build()

    private fun item(kind: String, text: String): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind(kind).setTime(0).setText(text).build()

    private fun noticeItem(kind: String, text: String): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind("notice").setNotice(kind).setText(text).setTime(0).build()

    private fun engineItem(text: String): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind("prompt").setSender("engine").setText(text).build()

    private fun serveSession(server: FakeOrreryServer, items: List<ConversationItemProto>) {
        val requests = AtomicInteger()
        server.route(WATCH_PATH) { _ ->
            if (requests.getAndIncrement() == 0) {
                Reply.Stream(listOf<MessageLite>(delta(seq = 1L, items = items)), StreamEnd.Open)
            } else {
                Reply.Stream(emptyList(), StreamEnd.Open)
            }
        }
        server.respond(SESSION_SEEN_PATH, SeenResponse.getDefaultInstance())
    }

    private fun setSessionScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client) }
        }
    }

    private fun servePane(server: FakeOrreryServer, conversation: ConversationResponse) {
        val pane = PaneProto.newBuilder()
            .setId(PANE_ID)
            .setHost("example-host")
            .setSession("example-session")
            .setWindow(1)
            .setPane(2)
            .setCwd("/home/user/project")
            .setCommand("claude")
            .setState("waiting")
            .setLabel("example-label")
            .setChanged(FIXTURE_CHANGED)
            .setSeen(FIXTURE_SEEN)
        server.respond(PANES_PATH, PanesListResponse.newBuilder().addPanes(pane).build())
        server.respond(PANE_SEEN_PATH, MarkSeenResponse.getDefaultInstance())
        server.respond(PANE_CONVERSATION_PATH, conversation)
        server.route(PANE_STREAM_PATH) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
    }

    private fun setPaneScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client) }
        }
    }

    private fun conversationOf(vararg items: ConversationItemProto): ConversationResponse =
        ConversationResponse.newBuilder().setOrigin(SYNTHETIC_ORIGIN).addAllItems(items.toList()).build()

    private fun openFindFromMore() {
        composeRule.onNodeWithTag(HEAD_MORE_TAG).performClick()
        composeRule.onNodeWithTag(HEAD_MENU_FIND_TAG).performClick()
    }

    private fun typeQuery(query: String) {
        composeRule.onNode(hasSetTextAction()).performTextInput(query)
    }

    /** The spans of the drawn text [text] that carry a background, by their colour. */
    private fun backgroundsOf(text: String): List<Color?> {
        // A merged row lists its children's text too, so the same drawn text can come from two nodes: one value, once.
        val drawn = composeRule.onAllNodes(hasText(text)).fetchSemanticsNodes()
            .flatMap { it.config[SemanticsProperties.Text] }
            .filter { it.text == text }
            .distinct()
        return drawn.single().spanStyles.map { it.item.background }
    }

    private fun countOf(text: String, colour: Color): Int = backgroundsOf(text).count { it == colour }

    @Test
    fun findOpensFromMoreOnTheSessionScreen() {
        FakeOrreryServer().use { server ->
            serveSession(server, listOf(item("prompt", PROMPT_TEXT), item("reply", REPLY_TEXT)))
            setSessionScreen(server)
            composeRule.awaitExactlyOne(hasText(REPLY_TEXT))

            openFindFromMore()

            composeRule.onNodeWithTag(FIND_BAR_TAG).assertIsDisplayed()
        }
    }

    @Test
    fun findOpensFromMoreOnThePaneScreen() {
        FakeOrreryServer().use { server ->
            servePane(server, conversationOf(item("prompt", PROMPT_TEXT), item("reply", REPLY_TEXT)))
            setPaneScreen(server)
            composeRule.awaitExactlyOne(hasText(REPLY_TEXT))

            openFindFromMore()

            composeRule.onNodeWithTag(FIND_BAR_TAG).assertIsDisplayed()
        }
    }

    @Test
    fun findMarksEveryMatchWithTheCurrentOneSolidAndCountsIt() {
        FakeOrreryServer().use { server ->
            serveSession(server, listOf(item("prompt", PROMPT_TEXT), item("reply", REPLY_TEXT), item("reply", LATE_REPLY_TEXT)))
            setSessionScreen(server)
            composeRule.awaitExactlyOne(hasText(LATE_REPLY_TEXT))
            openFindFromMore()

            typeQuery(QUERY)

            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("1 of 4")
            assertEquals(1, countOf(PROMPT_TEXT, OrreryTokens.Role.mark))
            assertEquals(2, countOf(REPLY_TEXT, OrreryTokens.Role.markLine))
            assertEquals(0, countOf(REPLY_TEXT, OrreryTokens.Role.mark))
        }
    }

    @Test
    fun findNextAndPreviousStepAndWrap() {
        FakeOrreryServer().use { server ->
            serveSession(server, listOf(item("prompt", PROMPT_TEXT), item("reply", REPLY_TEXT)))
            setSessionScreen(server)
            composeRule.awaitExactlyOne(hasText(REPLY_TEXT))
            openFindFromMore()
            typeQuery(QUERY)
            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("1 of 3")

            composeRule.onNodeWithTag(FIND_NEXT_TAG).performClick()
            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("2 of 3")
            composeRule.onNodeWithTag(FIND_NEXT_TAG).performClick()
            composeRule.onNodeWithTag(FIND_NEXT_TAG).performClick()
            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("1 of 3")
            composeRule.onNodeWithTag(FIND_PREVIOUS_TAG).performClick()
            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("3 of 3")
        }
    }

    @Test
    fun findSaysNoMatchesAndMarksNothingForAQueryThatFindsNone() {
        FakeOrreryServer().use { server ->
            serveSession(server, listOf(item("prompt", PROMPT_TEXT), item("reply", REPLY_TEXT)))
            setSessionScreen(server)
            composeRule.awaitExactlyOne(hasText(REPLY_TEXT))
            openFindFromMore()

            typeQuery("zzz")

            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("No matches")
            assertEquals(0, countOf(REPLY_TEXT, OrreryTokens.Role.markLine))
        }
    }

    @Test
    fun findCountsAndMarksAMatchInsideACodeBlock() {
        FakeOrreryServer().use { server ->
            serveSession(server, listOf(item("prompt", PROMPT_TEXT), item("reply", REPLY_WITH_CODE)))
            setSessionScreen(server)
            composeRule.awaitExactlyOne(hasText(CODE_BODY))
            openFindFromMore()

            typeQuery(QUERY)

            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("1 of 2")
            assertEquals(1, countOf(CODE_BODY, OrreryTokens.Role.markLine))
            composeRule.onNodeWithTag(FIND_NEXT_TAG).performClick()
            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("2 of 2")
            assertEquals(1, countOf(CODE_BODY, OrreryTokens.Role.mark))
        }
    }

    @Test
    fun findSearchesAndMarksTheKindWordAndTheTextOfANotice() {
        FakeOrreryServer().use { server ->
            serveSession(server, listOf(item("prompt", PROMPT_TEXT), noticeItem(NOTICE_KIND, NOTICE_TEXT)))
            setSessionScreen(server)
            composeRule.awaitExactlyOne(hasText(NOTICE_TEXT))
            openFindFromMore()

            typeQuery(QUERY)

            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("1 of 3")
            assertEquals(1, countOf(KIND_LABEL, OrreryTokens.Role.markLine))
            assertEquals(1, countOf(NOTICE_TEXT, OrreryTokens.Role.markLine))
            composeRule.onNodeWithTag(FIND_NEXT_TAG).performClick()
            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("2 of 3")
            assertEquals(1, countOf(KIND_LABEL, OrreryTokens.Role.mark))
            composeRule.onNodeWithTag(FIND_NEXT_TAG).performClick()
            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("3 of 3")
            assertEquals(1, countOf(NOTICE_TEXT, OrreryTokens.Role.mark))
        }
    }

    @Test
    fun findOpensAFoldWhoseHiddenTextHoldsTheCurrentMatch() {
        FakeOrreryServer().use { server ->
            serveSession(server, listOf(item("prompt", PROMPT_TEXT), engineItem(ENGINE_TEXT)))
            setSessionScreen(server)
            composeRule.awaitExactlyOne(hasText(ENGINE_HEAD))
            // The find field holds the query too, so the drawn text is the match that is not the field.
            val drawnHidden = hasText(ENGINE_HIDDEN, substring = true) and !hasSetTextAction()
            composeRule.onAllNodes(drawnHidden).assertCountEquals(0)
            openFindFromMore()

            typeQuery(ENGINE_HIDDEN)

            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("1 of 1")
            composeRule.onNode(drawnHidden).assertIsDisplayed()
        }
    }

    @Test
    fun findCloseReturnsTheListToWhereTheReaderWas() {
        FakeOrreryServer().use { server ->
            servePane(server, conversationOf(*(1..REPLY_COUNT).map { item("reply", "synthetic reply $it") }.toTypedArray()))
            setPaneScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply $REPLY_COUNT"))
            composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(READER_AT)
            composeRule.onNodeWithTag("entry-row:$READER_AT").assertIsDisplayed()
            openFindFromMore()

            typeQuery(TARGET_REPLY)
            composeRule.onNodeWithTag("entry-row:4").assertIsDisplayed()
            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("1 of 1")

            composeRule.onNodeWithTag("find-close").performClick()
            composeRule.onNodeWithTag("entry-row:$READER_AT").assertIsDisplayed()
            composeRule.onNodeWithTag("entry-row:4").assertIsNotDisplayed()
            composeRule.onAllNodesWithTag(FIND_BAR_TAG).assertCountEquals(0)
        }
    }

    @Test
    fun findBringsTheCurrentRunOfAReplyTallerThanTheScreenIntoView() {
        FakeOrreryServer().use { server ->
            val tall = (1..TALL_PARAGRAPHS).joinToString("\n\n") { "synthetic paragraph $it" } + "\n\n$TAIL_TEXT"
            serveSession(server, listOf(item("prompt", PROMPT_TEXT), item("reply", tall)))
            setSessionScreen(server)
            composeRule.awaitExactlyOne(hasText(TAIL_TEXT))
            openFindFromMore()

            typeQuery(TAIL_QUERY)

            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("1 of 1")
            composeRule.onNode(hasText(TAIL_TEXT)).assertIsDisplayed()
        }
    }

    @Test
    fun findNextScrollsBackToTheOnlyMatchAfterTheReaderScrolledAway() {
        FakeOrreryServer().use { server ->
            servePane(server, conversationOf(*(1..REPLY_COUNT).map { item("reply", "synthetic reply $it") }.toTypedArray()))
            setPaneScreen(server)
            composeRule.awaitExactlyOne(hasText("synthetic reply $REPLY_COUNT"))
            openFindFromMore()
            typeQuery(TARGET_REPLY)
            composeRule.onNodeWithTag("entry-row:4").assertIsDisplayed()

            composeRule.onNodeWithTag(CONVERSATION_LIST_TAG).performScrollToIndex(READER_AT)
            composeRule.onNodeWithTag("entry-row:4").assertIsNotDisplayed()
            composeRule.onNodeWithTag(FIND_NEXT_TAG).performClick()

            composeRule.onNodeWithTag(FIND_COUNT_TAG).assertTextEquals("1 of 1")
            composeRule.onNodeWithTag("entry-row:4").assertIsDisplayed()
        }
    }
}
