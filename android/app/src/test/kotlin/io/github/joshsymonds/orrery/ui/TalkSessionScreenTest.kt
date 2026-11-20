package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import java.util.concurrent.atomic.AtomicInteger
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val EPIC_LINE = "synthetic epic line"
private const val SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val PANE_ID = "%7"
private const val SYNTHETIC_ORIGIN = "synthetic origin"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val PANE_STREAM_PATH = "/orrery.v1.Panes/WatchConversation"
private const val EPIC_LINE_TAG = "talk-epic-line"
private const val RESUME_TAG = "dock-resume"
private const val NO_CONVERSATION_TAG = "no-conversation"
private val SESSION_TARGET = TalkTarget(TalkKind.SESSION, SESSION_ID, "synthetic session")
private val PANE_TARGET = TalkTarget(TalkKind.PANE, PANE_ID, "synthetic pane")

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class TalkSessionScreenTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val server = FakeOrreryServer()

    @After
    fun tearDown() {
        server.close()
    }

    private fun setScreen(target: TalkTarget, onBack: () -> Unit = {}) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { TalkSessionScreen(EPIC_LINE, target, client, onBack) }
        }
    }

    /** A session whose stream says it is interrupted: the first state the screen reads. */
    private fun serveInterruptedSession() {
        val interrupted = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .setState(
                ConversationState.newBuilder()
                    .setOrigin(SYNTHETIC_ORIGIN)
                    .setSession(HeadlessSession.newBuilder().setId(SESSION_ID).setState("interrupted")),
            )
            .build()
        server.route(WATCH_PATH) { Reply.Stream(listOf(interrupted), StreamEnd.Open) }
    }

    /** A pane with no conversation, so its screen says so. Its stream is refused, so the screen polls the pane list. */
    private fun servePane() {
        val pane = PanesListResponse.newBuilder()
            .addPanes(
                PaneProto.newBuilder()
                    .setId(PANE_ID)
                    .setHost("synthetic-host")
                    .setSession("synthetic-session")
                    .setCwd("/home/user/project")
                    .setLabel("synthetic pane")
                    .setState(""),
            )
            .build()
        server.respond(PANES_PATH, pane)
        server.route(PANE_STREAM_PATH) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
    }

    @Test
    fun theEpicLineShowsItsText() {
        server.route(WATCH_PATH) { Reply.Stream(emptyList(), StreamEnd.Open) }
        setScreen(SESSION_TARGET)

        composeRule.onNodeWithTag(EPIC_LINE_TAG).assertTextEquals(EPIC_LINE)
    }

    @Test
    fun aTapOnTheEpicLineCallsBackOnce() {
        server.route(WATCH_PATH) { Reply.Stream(emptyList(), StreamEnd.Open) }
        val backs = AtomicInteger()
        setScreen(SESSION_TARGET) { backs.incrementAndGet() }

        composeRule.onNodeWithTag(EPIC_LINE_TAG).performClick()
        composeRule.awaitUntil { backs.get() == 1 }
        composeRule.waitForIdle()

        assertEquals(1, backs.get())
    }

    @Test
    fun anInterruptedSessionShowsResume() {
        serveInterruptedSession()
        setScreen(SESSION_TARGET)

        composeRule.awaitExactlyOne(hasTestTag(RESUME_TAG))
        composeRule.onNodeWithTag(RESUME_TAG).assertIsDisplayed()
    }

    @Test
    fun aPaneTargetDrawsThePaneScreenNotTheSession() {
        servePane()
        setScreen(PANE_TARGET)

        composeRule.awaitExactlyOne(hasTestTag(NO_CONVERSATION_TAG))
        assertTrue(server.requests.none { it.rawPath == WATCH_PATH })
    }
}
