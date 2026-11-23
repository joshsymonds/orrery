package io.github.joshsymonds.orrery

import android.content.Context
import android.content.Intent
import android.provider.Settings
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAddition
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAlternative
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicBuildTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicFailure
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicHistoryEntry
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicPremise
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.ui.theme.Move
import org.junit.After
import org.junit.Assert.assertEquals
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

private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val HEADLESS_PATH = "/orrery.v1.Sessions/ListHeadless"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val EPICS_HISTORY_PATH = "/orrery.v1.Epics/History"
private const val EPIC_ID = "example-repo/example-epic"
private const val EPIC_SLUG = "example-epic"
private const val EPIC_REPOSITORY = "example-repo"
private const val FOLDED_ID = "example-repo/folded-epic"
private const val FOLDED_SLUG = "folded-epic"
private const val STEP_ID = "step-synthetic-alpha"
private const val HELD_STEP_ID = "step-synthetic-held"
private const val STEP_SESSION_LABEL = "step-session-label"
private const val TASK_ID = "synthetic-task"
private const val TASK_TITLE = "Synthetic task title"
private const val IMPLEMENT_STEP_ID = "step-synthetic-implement"
private const val CHECK_STEP_ID = "step-synthetic-check"
private const val CHECK_SESSION_LABEL = "step-check-session-label"
private const val ENVELOPE_HEADER_BYTES = 5
private const val BRIEF_TEXT = "Synthetic brief text."

