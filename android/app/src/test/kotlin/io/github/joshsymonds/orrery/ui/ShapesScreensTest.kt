package io.github.joshsymonds.orrery.ui

import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import com.google.protobuf.MessageLite
import io.github.joshsymonds.orrery.api.ConversationItem
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.gen.orrery.v1.BackgroundTask as BackgroundTaskProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion as EpicQuestionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EngineText
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.SayRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SayResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import java.util.TimeZone
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val SAY_PATH = "/orrery.v1.Control/Say"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val EPIC_ID = "example-repo/rota-export"
private const val QUESTION_ID = "Q1"
private const val FIXTURE_CHANGED = 4102444800L
private const val FIXTURE_SEEN = 4102444900L
private const val SYNTHETIC_DRAFT = "synthetic draft"
private const val SYNTHETIC_THINKING = "synthetic thinking"
private const val SYNTHETIC_LIVE_TEXT = "synthetic live text"
private const val SYNTHETIC_DESCRIPTION = "synthetic description"
private const val SYNTHETIC_REPLY = "synthetic reply"
private const val SYNTHETIC_FILLER = "synthetic filler"
// The items a stream delivers after a send's prompt: the conversation keeps the newest 50, so the prompt leaves the window.
private const val ITEMS_AFTER_PROMPT = 50
// A hang guard for a handler that waits on the test. It never elapses in a passing run.
private const val HANG_GUARD_SECONDS = 30L

