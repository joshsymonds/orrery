package io.github.joshsymonds.orrery

import android.content.Context
import android.content.Intent
import android.provider.Settings
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicSession
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StartRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StartResponse
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.android.controller.ActivityController
import java.nio.ByteBuffer
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val START_PATH = "/orrery.v1.Control/Start"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val EPICS_HISTORY_PATH = "/orrery.v1.Epics/History"
private const val EPIC_ID = "example-repo/example-epic"
private const val EPIC_SLUG = "example-epic"
private const val EPIC_REPOSITORY = "example-repo"
private const val REPOSITORY_DIR = "/home/user/example-repo"
private const val SESSION_ID = "session-synthetic-talk"
private const val SESSION_LABEL = "synthetic-talk-label"
private const val NEW_SESSION_ID = "session-synthetic-started"
private const val ENVELOPE_HEADER_BYTES = 5
private const val FIRST_PROMPT =
    "This session is about orrery epic example-repo/example-epic. Read it with epic_get, then wait for my words."

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class MainActivityTalkTest {
    @get:Rule
    val composeRule = mainThreadEmptyComposeRule()

    private lateinit var context: Context
    private lateinit var server: FakeOrreryServer

    /** Every activity a test launched, destroyed after it so no screen keeps composing into the next test. */
    private val launched = mutableListOf<ActivityController<MainActivity>>()

    /** The clock time the session's call was made, as the Talk list shows it, in this machine's zone. */
    private val callTime: String = Instant.ofEpochSecond(NOW - 60)
        .atZone(ZoneId.systemDefault())
        .format(DateTimeFormatter.ofPattern("HH:mm"))

    @Before
    fun pointSettingsAtFakeServer() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
        server = FakeOrreryServer()
        ServerSettings(context).saveServerUrl(server.baseUrl)
        setAnimatorDurationScale(0f)
        server.respond(PANES_PATH, PanesListResponse.getDefaultInstance())
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(liveEpic()).build())
        server.respond(EPICS_HISTORY_PATH, EpicsHistoryResponse.getDefaultInstance())
        server.respond(EPICS_GET_PATH, epicDetail())
        serveConversations(headlessSession(SESSION_ID, SESSION_LABEL), headlessSession(NEW_SESSION_ID, "new-label"))
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
        server.close()
    }

    private fun liveEpic(): Epic = Epic.newBuilder()
        .setId(EPIC_ID)
        .setRepository(EPIC_REPOSITORY)
        .setSlug(EPIC_SLUG)
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .setEndedSteps(0)
        .setProblem("")
        .build()

    /** The epic as Get reads it: its one session, and the repository a new session of its Talk starts in. */
    private fun epicDetail(): EpicsGetResponse = EpicsGetResponse.newBuilder()
        .setEpic(liveEpic())
        .setRepository(REPOSITORY_DIR)
        .addSessions(
            EpicSession.newBuilder()
                .setId(SESSION_ID)
                .setPane("")
                .setLabel(SESSION_LABEL)
                .setCall("epic_get")
                .setAt(NOW - 60),
        )
        .build()

    private fun headlessSession(id: String, label: String): HeadlessSession = HeadlessSession.newBuilder()
        .setId(id)
        .setDir(REPOSITORY_DIR)
        .setLabel(label)
        .setState("working")
        .setChanged(NOW - 30)
        .setSeen(NOW - 10)
        .build()

    /** Serves each of [sessions]' conversation when its screen opens, and answers not_found for any other id. */
    private fun serveConversations(vararg sessions: HeadlessSession) {
        server.route(WATCH_PATH) { request ->
            val session = sessions.firstOrNull { it.id == watchedId(request) }
            if (session == null) {
                Reply.Failure(Code.NOT_FOUND, "", 0L)
            } else {
                Reply.Stream(listOf(stateDelta(session)), StreamEnd.Open)
            }
        }
    }

    private fun stateDelta(session: HeadlessSession): SessionsWatchConversationResponse =
        SessionsWatchConversationResponse.newBuilder()
            .setTranscript(session.id)
            .setSeq(1L)
            .setState(
                ConversationState.newBuilder()
                    .setOrigin("synthetic origin")
                    .setSession(session),
            )
            .build()

    /** The id a watch request names: its enveloped body, one flag byte, a big-endian length, then the payload. */
    private fun watchedId(request: RecordedRequest): String {
        val length = ByteBuffer.wrap(request.body, 1, 4).int
        return SessionsWatchConversationRequest.parseFrom(
            request.body.copyOfRange(ENVELOPE_HEADER_BYTES, ENVELOPE_HEADER_BYTES + length),
        ).id
    }

    private fun launch(): ActivityController<MainActivity> {
        val intent = Intent(context, MainActivity::class.java)
        return Robolectric.buildActivity(MainActivity::class.java, intent).setup().also { launched += it }
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

    private fun titled(label: String): SemanticsMatcher = hasTestTag("head-text").and(hasText(label))

    private fun setAnimatorDurationScale(scale: Float) {
        Settings.Global.putFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, scale)
    }

    /** Launches the app, opens the epic from the home list, then taps Talk and waits for the list's sessions. */
    private fun openTalkList(): ActivityController<MainActivity> {
        val controller = launch()
        awaitNode(hasTestTag("epic:$EPIC_ID"))
        composeRule.onNodeWithTag("epic:$EPIC_ID").performClick()
        awaitNode(hasTestTag("epic-talk"))
        composeRule.onNodeWithTag("epic-talk").performClick()
        awaitNode(hasTestTag("talk-session"))
        return controller
    }

    @Test
    fun talkOpensTheListOfTheEpicsSessionsWithItsCallAndTime() {
        openTalkList()

        composeRule.onNodeWithTag("talk-session").assertTextEquals("$SESSION_LABEL · epic_get · $callTime")
    }

    @Test
    fun tappingASessionOpensItsConversationUnderTheEpicsLine() {
        openTalkList()

        composeRule.onNodeWithTag("talk-session").performClick()

        awaitNode(hasTestTag("talk-epic-line"))
        composeRule.onNodeWithTag("talk-epic-line").assertTextEquals(EPIC_SLUG)
        awaitNode(titled(SESSION_LABEL))
    }

    @Test
    fun tappingTheEpicsLineReturnsToTheEpicScreen() {
        openTalkList()
        composeRule.onNodeWithTag("talk-session").performClick()
        awaitNode(hasTestTag("talk-epic-line"))

        composeRule.onNodeWithTag("talk-epic-line").performClick()

        awaitNode(hasTestTag("epic-talk"))
        awaitNone(hasTestTag("talk-epic-line"))
    }

    @Test
    fun aNewSessionStartsInTheEpicsRepositoryWithTheFirstPromptAndOpensUnderTheEpicsLine() {
        server.respond(START_PATH, StartResponse.newBuilder().setId(NEW_SESSION_ID).build())
        openTalkList()

        composeRule.onNodeWithTag("talk-new").performClick()

        awaitNode(hasTestTag("talk-epic-line"))
        val start = server.requests.single { it.rawPath == START_PATH }
        val sent = StartRequest.parseFrom(start.body)
        assertEquals(REPOSITORY_DIR, sent.dir)
        assertEquals(FIRST_PROMPT, sent.prompt)
    }
}