// A summary of sixty lines is taller than the screen, so a scroll leaves the summary's top above the screen and its
// centre still under a tap.
private val TALL_SUMMARY = (1..60).joinToString("\n") { "Synthetic summary line $it." }

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class MainActivityEpicsTest {
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
        server.respond(PANES_PATH, panesOf(paneProto("%1", "alpha-build"), paneProto("%2", "alpha-review")))
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
        setAnimatorDurationScale(1f)
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
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

    private fun epicStep(
        id: String = STEP_ID,
        held: Boolean = false,
        task: String = TASK_ID,
        stage: String = "",
        end: String = "",
    ): EpicStep = EpicStep.newBuilder()
        .setId(id)
        .setTask(task)
        .setAgent("synthetic-agent")
        .setEnd(end)
        .setStatus(if (held) "held" else "working")
        .setHeld(held)
        .setStage(stage)
        .setStarted(NOW - 60)
        .setEnded(if (end.isEmpty()) 0L else NOW - 30)
        .setDoing("Synthetic work.")
        .setReason("")
        .build()

    /** A task in checking: its implementing step has ended done, and its checks step runs. */
    private fun checkingSteps(): List<EpicStep> = listOf(
        epicStep(id = IMPLEMENT_STEP_ID, stage = "implement", end = "done"),
        epicStep(id = CHECK_STEP_ID, stage = "checks"),
    )

    /** The one planned task of the epic, the orrery's one wedge, which serve places running at green. */
    private fun taskBuild(): EpicBuildTask = EpicBuildTask.newBuilder()
        .setId(TASK_ID)
        .setTitle(TASK_TITLE)
        .setStatus("open")
        .setStage("checks")
        .addRequirements("R1")
        .setPlace("green")
        .setTone("run")
        .build()

    private fun liveEpic(): Epic = Epic.newBuilder()
        .setId(EPIC_ID)
        .setRepository(EPIC_REPOSITORY)
        .setSlug(EPIC_SLUG)
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .setSource("orreryd")
        .addRunningSteps(epicStep())
        .addRunningSteps(epicStep(id = HELD_STEP_ID, held = true))
        .setEndedSteps(0)
        .build()

    /**
     * Serves the epic with its [summary], [steps], planned [buildTasks] and open [questions]. When [contracted], it also
     * serves its contract and its one requirement.
     */
    private fun serveEpic(
        contracted: Boolean = false,
        summary: String = "",
        steps: List<EpicStep> = listOf(epicStep()),
        buildTasks: List<EpicBuildTask> = emptyList(),
        questions: List<EpicQuestion> = emptyList(),
    ) {
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(liveEpic()).build())
        val detail = EpicsGetResponse.newBuilder()
            .setEpic(liveEpic())
            .addAllSteps(steps)
            .addAllBuildTasks(buildTasks)
            .setSummary(summary)
            .addAllQuestions(questions)
        if (contracted) {
            detail.setContract(contractProto()).addRequirements(requirement)
        }
        server.respond(EPICS_GET_PATH, detail.build())
        server.respond(EPICS_HISTORY_PATH, EpicsHistoryResponse.getDefaultInstance())
    }

    /** The question the epic waits on, asked two minutes before [NOW]. */
    private fun openQuestion(): EpicQuestion = EpicQuestion.newBuilder()
        .setId("Q1")
        .setText("Synthetic question text?")
        .setTurn(1L)
        .setAsked(NOW - 120L)
        .build()

    /** An epic that has ended: not live, so the inbox never asks for its detail. */
    private fun foldedEpic(): Epic = Epic.newBuilder()
        .setId(FOLDED_ID)
        .setRepository(EPIC_REPOSITORY)
        .setSlug(FOLDED_SLUG)
        .setOutcome("done")
        .setChanged(NOW - 3_600L)
        .setLive(false)
        .setEndedSteps(0)
        .build()

    /** The one requirement a contracted epic carries, with its evidence. */
    private val requirement: EpicRequirement = EpicRequirement.newBuilder()
        .setId("R1")
        .setText("Synthetic requirement text")
        .setEvidence("Synthetic requirement evidence")
        .setKind("check")
        .build()

    /** The contract the epic was accepted with: one entry in each list, so every section has a body. */
    private fun contractProto(): EpicContract = EpicContract.newBuilder()
        .setTitle("Synthetic contract title")
        .setApproved("Synthetic approved line")
        .setIntent("Synthetic intent text.")
        .addFailures(
            EpicFailure.newBuilder()
                .setId("F1")
                .setText("Synthetic failure text")
                .setRating("limited")
                .setResponse("Synthetic failure response")
                .build(),
        )
        .setCare("Synthetic care text.")
        .addAdditions(
            EpicAddition.newBuilder()
                .setLocation("Synthetic addition location")
                .setWhat("Synthetic addition what")
                .setWhy("Synthetic addition why")
                .setCost("Synthetic addition cost")
                .build(),
        )
        .addPremises(
            EpicPremise.newBuilder()
                .setId("P1")
                .setText("Synthetic premise text")
                .setIfWrong("Synthetic premise if wrong")
                .build(),
        )
        .addExclusions("Synthetic exclusion text")
        .setApproach("Synthetic approach text.")
        .addAlternatives(
            EpicAlternative.newBuilder()
                .setAlternative("Synthetic alternative")
                .setWhyNot("Synthetic why not")
                .setReconsider("Synthetic reconsider")
                .build(),
        )
        .addReleaseSteps(
            EpicReleaseStep.newBuilder()
                .setStep("Synthetic release step")
                .setTarget("Synthetic release target")
                .setUndo("Synthetic release undo")
                .build(),
        )
        .setReleaseThen("Synthetic release then text.")
        .setChecks("Synthetic checks text.")
        .setBase("0123456789abcdef0123456789abcdef01234567")
        .setAccepted(NOW - 3_600L)
        .build()

    /** The title row of an open worker's session, whose last crumb names it. */
    private val workerSession: SemanticsMatcher = hasTestTag("crumb:2").and(hasText("Worker session"))

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

    /** Serves [text] as the brief: the first prompt of the conversation of the step [stepId], which then stays open. */
    private fun serveBrief(stepId: String, text: String) {
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(stepId)
            .setSeq(1L)
            .addItems(ConversationItemProto.newBuilder().setKind("prompt").setTime(NOW).setText(text).setSender("brief"))
            .build()
        server.route(WATCH_PATH) { request ->
            if (watchedId(request) == stepId) Reply.Stream(listOf(delta), StreamEnd.Open) else Reply.Failure(Code.NOT_FOUND, "", 0L)
        }
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

    private fun awaitRequest(path: String) {
        composeRule.awaitUntil {
            server.requests.any { it.rawPath == path }
        }
    }

    /** The body of the last request to [path]. */
    private fun requestBody(path: String): ByteArray = server.requests.last { it.rawPath == path }.body

    private fun awaitNone(matcher: SemanticsMatcher) {
        composeRule.awaitUntil {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isEmpty()
        }
    }

    private fun assertAbsent(matcher: SemanticsMatcher) {
        assertTrue(
            "expected no node matching $matcher",
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isEmpty(),
        )
    }

    private fun setAnimatorDurationScale(scale: Float) {
        Settings.Global.putFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, scale)
    }

    /** Launches the app and opens the epic from the home list, waiting for its summary. */
    private fun openEpicFromHome(): ActivityController<MainActivity> {
        val controller = launch()
        awaitNode(hasTestTag("hung:epic:$EPIC_ID"))
        composeRule.onNodeWithTag("hung:epic:$EPIC_ID").performClick()
        awaitNode(hasTestTag("epic-summary"))
        return controller
    }

    /** Taps the summary, then waits until the contract is read and the epic is gone. */
    private fun openContract() {
        composeRule.onNodeWithTag("epic-door-contract").performClick()
        awaitNode(hasTestTag("contract-body"))
        awaitNone(hasTestTag("epic-summary"))
    }

    /** Where the summary's top is, unclipped: where the epic's list has put it, above the list's top once scrolled. */
    private fun summaryTop(): Float = composeRule.onNodeWithTag("epic-summary").fetchSemanticsNode().positionInRoot.y

    /** Scrolls the epic's list until [section] shows, then returns where the summary's top is. */
    private fun scrollEpicListUp(section: String = "epic-section:now"): Float {
        val before = summaryTop()
        composeRule.onNode(hasScrollAction() and hasAnyDescendant(hasTestTag("epic-summary")))
            .performScrollToNode(hasTestTag(section))
        composeRule.waitForIdle()
        val after = summaryTop()
        assertTrue("expected the scroll to move the list up", after < before)
        return after
    }

    /** Runs the clock past [move]'s tween, whichever frame it began on, and lets the screens settle. */
    private fun finish(move: Move) {
        composeRule.mainClock.advanceTimeBy(2L * move.millis)
        composeRule.waitForIdle()
    }

    /** Scrolls the epic's list until its implementation section shows: the orrery and the task rows. */
    private fun showImplementation() {
        composeRule.onNode(hasScrollAction()).performScrollToNode(hasTestTag("epic-section:tasks"))
    }

    /** The session door of the implement stage, which opens the latest writing step's session. */
    private fun sessionDoor(): SemanticsMatcher =
        hasTestTag("open-session") and hasAnyAncestor(hasTestTag("stage-body:implement"))

    @Test
    fun homeDrawsTheLiveEpicsWithTheirRunningAndHeldSteps() {
        serveEpic()

        launch()

        awaitNode(hasTestTag("hung:epic:$EPIC_ID"))
        awaitNode(hasTestTag("step:$STEP_ID"))
        awaitNode(hasTestTag("step:$HELD_STEP_ID"))
        awaitNode(hasTestTag("convo:pane:example-host:%1"))
    }

    @Test
    fun homeReadsTheDetailOfALiveEpicAndDrawsItsCountAndBlocker() {
        serveEpic(questions = listOf(openQuestion()))

        launch()

        awaitNode(hasText("0 of 0 tasks done") and hasAnyAncestor(hasTestTag("hung:epic:$EPIC_ID")))
        awaitNode(hasText("waits on your answer ·", substring = true) and hasAnyAncestor(hasTestTag("hung:epic:$EPIC_ID")))
    }

    @Test
    fun aFailedDetailDrawsTheServerDidNotAnswer() {
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(liveEpic()).build())
        server.fail(EPICS_GET_PATH, Code.UNAVAILABLE, "synthetic failure")

        launch()

        awaitRequest(EPICS_GET_PATH)
        awaitNode(hasText("The server did not answer."))
        assertAbsent(hasTestTag("hung:epic:$EPIC_ID"))
    }

    @Test
    fun anEndedEpicIsNeverAskedFor() {
        server.respond(
            EPICS_LIST_PATH,
            EpicsListResponse.newBuilder().addEpics(liveEpic()).addEpics(foldedEpic()).build(),
        )
        server.respond(EPICS_GET_PATH, EpicsGetResponse.newBuilder().setEpic(liveEpic()).build())

        launch()

        awaitNode(hasText("0 of 0 tasks done") and hasAnyAncestor(hasTestTag("hung:epic:$EPIC_ID")))
        val asked = server.requests
            .filter { it.rawPath == EPICS_GET_PATH }
            .map { EpicsGetRequest.parseFrom(it.body).id }
        assertEquals(listOf(EPIC_ID), asked)
    }

    @Test
    fun tappingAnEpicOpensItsScreen() {
        serveEpic()
        launch()
        awaitNode(hasTestTag("hung:epic:$EPIC_ID"))

        composeRule.onNodeWithTag("hung:epic:$EPIC_ID").performClick()

        awaitNode(hasTestTag("title-back"))
        awaitNone(hasTestTag("convo:pane:example-host:%1"))
    }

    @Test
    fun backReturnsFromAnEpicToHome() {
        serveEpic()
        val controller = launch()
        awaitNode(hasTestTag("hung:epic:$EPIC_ID"))
        composeRule.onNodeWithTag("hung:epic:$EPIC_ID").performClick()
        awaitNode(hasTestTag("title-back"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("convo:pane:example-host:%1"))
        awaitNone(hasTestTag("title-back"))
    }

    @Test
    fun tappingAStepOpensTheSessionScreenAndBackReturnsToTheEpic() {
        serveEpic()
        server.respond(
            EPICS_HISTORY_PATH,
            EpicsHistoryResponse.newBuilder()
                .addEntries(
                    EpicHistoryEntry.newBuilder()
                        .setTime(NOW - 60)
                        .setCursor("example-cursor-1")
                        .setStep(epicStep()),
                )
                .build(),
        )
        serveSessions(headlessSession(STEP_ID, STEP_SESSION_LABEL))
        val controller = launch()
        awaitNode(hasTestTag("hung:epic:$EPIC_ID"))
        composeRule.onNodeWithTag("hung:epic:$EPIC_ID").performClick()
        awaitNode(hasTestTag("happened:0"))

        composeRule.onNodeWithTag("happened:0").performClick()

        awaitNode(workerSession)

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("title-back"))
        awaitNone(workerSession)
    }

    @Test
    fun tappingTheContractDoorOpensTheContractScreenWithItsHeadings() {
        serveEpic(contracted = true)
        openEpicFromHome()

        composeRule.onNodeWithTag("epic-door-contract").performClick()

        awaitNode(hasTestTag("contract-screen"))
        awaitNode(hasTestTag("contract-heading:0").and(hasText("WHAT YOU ASKED FOR")))
        awaitNone(hasTestTag("epic-summary"))
        composeRule.onNodeWithTag("crumb:0").assertTextEquals("orrery")
        composeRule.onNodeWithTag("crumb:1").assertTextEquals(EPIC_SLUG)
        composeRule.onNodeWithTag("crumb:2").assertTextEquals("contract")
    }

    @Test
    fun theOrreryCrumbGoesHome() {
        serveEpic(contracted = true)
        openEpicFromHome()
        openContract()

        composeRule.onNodeWithTag("crumb:0").performClick()

        awaitNode(hasTestTag("convo:pane:example-host:%1"))
        awaitNone(hasTestTag("contract-screen"))
    }

    @Test
    fun systemBackFromTheContractReturnsToTheEpicWithItsListAsLeft() {
        serveEpic(contracted = true, summary = TALL_SUMMARY)
        val controller = openEpicFromHome()
        val scrolled = scrollEpicListUp()
        openContract()

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("epic-summary"))
        awaitNone(hasTestTag("contract-screen"))
        assertEquals(scrolled, summaryTop(), 0f)
    }

    @Test
    fun theBackButtonReturnsToTheEpicWithItsListAsLeft() {
        serveEpic(contracted = true, summary = TALL_SUMMARY)
        openEpicFromHome()
        val scrolled = scrollEpicListUp()
        openContract()

        composeRule.onNodeWithTag("title-back").performClick()

        awaitNode(hasTestTag("epic-summary"))
        awaitNone(hasTestTag("contract-screen"))
        assertEquals(scrolled, summaryTop(), 0f)
    }

    @Test
    fun theSlugCrumbReturnsToTheEpicWithItsListAsLeft() {
        serveEpic(contracted = true, summary = TALL_SUMMARY)
        openEpicFromHome()
        val scrolled = scrollEpicListUp()
        openContract()

        composeRule.onNodeWithTag("crumb:1").performClick()

        awaitNode(hasTestTag("epic-summary"))
        awaitNone(hasTestTag("contract-screen"))
        assertEquals(scrolled, summaryTop(), 0f)
    }

    @Test
    fun atAnimatorScaleZeroTheContractReplacesTheEpicAtOnce() {
        setAnimatorDurationScale(0f)
        serveEpic(contracted = true)
        openEpicFromHome()
        composeRule.mainClock.autoAdvance = false

        composeRule.onNodeWithTag("epic-door-contract").performClick()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()

        composeRule.onNodeWithTag("contract-screen").assertExists()
        assertAbsent(hasTestTag("epic-summary"))
    }

    @Test
    fun atAnimatorScaleOneTheDrillAndTheLeaveMoveTheScreens() {
        setAnimatorDurationScale(1f)
        serveEpic(contracted = true)
        openEpicFromHome()
        composeRule.mainClock.autoAdvance = false

        composeRule.onNodeWithTag("epic-door-contract").performClick()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()
        composeRule.onNodeWithTag("epic-summary").assertExists()
        composeRule.onNodeWithTag("contract-screen").assertExists()
        finish(Move.DRILL)
        assertAbsent(hasTestTag("epic-summary"))
        composeRule.mainClock.autoAdvance = true
        awaitNode(hasTestTag("title-back"))
        composeRule.mainClock.autoAdvance = false

        composeRule.onNodeWithTag("title-back").performClick()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()
        // Mid-move both screens are drawn: the contract leaving and the epic, still reading, coming back.
        composeRule.onNodeWithTag("contract-screen").assertExists()
        val backs = composeRule.onAllNodes(hasTestTag("title-back"), useUnmergedTree = true).fetchSemanticsNodes()
        assertEquals(2, backs.size)
        finish(Move.LEAVE)
        assertAbsent(hasTestTag("contract-screen"))
    }

    @Test
    fun tappingATaskRowOpensThatTasksRecordAtItsNowStage() {
        serveEpic(contracted = true, steps = checkingSteps(), buildTasks = listOf(taskBuild()))
        openEpicFromHome()
        showImplementation()
        awaitNode(hasTestTag("task-row:$TASK_ID"))

        composeRule.onNodeWithTag("task-row:$TASK_ID").performClick()

        awaitNode(hasTestTag("stage-body:green"))
        composeRule.onNodeWithTag("crumb:1").assertTextEquals(TASK_TITLE)
        awaitNone(hasTestTag("epic-summary"))
    }

    @Test
    fun pressingAStageHeadOpensItsBodyInPlaceAndBackLeavesTheRecord() {
        serveEpic(contracted = true, steps = checkingSteps(), buildTasks = listOf(taskBuild()))
        val controller = openEpicFromHome()
        showImplementation()
        composeRule.onNodeWithTag("task-row:$TASK_ID").performClick()
        awaitNode(hasTestTag("stage-body:green"))

        composeRule.onNode(hasTestTag("stage-head:implement"), useUnmergedTree = true).performClick()

        awaitNode(hasTestTag("stage-body:implement"))
        awaitNone(hasTestTag("stage-body:green"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("epic-summary"))
        awaitNone(hasTestTag("stage-body:implement"))
    }

    @Test
    fun theEpicCrumbGoesBackToTheEpicRecord() {
        serveEpic(contracted = true, steps = checkingSteps(), buildTasks = listOf(taskBuild()))
        openEpicFromHome()
        showImplementation()
        composeRule.onNodeWithTag("task-row:$TASK_ID").performClick()
        awaitNode(hasTestTag("stage-body:green"))
        awaitNone(hasTestTag("epic-summary"))

        composeRule.onNodeWithTag("crumb:0").performClick()

        awaitNode(hasTestTag("epic-summary"))
        awaitNone(hasTestTag("stage-body:green"))
    }

    @Test
    fun openingTheStepsSessionThenBackReturnsToTheRecordThenTheEpicWithItsListAsLeft() {
        serveEpic(
            contracted = true,
            summary = TALL_SUMMARY,
            steps = checkingSteps(),
            buildTasks = listOf(taskBuild()),
        )
        serveSessions(headlessSession(CHECK_STEP_ID, CHECK_SESSION_LABEL))
        val controller = openEpicFromHome()
        val scrolled = scrollEpicListUp("epic-section:tasks")
        composeRule.onNodeWithTag("task-row:$TASK_ID").performClick()
        awaitNode(hasTestTag("stage-body:green"))
        composeRule.onNode(hasTestTag("stage-head:implement"), useUnmergedTree = true).performClick()
        awaitNode(sessionDoor())

        composeRule.onNode(sessionDoor(), useUnmergedTree = true).performClick()

        awaitNode(workerSession)
        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("stage-body:green"))
        awaitNone(workerSession)
        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("epic-summary"))
        awaitNone(hasTestTag("stage-body:green"))
        assertEquals(scrolled, summaryTop(), 0f)
    }

    @Test
    fun atAnimatorScaleZeroEachStageMoveSnaps() {
        setAnimatorDurationScale(0f)
        serveEpic(contracted = true, steps = checkingSteps(), buildTasks = listOf(taskBuild()))
        serveSessions(headlessSession(CHECK_STEP_ID, CHECK_SESSION_LABEL))
        val controller = openEpicFromHome()
        showImplementation()
        composeRule.mainClock.autoAdvance = false

        composeRule.onNodeWithTag("task-row:$TASK_ID").performClick()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()
        assertAbsent(hasTestTag("epic-summary"))

        composeRule.mainClock.autoAdvance = true
        awaitNode(hasTestTag("stage-body:green"))
        composeRule.onNodeWithTag("crumb:1").assertTextEquals(TASK_TITLE)
        composeRule.onNode(hasTestTag("stage-head:implement"), useUnmergedTree = true).performClick()
        awaitNode(sessionDoor())
        composeRule.mainClock.autoAdvance = false

        composeRule.onNode(sessionDoor(), useUnmergedTree = true).performClick()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()
        assertAbsent(hasTestTag("stage-head:implement"))

        composeRule.mainClock.autoAdvance = true
        awaitNode(workerSession)
        composeRule.mainClock.autoAdvance = false

        controller.get().onBackPressedDispatcher.onBackPressed()
        composeRule.waitForIdle()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()
        assertAbsent(workerSession)

        composeRule.mainClock.autoAdvance = true
        awaitNode(hasTestTag("stage-body:green"))
        composeRule.onNodeWithTag("crumb:1").assertTextEquals(TASK_TITLE)
        composeRule.mainClock.autoAdvance = false

        controller.get().onBackPressedDispatcher.onBackPressed()
        composeRule.waitForIdle()
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()
        assertAbsent(hasTestTag("crumb:1"))

        composeRule.mainClock.autoAdvance = true
        awaitNode(hasTestTag("epic-summary"))
    }

    @Test
    fun theRecordReadsThePlannersBriefFromTheFirstImplementersSession() {
        serveEpic(contracted = true, steps = checkingSteps(), buildTasks = listOf(taskBuild()))
        serveBrief(IMPLEMENT_STEP_ID, BRIEF_TEXT)
        openEpicFromHome()
        showImplementation()

        composeRule.onNodeWithTag("task-row:$TASK_ID").performClick()

        awaitNode(hasTestTag("task-brief"))
        composeRule.onNodeWithTag("task-brief").assertTextContains(BRIEF_TEXT, substring = true)
        composeRule.onNodeWithTag("task-requirement:R1")
            .assertTextContains("Synthetic requirement text", substring = true)
    }
}