// Unix seconds, 22:13:20 UTC on 14 November 2023, and the same instant a day later.
private const val T1 = 1_700_000_000L
private const val ONE_DAY = 86_400L

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ShapesScreensTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val originalZone: TimeZone = TimeZone.getDefault()

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    private fun setScreen(server: FakeOrreryServer, clock: () -> Long = { T1 }, onOpenEpic: (String) -> Unit = {}) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client, onOpenEpic = onOpenEpic, clock = clock) }
        }
    }

    /**
     * Scripts the watch stream: the first request gets streams[0], the second streams[1], and so on. A request past the
     * script gets a stream that stays open and quiet.
     */
    private fun serveStreams(server: FakeOrreryServer, vararg streams: Reply) {
        val requests = AtomicInteger()
        server.route(WATCH_PATH) { _ ->
            val index = requests.getAndIncrement()
            if (index < streams.size) streams[index] else Reply.Stream(emptyList(), StreamEnd.Open)
        }
    }

    private fun streamOf(vararg messages: MessageLite): Reply.Stream = Reply.Stream(messages.toList(), StreamEnd.Open)

    private fun stateOf(
        state: String,
        background: List<BackgroundTaskProto> = emptyList(),
    ): ConversationState =
        ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(SESSION_ID)
                    .setDir("/home/user/project")
                    .setLabel("example-label")
                    .setState(state)
                    .setChanged(FIXTURE_CHANGED)
                    .setSeen(FIXTURE_SEEN),
            )
            .addAllBackground(background)
            .build()

    private fun delta(
        seq: Long,
        items: List<ConversationItemProto> = emptyList(),
        state: ConversationState? = null,
        unfinished: List<EngineText> = emptyList(),
    ): SessionsWatchConversationResponse {
        val builder = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(seq)
            .addAllItems(items)
            .addAllUnfinished(unfinished)
        state?.let { builder.setState(it) }
        return builder.build()
    }

    private fun item(kind: String, text: String, time: Long = 0L, sender: String = ""): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind(kind).setTime(time).setText(text).setSender(sender).build()

    private fun unfinishedText(text: String, kind: String): EngineText =
        EngineText.newBuilder()
            .setMessageId("synthetic-message")
            .setIndex(0)
            .setParentToolUseId("")
            .setKind(kind)
            .setText(text)
            .build()

    private fun block(kind: String, id: String, body: String): String =
        "<orrery-event kind=\"$kind\" id=\"$id\">\n$body\n</orrery-event>"

    /**
     * The epic [EPIC_ID], which the session id steers when [live] is set; its question [QUESTION_ID] is listed as open when
     * [open] is set.
     */
    private fun serveEpic(server: FakeOrreryServer, open: Boolean, live: Boolean = true) {
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug("rota-export")
            .setLive(live)
            .setSteering(SESSION_ID)
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(epic).build())
        server.respond(
            EPICS_GET_PATH,
            EpicsGetResponse.newBuilder()
                .setEpic(epic)
                .apply {
                    if (open) addQuestions(EpicQuestionProto.newBuilder().setId(QUESTION_ID).setText("Bump the embedded tz database?"))
                }
                .build(),
        )
    }

    /** The epic event block that asks [QUESTION_ID] of [EPIC_ID]. */
    private fun epicQuestionItem(): ConversationItemProto = item(
        "prompt",
        block("question", "epic:$EPIC_ID:$QUESTION_ID", "Bump the embedded tz database? More."),
        time = T1,
        sender = "event",
    )

    private fun backgroundTask(): BackgroundTaskProto =
        BackgroundTaskProto.newBuilder()
            .setId("synthetic-task")
            .setType("agent")
            .setSubagentType("Explore")
            .setDescription(SYNTHETIC_DESCRIPTION)
            .setStatus("running")
            .build()

    @Test
    fun unfinishedThinkingDrawsALineOfThinking() {
        FakeOrreryServer().use { server ->
            serveStreams(
                server,
                streamOf(
                    delta(
                        seq = 1L,
                        state = stateOf("working"),
                        unfinished = listOf(unfinishedText(SYNTHETIC_THINKING, kind = "thinking")),
                    ),
                ),
            )
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("line:thinking"))
        }
    }

    @Test
    fun unfinishedTextDrawsALiveReplyWithItsCaret() {
        FakeOrreryServer().use { server ->
            serveStreams(
                server,
                streamOf(
                    delta(
                        seq = 1L,
                        state = stateOf("working"),
                        unfinished = listOf(unfinishedText(SYNTHETIC_LIVE_TEXT, kind = "text")),
                    ),
                ),
            )
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("reply"))
            composeRule.onNodeWithText(SYNTHETIC_LIVE_TEXT).assertIsDisplayed()
            composeRule.onNodeWithTag("caret").assertIsDisplayed()
        }
    }

    @Test
    fun backgroundTaskDrawsATaskLineWithItsWords() {
        FakeOrreryServer().use { server ->
            serveStreams(server, streamOf(delta(seq = 1L, state = stateOf("working", listOf(backgroundTask())))))
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("line:task"))
            composeRule.onNodeWithTag("line:task")
                .assertTextContains("Explore")
                .assertTextContains(SYNTHETIC_DESCRIPTION)
                .assertTextContains("running")
        }
    }

    @Test
    fun anOpenEpicQuestionDrawsItsAskInsideItsBlock() {
        FakeOrreryServer().use { server ->
            serveEpic(server, open = true)
            serveStreams(server, streamOf(delta(seq = 1L, items = listOf(epicQuestionItem()), state = stateOf("working"))))
            setScreen(server)

            composeRule.awaitExactlyOne(
                hasTestTag("block:epicevent") and hasAnyDescendant(hasTestTag("ask")),
            )
        }
    }

    @Test
    fun aSettledEpicQuestionDrawsTheSettledLineInsideItsBlock() {
        FakeOrreryServer().use { server ->
            serveEpic(server, open = false)
            serveStreams(server, streamOf(delta(seq = 1L, items = listOf(epicQuestionItem()), state = stateOf("working"))))
            setScreen(server)

            composeRule.awaitExactlyOne(
                hasTestTag("block:epicevent") and hasAnyDescendant(hasTestTag("ask-settled")),
            )
            composeRule.onNodeWithText("· example-repo · rota-export").assertIsDisplayed()
        }
    }

    @Test
    fun aSettledQuestionOfAnEpicTheSessionDoesNotSteerDrawsTheSettledLine() {
        FakeOrreryServer().use { server ->
            serveEpic(server, open = false, live = false)
            serveStreams(server, streamOf(delta(seq = 1L, items = listOf(epicQuestionItem()), state = stateOf("working"))))
            setScreen(server)

            composeRule.awaitExactlyOne(
                hasTestTag("block:epicevent") and hasAnyDescendant(hasTestTag("ask-settled")),
            )
        }
    }

    @Test
    fun theEpicSlugOpensTheEpicWithItsId() {
        FakeOrreryServer().use { server ->
            val opened = mutableListOf<String>()
            serveStreams(server, streamOf(delta(seq = 1L, items = listOf(epicQuestionItem()), state = stateOf("working"))))
            setScreen(server, onOpenEpic = { opened += it })

            composeRule.awaitExactlyOne(hasContentDescription("Open the epic rota-export"))
            // The Block clips its content, and a press through that clip misses in Robolectric (as Blocks.kt notes), so the
            // test runs the control's click action, the one a press and a screen reader both run.
            composeRule.onNodeWithContentDescription("Open the epic rota-export")
                .performSemanticsAction(SemanticsActions.OnClick)

            assertEquals(listOf(EPIC_ID), opened)
        }
    }

    @Test
    fun aSentMessageIsHeldByItsOwnPromptIdNotByItsText() {
        val sent = listOf(
            SentText("synthetic draft", after = null, promptId = "first"),
            SentText("synthetic draft", after = null, promptId = "second"),
        )
        val held = ConversationItem("prompt", 0L, "synthetic draft", "", "", "", false, promptId = "first")

        val unsent = unsentTexts(sent, listOf(held), queued = emptyList())

        assertEquals(listOf("second"), unsent.map { it.promptId })
    }

    @Test
    fun aFirstItemOnAnotherDayThanTheScreensClockDrawsADayRule() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
        FakeOrreryServer().use { server ->
            val reply = item("reply", SYNTHETIC_REPLY, time = T1)
            serveStreams(server, streamOf(delta(seq = 1L, items = listOf(reply), state = stateOf("working"))))
            setScreen(server, clock = { T1 + ONE_DAY })

            composeRule.awaitExactlyOne(hasTestTag("rule:day"))
        }
    }

    @Test
    fun aFirstItemOnTheScreensClockDayDrawsNoDayRule() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
        FakeOrreryServer().use { server ->
            val reply = item("reply", SYNTHETIC_REPLY, time = T1)
            serveStreams(server, streamOf(delta(seq = 1L, items = listOf(reply), state = stateOf("working"))))
            setScreen(server, clock = { T1 })

            composeRule.awaitExactlyOne(hasTestTag("reply"))
            composeRule.onAllNodesWithTag("rule:day").assertCountEquals(0)
        }
    }

    @Test
    fun aMessageSentFromTheComposerDrawsASendingBlockWithoutATime() {
        FakeOrreryServer().use { server ->
            serveStreams(server, streamOf(delta(seq = 1L, state = stateOf("done"))))
            server.respond(SAY_PATH, SayResponse.getDefaultInstance())
            setScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("head"))

            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()

            composeRule.awaitExactlyOne(hasText("SENDING"))
            composeRule.onNodeWithTag("block:you").assertIsDisplayed()
            composeRule.onAllNodesWithTag("at").assertCountEquals(0)
        }
    }

    @Test
    fun aSentMessageIsForgottenOnceItsPromptHasLeftTheWindow() {
        FakeOrreryServer().use { server ->
            // The first stream ends, so the screen reconnects. The reconnect waits until the send is on the screen, then
            // delivers the prompt under the id the send was made with, and the items after it.
            val sent = CountDownLatch(1)
            val requests = AtomicInteger()
            server.route(WATCH_PATH) { _ ->
                if (requests.getAndIncrement() == 0) {
                    Reply.Stream(listOf(delta(seq = 1L, state = stateOf("done"))), StreamEnd.Clean)
                } else {
                    check(sent.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS)) { "the send never reached the screen" }
                    val promptId = SayRequest.parseFrom(server.requests.single { it.rawPath == SAY_PATH }.body).promptId
                    val prompt = ConversationItemProto.newBuilder()
                        .setKind("prompt")
                        .setText(SYNTHETIC_DRAFT)
                        .setPromptId(promptId)
                        .build()
                    val after = (1..ITEMS_AFTER_PROMPT).map { item("reply", "$SYNTHETIC_FILLER $it") }
                    Reply.Stream(
                        listOf(delta(seq = 2L, items = listOf(prompt)), delta(seq = 3L, items = after)),
                        StreamEnd.Open,
                    )
                }
            }
            server.respond(SAY_PATH, SayResponse.getDefaultInstance())
            setScreen(server)
            composeRule.awaitExactlyOne(hasTestTag("head"))

            setDraft(SYNTHETIC_DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.awaitExactlyOne(hasText("SENDING"))
            sent.countDown()

            composeRule.awaitExactlyOne(hasText("$SYNTHETIC_FILLER $ITEMS_AFTER_PROMPT"))
            composeRule.onAllNodesWithText("SENDING").assertCountEquals(0)
        }
    }

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
}
