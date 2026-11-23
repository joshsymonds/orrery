package io.github.joshsymonds.orrery

import android.content.Context
import android.content.Intent
import android.provider.Settings
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsMoveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsMoveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
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
import java.util.concurrent.atomic.AtomicReference

private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val LIST_HEADLESS_PATH = "/orrery.v1.Sessions/ListHeadless"
private const val SESSIONS_LIST_PATH = "/orrery.v1.Sessions/List"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val EPICS_HISTORY_PATH = "/orrery.v1.Epics/History"
private const val EPICS_MOVE_PATH = "/orrery.v1.Epics/Move"
private const val EPICS_CONTINUE_PATH = "/orrery.v1.Epics/Continue"
private const val NOW = 1_700_000_000L
private const val ENVELOPE_HEADER_BYTES = 5
private const val EPIC_ID = "example-repo/example-epic"
private const val EPIC_SLUG = "example-epic"
private const val EPIC_REPOSITORY = "example-repo"
private const val REPOSITORY_DIR = "/home/user/example-repo"
private const val STEERER = "session-steerer"
private const val ROW_SESSION = "session-row"
private const val ROW_LABEL = "row-label"
private const val NAMED_SESSION = "session-named-by-read"
private const val NAMED_LABEL = "named-label"
private const val PANE_ID = "%2"
private const val PANE_ORRERY = "session-pane"
private const val PANE_LABEL = "pane-label"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class MainActivitySteerMenuTest {
    @get:Rule
    val composeRule = mainThreadEmptyComposeRule()

    private lateinit var context: Context
    private lateinit var server: FakeOrreryServer

    /** Every activity a test launched, destroyed after it so no screen keeps composing into the next test. */
    private val launched = mutableListOf<ActivityController<MainActivity>>()

    @Before
    fun pointSettingsAtFakeServer() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
        server = FakeOrreryServer()
        ServerSettings(context).saveServerUrl(server.baseUrl)
        setAnimatorDurationScale(0f)
        server.respond(PANES_PATH, panes())
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(epicProto("")).build())
        server.respond(EPICS_HISTORY_PATH, EpicsHistoryResponse.getDefaultInstance())
        server.respond(LIST_HEADLESS_PATH, headlessList())
        server.respond(SESSIONS_LIST_PATH, ListResponse.getDefaultInstance())
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
        server.close()
    }

    /** The epic as the server lists and reads it, steered by [steering]; "" when no conversation steers it. */
    private fun epicProto(steering: String): Epic = Epic.newBuilder()
        .setId(EPIC_ID)
        .setRepository(EPIC_REPOSITORY)
        .setSlug(EPIC_SLUG)
        .setSource("orreryd")
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .setEndedSteps(0)
        .setSteering(steering)
        .build()

    private fun epicGet(steering: String): EpicsGetResponse = EpicsGetResponse.newBuilder()
        .setEpic(epicProto(steering))
        .setRepository(REPOSITORY_DIR)
        .build()

    /**
     * Serves the epic as steered by [steering]. A move or a continue that the server answers leaves it steered by
     * [afterAnswer], so the read after it names that conversation.
     */
    private fun serveEpic(steering: String, afterAnswer: String) {
        val current = AtomicReference(steering)
        server.route(EPICS_GET_PATH) { Reply.Message(epicGet(current.get()), 0L) }
        server.route(EPICS_MOVE_PATH) {
            current.set(afterAnswer)
            Reply.Message(EpicsMoveResponse.getDefaultInstance(), 0L)
        }
        server.route(EPICS_CONTINUE_PATH) {
            current.set(afterAnswer)
            Reply.Message(EpicsContinueResponse.getDefaultInstance(), 0L)
        }
    }

    /** The tmux pane [PANE_ORRERY], which a read after a move may name, so its screen is the pane's. */
    private fun panes(): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(
            PaneProto.newBuilder()
                .setId(PANE_ID)
                .setOrreryId(PANE_ORRERY)
                .setCwd(REPOSITORY_DIR)
                .setLabel(PANE_LABEL)
                .setState("idle")
                .setChanged(NOW - 30)
                .setSeen(NOW - 10),
        )
        .build()

    private fun headlessList(): ListHeadlessResponse = ListHeadlessResponse.newBuilder()
        .addSessions(headlessSession(STEERER, "steerer-label"))
        .addSessions(headlessSession(ROW_SESSION, ROW_LABEL))
        .build()

    private fun headlessSession(id: String, label: String): HeadlessSession = HeadlessSession.newBuilder()
        .setId(id)
        .setDir(REPOSITORY_DIR)
        .setLabel(label)
        .setState("running")
        .setChanged(NOW - 30)
        .setSeen(NOW - 10)
        .build()

    /** Serves [session]'s conversation when its screen opens, and answers not_found for any other id. */
    private fun serveConversation(session: HeadlessSession) {
        server.route(WATCH_PATH) { request ->
            if (watchedId(request) == session.id) {
                Reply.Stream(listOf(stateDelta(session)), StreamEnd.Open)
            } else {
                Reply.Failure(Code.NOT_FOUND, "", 0L)
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

    private fun countOf(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun titled(label: String): SemanticsMatcher = hasTestTag("head-text").and(hasText(label))

    /** Lets the main thread and every request it started finish, so a count taken after it sees every call. */
    private fun settle() {
        Quiescence.awaitUntil("the work settles", step = { composeRule.waitForIdle() }) {
            Quiescence.inFlight() == null
        }
    }

    /**
     * Taps [tag] through its click action. A touch injected into a popup's or a sheet's window does not reach its content
     * under Robolectric, so a tap inside one is sent as the accessibility click that the same tap runs.
     */
    private fun tapPopup(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    private fun setAnimatorDurationScale(scale: Float) {
        Settings.Global.putFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, scale)
    }

    /** Launches the app and opens the epic from the home list, then waits for its screen to draw its title row. */
    private fun openEpic() {
        launch()
        awaitNode(hasTestTag("hung:epic:$EPIC_ID"))
        composeRule.onNodeWithTag("hung:epic:$EPIC_ID").performClick()
        awaitNode(hasTestTag("epic-more"))
    }

    /** Opens the epic, then its More, then its steer command, which opens the steering sheet. */
    private fun openSteerSheet() {
        openEpic()
        composeRule.onNodeWithTag("epic-more", useUnmergedTree = true).performClick()
        awaitNode(hasTestTag("epic-steer"))
        tapPopup("epic-steer")
        awaitNode(hasTestTag("steer-menu"))
    }

    @Test
    fun theEpicScreenDrawsTheMoreAndNoTalk() {
        serveEpic(STEERER, afterAnswer = NAMED_SESSION)
        openEpic()

        settle()
        assertEquals(0, countOf(hasTestTag("epic-talk")))
    }

    @Test
    fun theMoreThenSteerOpensTheSteerMenuSheet() {
        serveEpic(STEERER, afterAnswer = NAMED_SESSION)
        openSteerSheet()

        awaitNode(hasTestTag("steer-row-$ROW_SESSION"))
        assertEquals(1, countOf(hasTestTag("steer-menu")))
    }

    @Test
    fun aRowSendsOneMoveAndOpensTheSessionTheReadNames() {
        serveEpic(STEERER, afterAnswer = NAMED_SESSION)
        serveConversation(headlessSession(NAMED_SESSION, NAMED_LABEL))
        openSteerSheet()
        awaitNode(hasTestTag("steer-row-$ROW_SESSION"))

        tapPopup("steer-row-$ROW_SESSION")
        awaitNode(titled(NAMED_LABEL))
        settle()

        val moves = server.requests.filter { it.rawPath == EPICS_MOVE_PATH }
        assertEquals(1, moves.size)
        val request = EpicsMoveRequest.parseFrom(moves.single().body)
        assertEquals(EPIC_ID, request.id)
        assertEquals(ROW_SESSION, request.session)
        awaitNone(hasTestTag("steer-menu"))
    }

    @Test
    fun aRowSendsOneMoveAndOpensThePaneTheReadNamesWhenAPaneCarriesItsOrreryId() {
        serveEpic(STEERER, afterAnswer = PANE_ORRERY)
        openSteerSheet()
        awaitNode(hasTestTag("steer-row-$ROW_SESSION"))

        tapPopup("steer-row-$ROW_SESSION")
        awaitNode(titled(PANE_LABEL))
        settle()

        assertEquals(1, server.requests.count { it.rawPath == EPICS_MOVE_PATH })
        awaitNone(hasTestTag("steer-menu"))
    }

    @Test
    fun aLiveEpicNoConversationSteersShowsTheEndedBarAndContinueOpensTheSessionTheReadNames() {
        serveEpic("", afterAnswer = NAMED_SESSION)
        serveConversation(headlessSession(NAMED_SESSION, NAMED_LABEL))
        openEpic()
        awaitNode(hasTestTag("ended-bar"))

        composeRule.onNodeWithTag("ended-continue").performClick()
        awaitNode(titled(NAMED_LABEL))
        settle()

        assertEquals(1, server.requests.count { it.rawPath == EPICS_CONTINUE_PATH })
        val request = EpicsContinueRequest.parseFrom(
            server.requests.single { it.rawPath == EPICS_CONTINUE_PATH }.body,
        )
        assertEquals(EPIC_ID, request.id)
    }
}
