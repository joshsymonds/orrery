package io.github.joshsymonds.orrery

import android.content.Context
import android.content.Intent
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createEmptyComposeRule
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
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.android.controller.ActivityController
import java.nio.ByteBuffer

private const val TIMEOUT_MS = 5_000L
private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val HEADLESS_PATH = "/orrery.v1.Sessions/ListHeadless"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val EPIC_ID = "example-repo/example-epic"
private const val EPIC_SLUG = "example-epic"
private const val EPIC_REPOSITORY = "example-repo"
private const val STEP_ID = "step-synthetic-alpha"
private const val HELD_STEP_ID = "step-synthetic-held"
private const val STEP_SESSION_LABEL = "step-session-label"
private const val ENVELOPE_HEADER_BYTES = 5

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class MainActivityEpicsTest {
    @get:Rule
    val composeRule = createEmptyComposeRule()

    private lateinit var context: Context
    private lateinit var server: FakeOrreryServer

    @Before
    fun pointSettingsAtFakeServer() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
        server = FakeOrreryServer()
        ServerSettings(context).saveServerUrl(server.baseUrl)
        server.respond(PANES_PATH, panesOf(paneProto("%1", "alpha-build"), paneProto("%2", "alpha-review")))
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
    }

    @After
    fun closeServer() {
        server.close()
    }

    private fun paneProto(id: String, label: String): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost("example-host")
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd("/home/user/alpha")
        .setCommand("claude")
        .setState("working")
        .setSummary("Synthetic summary.")
        .setLabel(label)
        .setChanged(NOW - 30)
        .setSeen(NOW - 10)
        .setQuestion("")
        .setQuestionHash("")
        .build()

    private fun panesOf(vararg panes: PaneProto): PanesListResponse =
        PanesListResponse.newBuilder().addAllPanes(panes.toList()).build()

    private fun epicStep(id: String = STEP_ID, held: Boolean = false): EpicStep = EpicStep.newBuilder()
        .setId(id)
        .setTask("synthetic-task")
        .setAgent("synthetic-agent")
        .setEnd("")
        .setStatus(if (held) "held" else "working")
        .setHeld(held)
        .setStarted(NOW - 60)
        .setEnded(0L)
        .setDoing("Synthetic work.")
        .setReason("")
        .build()

    private fun liveEpic(): Epic = Epic.newBuilder()
        .setId(EPIC_ID)
        .setRepository(EPIC_REPOSITORY)
        .setSlug(EPIC_SLUG)
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .addRunningSteps(epicStep())
        .addRunningSteps(epicStep(id = HELD_STEP_ID, held = true))
        .setEndedSteps(0)
        .setProblem("")
        .build()

    private fun serveEpic() {
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(liveEpic()).build())
        server.respond(
            EPICS_GET_PATH,
            EpicsGetResponse.newBuilder().setEpic(liveEpic()).addSteps(epicStep()).build(),
        )
    }

    /** The header of the open session, by its label. */
    private fun titled(label: String): SemanticsMatcher = hasTestTag("pane-title").and(hasText(label))

    /** A headless session as the list and the watch stream report it. */
    private fun headlessSession(id: String, label: String): HeadlessSession = HeadlessSession.newBuilder()
        .setId(id)
        .setDir("/home/user/alpha")
        .setLabel(label)
        .setState("working")
        .setChanged(NOW - 30)
        .setSeen(NOW - 10)
        .build()

    /** Serves the watch stream for [sessions], each one's state when its screen opens. */
    private fun serveSessions(vararg sessions: HeadlessSession) {
        server.respond(
            HEADLESS_PATH,
            ListHeadlessResponse.newBuilder().addAllSessions(sessions.toList()).build(),
        )
        server.route(WATCH_PATH) { request ->
            val session = sessions.firstOrNull { it.id == watchedId(request) }
            if (session == null) {
                Reply.Failure(Code.NOT_FOUND, "", 0L)
            } else {
                Reply.Stream(listOf(stateDelta(session)), StreamEnd.Silent)
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
        return Robolectric.buildActivity(MainActivity::class.java, intent).setup()
    }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.waitUntil(TIMEOUT_MS) {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    private fun awaitRequest(path: String) {
        composeRule.waitUntil(TIMEOUT_MS) {
            server.requests.any { it.rawPath == path }
        }
    }

    private fun assertAbsent(matcher: SemanticsMatcher) {
        assertTrue(
            "expected no node matching $matcher",
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isEmpty(),
        )
    }

    @Test
    fun homeShowsTheLiveEpicsRunningAndHeldStepsAbovePanes() {
        serveEpic()

        launch()

        awaitNode(hasTestTag("epic:$EPIC_ID"))
        awaitNode(hasTestTag("step:$STEP_ID"))
        awaitNode(hasTestTag("held:$HELD_STEP_ID"))
        awaitNode(hasTestTag("pane-row:%1"))
        val groupTop = composeRule.onNodeWithTag("epics-group").fetchSemanticsNode().boundsInRoot.top
        val paneTop = composeRule.onNodeWithTag("pane-row:%1").fetchSemanticsNode().boundsInRoot.top
        assertTrue("expected the epics group above the panes", groupTop < paneTop)
    }

    @Test
    fun epicsListFailureStillListsThePanesWithNoGroup() {
        server.fail(EPICS_LIST_PATH, Code.UNAVAILABLE, "synthetic failure")

        launch()

        awaitNode(hasTestTag("pane-row:%1"))
        awaitRequest(EPICS_LIST_PATH)
        composeRule.waitForIdle()
        assertAbsent(hasTestTag("epics-group"))
        composeRule.onNodeWithTag("pane-row:%2").assertExists()
    }

    @Test
    fun tappingAnEpicOpensItsScreen() {
        serveEpic()
        launch()
        awaitNode(hasTestTag("epic:$EPIC_ID"))

        composeRule.onNodeWithTag("epic:$EPIC_ID").performClick()

        awaitNode(hasTestTag("epic-back"))
        assertAbsent(hasTestTag("pane-row:%1"))
    }

    @Test
    fun backReturnsFromAnEpicToHome() {
        serveEpic()
        val controller = launch()
        awaitNode(hasTestTag("epic:$EPIC_ID"))
        composeRule.onNodeWithTag("epic:$EPIC_ID").performClick()
        awaitNode(hasTestTag("epic-back"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("pane-row:%1"))
        assertAbsent(hasTestTag("epic-back"))
    }

    @Test
    fun tappingAStepOpensTheSessionScreenAndBackReturnsToTheEpic() {
        serveEpic()
        serveSessions(headlessSession(STEP_ID, STEP_SESSION_LABEL))
        val controller = launch()
        awaitNode(hasTestTag("epic:$EPIC_ID"))
        composeRule.onNodeWithTag("epic:$EPIC_ID").performClick()
        awaitNode(hasTestTag("step-row:$STEP_ID"))

        composeRule.onNodeWithTag("step-row:$STEP_ID").performClick()

        awaitNode(titled(STEP_SESSION_LABEL))
        assertAbsent(hasTestTag("epic-back"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("epic-back"))
        assertAbsent(titled(STEP_SESSION_LABEL))
    }
}
