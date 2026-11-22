package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.content.Intent
import android.provider.Settings
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.text.TextLayoutResult
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.MainActivity
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicBuildTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicHistoryEntry
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsPauseRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsPauseResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsResumeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsResumeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsRetryRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsRetryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsStopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsStopResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceRun
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.push.PushNotifier
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.android.controller.ActivityController
import java.nio.ByteBuffer
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicRecordScreenTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val SLUG = "example-epic"
        const val NOW = 1_800_000_000L
        const val CHANGED = 4_102_444_800L
        const val STEERING = "example-session"
        const val STEERING_LABEL = "synthetic-steering-label"
        /** Long enough that the door's line runs past the phone's width in the test's text metrics, so it would wrap. */
        val LONG_LABEL = "synthetic-steering-label-".repeat(20)
        const val STEERING_CWD = "/home/user/example-worktree"
        const val STEERING_DOOR = "example-worktree · $STEERING_LABEL"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val EPICS_PAUSE = "/orrery.v1.Epics/Pause"
        const val EPICS_RESUME = "/orrery.v1.Epics/Resume"
        const val EPICS_STOP = "/orrery.v1.Epics/Stop"
        const val EPICS_RETRY = "/orrery.v1.Epics/Retry"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val WATCH = "/orrery.v1.Sessions/WatchConversation"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val ENVELOPE_HEADER_BYTES = 5
        const val PANE_ID = "%2"
        const val PANE_ORRERY_ID = "example-pane-orrery"
        const val PANE_LABEL = "synthetic-pane-label"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val openedStep = AtomicReference<StepRow?>(null)
    private val contractOpened = AtomicInteger()
    private val talked = AtomicReference<String?>(null)

    private fun step(id: String, task: String, agent: String): EpicStep =
        EpicStep.newBuilder()
            .setId(id)
            .setTask(task)
            .setAgent(agent)
            .setStatus("working")
            .setStarted(CHANGED - 600)
            .build()

    private fun body(
        steps: List<EpicStep> = emptyList(),
        requirements: List<EpicRequirement> = emptyList(),
        questions: List<EpicQuestion> = emptyList(),
        contract: EpicContract? = null,
        summary: String = "",
        problem: String = "",
        outcome: String = "",
        live: Boolean = true,
        paused: Boolean = false,
        parked: String = "",
        plannerParked: Boolean = false,
        phaseAt: Long = 0L,
        buildTasks: List<EpicBuildTask> = emptyList(),
        tasks: List<EpicTask> = emptyList(),
        steering: String = "",
        source: String = "orreryd",
    ): EpicsGetResponse {
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug(SLUG)
            .setOutcome(outcome)
            .setChanged(CHANGED)
            .setLive(live)
            .addAllRunningSteps(steps.filter { it.end.isEmpty() })
            .setEndedSteps(steps.count { it.end.isNotEmpty() })
            .setProblem(problem)
            .setPhaseAt(phaseAt)
            .setSource(source)
            .setSteering(steering)
        val response = EpicsGetResponse.newBuilder()
            .setEpic(epic)
            .setRepository("/home/user/example-repo")
            .addAllSteps(steps)
            .addAllRequirements(requirements)
            .addAllQuestions(questions)
            .addAllBuildTasks(buildTasks)
            .addAllTasks(tasks)
            .setSummary(summary)
            .setPaused(paused)
            .setParked(parked)
            .setPlannerParked(plannerParked)
        contract?.let { response.setContract(it) }
        return response.build()
    }

    private fun buildTask(id: String): EpicBuildTask =
        EpicBuildTask.newBuilder()
            .setId(id)
            .setTitle("Example task")
            .setStatus("working")
            .setStage("implementing")
            .build()

    private fun contract(title: String = "Example contract title"): EpicContract =
        EpicContract.newBuilder()
            .setTitle(title)
            .setCare("standard")
            .build()

    /** The tmux pane the steering conversation is listed as: its orrery id is [STEERING], in [STEERING_CWD]. */
    private fun steeringPane(label: String = STEERING_LABEL): PaneProto =
        PaneProto.newBuilder()
            .setId(PANE_ID)
            .setOrreryId(STEERING)
            .setCwd(STEERING_CWD)
            .setLabel(label)
            .build()

    private fun question(id: String, text: String, asked: Long): EpicQuestion =
        EpicQuestion.newBuilder().setId(id).setText(text).setTurn(1L).setAsked(asked).build()

    private fun evidenceRun(id: String, phase: String): EvidenceRun =
        EvidenceRun.newBuilder()
            .setId(id)
            .setPhase(phase)
            .setSource("captured")
            .setState("ended")
            .setVerdict("holds")
            .setPassed(true)
            .setRevision("0123456789abcdef0123456789abcdef01234567")
            .setCreated(CHANGED - 600)
            .setEnded(CHANGED - 60)
            .build()

    private fun requirement(
        id: String,
        text: String,
        kind: String,
        runs: List<EvidenceRun> = emptyList(),
    ): EpicRequirement =
        EpicRequirement.newBuilder()
            .setId(id)
            .setText(text)
            .setEvidence("example evidence")
            .setKind(kind)
            .addAllRuns(runs)
            .build()

    private fun historyEntry(time: Long, cursor: String, step: EpicStep): EpicHistoryEntry =
        EpicHistoryEntry.newBuilder()
            .setTime(time)
            .setCursor(cursor)
            .setStep(step)
            .setSentence("${step.task} running")
            .build()

    private fun historyOf(vararg entries: EpicHistoryEntry): EpicsHistoryResponse =
        EpicsHistoryResponse.newBuilder().addAllEntries(entries.toList()).setNext("").build()

    /** Serves the epic and its history, the two calls a load makes. */
    private fun serve(server: FakeOrreryServer, epic: EpicsGetResponse, history: EpicsHistoryResponse = historyOf()) {
        server.respond(EPICS_GET, epic)
        server.respond(EPICS_HISTORY, history)
    }

    private fun setScreen(
        server: FakeOrreryServer,
        listState: LazyListState = LazyListState(),
        openAt: EpicOpening? = null,
    ) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            EpicScreen(
                client = client,
                epicId = EPIC_ID,
                onBack = {},
                onOpenStep = { openedStep.set(it) },
                onOpenContract = { contractOpened.incrementAndGet() },
                onTalk = { talked.set(it) },
                listedSteerer = { steering -> steererOf(client.panes() + client.headless(), steering) },
                listState = listState,
                openAt = openAt,
                now = { NOW },
            )
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun countIn(tag: String, matcher: SemanticsMatcher): Int =
        countNodes(matcher and hasAnyAncestor(hasTestTag(tag)))

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil { countNodes(hasTestTag(tag)) > 0 }
    }

    private fun awaitGone(tag: String) {
        composeRule.awaitUntil { countNodes(hasTestTag(tag)) == 0 }
    }

    /** Waits until the epic has loaded: the now section is drawn only once it has. */
    private fun awaitLoaded() = awaitTag("epic-section:now")

    private fun clickTag(tag: String) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).performClick()
    }

    /**
     * Taps [tag] through its click action. A touch injected into a sheet's window does not reach the sheet's content
     * under Robolectric, so a tap inside a sheet is sent as the accessibility click that the same tap runs.
     */
    private fun tapInSheet(tag: String) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).performSemanticsAction(SemanticsActions.OnClick)
    }

    /** Scrolls the list until the node tagged [tag] is composed, as a test reaches a section or row. */
    private fun scrollTo(tag: String) {
        composeRule.onNode(hasScrollAction()).performScrollToNode(hasTestTag(tag))
    }

    /** The index of the item of section [name] in the list, read once the section is on screen. */
    private fun sectionIndex(listState: LazyListState, name: String): Int {
        scrollTo("epic-section:$name")
        return composeRule.runOnIdle {
            val visible = listState.layoutInfo.visibleItemsInfo.firstOrNull { it.key == "section:$name" }
            checkNotNull(visible) { "section $name is not on screen after it was scrolled to" }.index
        }
    }

    private fun isShown(listState: LazyListState, name: String): Boolean = composeRule.runOnIdle {
        listState.layoutInfo.visibleItemsInfo.any { it.key == "section:$name" }
    }

    /** The colour the one span of the text at [tag] is drawn in: how a word's tone reaches the screen. */
    private fun colourOf(tag: String): Color? = composeRule.onNodeWithTag(tag, useUnmergedTree = true)
        .fetchSemanticsNode()
        .config
        .getOrNull(SemanticsProperties.Text)
        ?.single()
        ?.spanStyles
        ?.single()
        ?.item
        ?.color

    /** The two Texts of the door tagged [tag] in the unmerged tree, its label first and then its line. */
    private fun doorTexts(tag: String): List<SemanticsNode> {
        val texts = composeRule.onAllNodes(
            hasAnyAncestor(hasTestTag(tag)) and SemanticsMatcher.keyIsDefined(SemanticsProperties.Text),
            useUnmergedTree = true,
        ).fetchSemanticsNodes()
        assertEquals(2, texts.size)
        return texts
    }

    /** The text layout the Text at [node] is drawn with: its line count and whether it overflows its width. */
    private fun textLayoutOf(node: SemanticsNode): TextLayoutResult {
        val layouts = mutableListOf<TextLayoutResult>()
        checkNotNull(node.config.getOrNull(SemanticsActions.GetTextLayoutResult)?.action).invoke(layouts)
        return layouts.single()
    }

    private fun requestsTo(server: FakeOrreryServer, path: String) = server.requests.filter { it.rawPath == path }

    private fun getCount(server: FakeOrreryServer): Int = requestsTo(server, EPICS_GET).size

    /** Waits until the epic has been read again, which a command does once it has been sent. */
    private fun awaitReload(server: FakeOrreryServer, afterReads: Int) {
        composeRule.awaitUntil { getCount(server) > afterReads }
    }

    @Test
    fun epicRecordOrderForABuildingEpicIsProblemSummaryTrackNowHappenedTasksRequirements() {
        val listState = LazyListState()
        val step = step("example-step-1", "example-task-1", "example-agent")
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    problem = "Example problem.",
                    contract = contract(),
                    summary = "Example summary.",
                    outcome = "built",
                    steps = listOf(step),
                    buildTasks = listOf(buildTask("example-task-1")),
                    requirements = listOf(requirement("R1", "Example requirement one.", "check")),
                ),
                historyOf(historyEntry(NOW - 60, "example-cursor-1", step)),
            )
            setScreen(server, listState = listState)
            awaitLoaded()

            val names = listOf("problem", "summary", "track", "now", "happened", "tasks", "requirements")
            val indexes = names.map { sectionIndex(listState, it) }
            assertTrue("expected the sections in order, got $indexes", indexes.zipWithNext().all { (a, b) -> a < b })
        }
    }

    @Test
    fun epicRecordAPlanningEpicDrawsItsSummaryTrackNowAndRequirementsOnly() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    contract = contract(),
                    summary = "Example summary.",
                    outcome = "accepted",
                    requirements = listOf(requirement("R1", "Example requirement one.", "check")),
                ),
            )
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "summary")
            sectionIndex(listState, "track")
            sectionIndex(listState, "requirements")
            assertEquals(0, countNodes(hasTestTag("epic-section:problem")))
            assertEquals(0, countNodes(hasTestTag("epic-section:happened")))
            assertEquals(0, countNodes(hasTestTag("epic-section:tasks")))
        }
    }

    @Test
    fun epicRecordARecordEpicDrawsOnlyNowAndTasks() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    source = "record",
                    contract = contract(),
                    summary = "Example summary.",
                    tasks = listOf(
                        EpicTask.newBuilder()
                            .setSlug("example-task-1")
                            .setSubject("Example task")
                            .setStatus("working")
                            .build(),
                    ),
                    requirements = listOf(requirement("R1", "Example requirement one.", "check")),
                ),
            )
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "tasks")
            assertEquals(0, countNodes(hasTestTag("epic-section:summary")))
            assertEquals(0, countNodes(hasTestTag("epic-section:track")))
            assertEquals(0, countNodes(hasTestTag("epic-section:happened")))
            assertEquals(0, countNodes(hasTestTag("epic-section:requirements")))
        }
    }

    @Test
    fun epicRecordDrawsNoReviewReleaseReportOrHandoverSection() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(outcome = "released", live = false, contract = contract(), buildTasks = listOf(buildTask("example-task-1"))),
            )
            setScreen(server)
            awaitLoaded()

            assertEquals(0, countNodes(hasTestTag("epic-section:review")))
            assertEquals(0, countNodes(hasTestTag("epic-section:release")))
            assertEquals(0, countNodes(hasTestTag("epic-section:report")))
            assertEquals(0, countNodes(hasTestTag("epic-section:handover")))
        }
    }

    @Test
    fun epicRecordTheSummaryDrawsItsContractAndSteeredDoors() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(summary = "Example summary.", contract = contract(), steering = STEERING))
            server.respond(PANES_LIST, PanesListResponse.newBuilder().addPanes(steeringPane()).build())
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "summary")
            awaitTag("epic-door-steered")
            assertEquals(1, countIn("epic-section:summary", hasText("Example summary.")))
            composeRule.onNodeWithTag("epic-door-contract").assertTextContains("standard care", substring = true)
            composeRule.onNodeWithTag("epic-door-steered").assertTextContains(STEERING_DOOR, substring = true)
            clickTag("epic-door-contract")
            clickTag("epic-door-steered")

            assertEquals(1, contractOpened.get())
            assertEquals(STEERING, talked.get())
        }
    }

    @Test
    fun epicRecordTheSummaryFallsBackToTheContractTitle() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(contract = contract(title = "Example contract title")))
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "summary")
            composeRule.onNodeWithTag("epic-summary", useUnmergedTree = true).assertTextEquals("Example contract title")
        }
    }

    @Test
    fun epicRecordTheSteeredDoorIsTitledSessionAndOpensItsIdWhenNoListedRowHoldsIt() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(summary = "Example summary.", steering = STEERING))
            server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "summary")
            awaitTag("epic-door-steered")
            composeRule.onNodeWithTag("epic-door-steered").assertTextEquals("Steered from", "session")
            clickTag("epic-door-steered")

            assertEquals(STEERING, talked.get())
        }
    }

    @Test
    fun epicRecordTheSteeredDoorIsNotDrawnWithoutASteerer() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(summary = "Example summary."))
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "summary")
            assertEquals(0, countNodes(hasTestTag("epic-door-steered")))
        }
    }

    @Test
    fun epicRecordEachDoorsLabelLaysOutInOneLineBesideItsLongLine() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(summary = "Example summary.", contract = contract(), steering = STEERING))
            server.respond(PANES_LIST, PanesListResponse.newBuilder().addPanes(steeringPane(LONG_LABEL)).build())
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "summary")
            awaitTag("epic-door-steered")
            for (tag in listOf("epic-door-contract", "epic-door-steered")) {
                val (label, line) = doorTexts(tag)
                assertEquals(1, textLayoutOf(label).lineCount)
                assertEquals(label.boundsInRoot.top.toDouble(), line.boundsInRoot.top.toDouble(), 0.0)
            }
        }
    }

    @Test
    fun epicRecordADoorsLongLineLaysOutOnOneLineWithinItsRow() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(summary = "Example summary.", steering = STEERING))
            server.respond(PANES_LIST, PanesListResponse.newBuilder().addPanes(steeringPane(LONG_LABEL)).build())
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "summary")
            awaitTag("epic-door-steered")
            val (_, line) = doorTexts("epic-door-steered")
            assertEquals(1, textLayoutOf(line).lineCount)
            val door = composeRule.onNodeWithTag("epic-door-steered", useUnmergedTree = true).fetchSemanticsNode()
            assertTrue(line.boundsInRoot.right <= door.boundsInRoot.right)
        }
    }

    @Test
    fun epicRecordJustHappenedShowsTheNewestThreeAndOpensTheRestInPlace() {
        val listState = LazyListState()
        val entries = (1..5).map { index ->
            historyEntry(
                NOW - 60,
                "example-cursor-$index",
                step("example-step-$index", "example-task-$index", "example-agent"),
            )
        }
        FakeOrreryServer().use { server ->
            serve(server, body(), historyOf(*entries.toTypedArray()))
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "happened")
            assertEquals(1, countIn("happened:0", hasText("example-task-5 running")))
            assertEquals(1, countIn("happened:2", hasText("example-task-3 running")))
            assertEquals(0, countNodes(hasTestTag("happened:3")))
            clickTag("happened-more")

            assertEquals(1, countIn("happened:4", hasText("example-task-1 running")))
        }
    }

    @Test
    fun epicRecordAStepEntryOpensItsStepWhenTapped() {
        val listState = LazyListState()
        val step = step("example-step-1", "example-task-1", "example-agent")
        FakeOrreryServer().use { server ->
            serve(server, body(steps = listOf(step)), historyOf(historyEntry(NOW - 60, "example-cursor-1", step)))
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "happened")
            clickTag("happened:0")
            clickTag("happened-open:0")

            assertEquals("example-step-1", openedStep.get()?.id)
        }
    }

    @Test
    fun epicRecordNowDrawsItsSentenceAndItsRunningStepRows() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "built",
                    steps = listOf(step("example-step-1", "example-task-1", "example-agent")),
                    buildTasks = listOf(buildTask("example-task-1")),
                ),
            )
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "now")
            composeRule.onNodeWithTag("now-sentence", useUnmergedTree = true)
                .assertTextEquals("One task is being worked on.")
            assertEquals(1, countIn("epic-section:now", hasTestTag("step-row:example-step-1")))
        }
    }

    @Test
    fun epicRecordTheChartDrawsAboveTheTaskTableUnderTheTasksHeading() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", buildTasks = listOf(buildTask("example-task-1"))))
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "tasks")
            assertEquals(1, countIn("epic-section:tasks", hasTestTag("epic-orrery")))
            assertEquals(1, countIn("epic-section:tasks", hasTestTag("task-row:example-task-1")))
            assertEquals(1, countIn("epic-section:tasks", hasText("0 of 1 done")))
            val chart = composeRule.onNodeWithTag("epic-orrery", useUnmergedTree = true).fetchSemanticsNode()
            val row = composeRule.onNodeWithTag("task-row:example-task-1", useUnmergedTree = true).fetchSemanticsNode()
            assertTrue(chart.positionInRoot.y < row.positionInRoot.y)
        }
    }

    @Test
    fun epicRecordTheRequirementsDrawUnderTheirHeadingWithTheProvenCount() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    requirements = listOf(
                        requirement("R1", "Example requirement one.", "check", runs = listOf(evidenceRun("example-run-2", "green"))),
                        requirement("R2", "Example requirement two.", "check"),
                    ),
                ),
            )
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "requirements")
            assertEquals(1, countIn("epic-section:requirements", hasText("1 of 2 proven")))
            assertEquals(1, countIn("epic-section:requirements", hasTestTag("req-row:R1")))
            assertEquals(1, countIn("epic-section:requirements", hasTestTag("req-row:R2")))
        }
    }

    @Test
    fun epicRecordAnOpeningOfASectionThatIsNotDrawnLeavesTheListAtItsTop() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body())
            setScreen(server, listState = listState, openAt = EpicOpening("happened"))
            awaitLoaded()

            assertEquals(0, composeRule.runOnIdle { listState.firstVisibleItemIndex })
        }
    }

    @Test
    fun epicRecordAnOpeningOfNowShowsTheNowSection() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(summary = "Example summary.", contract = contract()))
            setScreen(server, listState = listState, openAt = EpicOpening("now"))
            awaitLoaded()

            composeRule.awaitUntil { isShown(listState, "now") }
        }
    }

    @Test
    fun epicAskAnOpenQuestionIsDrawnInNowUnderTheSentence() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "built",
                    buildTasks = listOf(buildTask("example-task-1")),
                    questions = listOf(question("example-question-1", "Example question?", NOW - 120)),
                ),
            )
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "now")
            assertEquals(1, countIn("epic-section:now", hasTestTag("ask-question") and hasText("Example question?")))
            assertEquals(1, countIn("epic-section:now", hasTestTag("ask-confirm")))
            assertEquals(1, countNodes(hasTestTag("ask-question")))
            val sentenceTop = composeRule.onNodeWithTag("now-sentence", useUnmergedTree = true)
                .fetchSemanticsNode().boundsInRoot.top
            val askTop = composeRule.onNodeWithTag("ask-question", useUnmergedTree = true)
                .fetchSemanticsNode().boundsInRoot.top
            assertTrue("the Ask is drawn under the sentence", sentenceTop < askTop)
        }
    }

    @Test
    fun epicAskAnOwnersParkIsDrawnInNowAsItsAskWithItsReasonRetryAndStop() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", parked = "rate limit"))
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "now")
            assertEquals(1, countIn("epic-section:now", hasTestTag("epic-park")))
            assertEquals(1, countIn("epic-section:now", hasTestTag("ask-label") and hasText("ASKS YOU")))
            assertEquals(1, countIn("epic-section:now", hasTestTag("ask-question") and hasText("Rate limit.")))
            composeRule.onNodeWithTag("ask-option:1").assertTextContains("Retry")
            composeRule.onNodeWithTag("epic-park-stop").assertTextEquals("Stop…")
            // The park's one answer, its confirm, and Stop…; a park has no Something else.
            assertEquals(3, countIn("epic-section:now", hasClickAction() and hasAnyAncestor(hasTestTag("epic-park"))))
            assertEquals(0, countNodes(hasTestTag("ask-other")))
        }
    }

    @Test
    fun epicAskAPlannersParkIsOneLineInNowWithNoControlAndNoAsk() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", parked = "rate limit", plannerParked = true))
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "now")
            assertEquals(
                1,
                countIn(
                    "epic-section:now",
                    hasTestTag("epic-planner-park") and
                        hasText("The planner holds this park: rate limit. It will retry, replan, or ask you."),
                ),
            )
            assertEquals(0, countNodes(hasTestTag("epic-park")))
            assertEquals(0, countNodes(hasTestTag("ask")))
            assertEquals(0, countNodes(hasTestTag("epic-park-stop")))
            assertEquals(0, countNodes(hasTestTag("ask-question")))
        }
    }

    @Test
    fun epicRecordTheProblemIsDrawnInItsSectionWithNoNoticeOrReadLine() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(problem = "Example problem.", outcome = "built"))
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "problem")
            assertEquals(1, countIn("epic-section:problem", hasTestTag("epic-problem") and hasText("Example problem.")))
            assertEquals(0, countNodes(hasTestTag("epic-notice")))
            assertEquals(0, countNodes(hasText("The server could not read this epic.")))
            assertEquals(0, countNodes(hasText("loading the epic…")))
        }
    }

    @Test
    fun epicWordsThePausedPillIsQuiet() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", paused = true))
            setScreen(server)
            awaitTag("title-state")

            composeRule.onNodeWithTag("title-state", useUnmergedTree = true).assertTextEquals("paused")
            assertEquals(OrreryTokens.Ink.three, colourOf("title-state"))
        }
    }

    @Test
    fun epicWordsABuildingPillIsLive() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", buildTasks = listOf(buildTask("example-task-1"))))
            setScreen(server)
            awaitTag("title-state")

            composeRule.onNodeWithTag("title-state", useUnmergedTree = true).assertTextEquals("building")
            assertEquals(OrreryTokens.Role.live, colourOf("title-state"))
        }
    }

    @Test
    fun epicWordsAnEndedEpicsPillIsOk() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "released", live = false, phaseAt = NOW - 120))
            setScreen(server)
            awaitTag("title-state")

            composeRule.onNodeWithTag("title-state", useUnmergedTree = true).assertTextEquals("released · 2 min")
            assertEquals(OrreryTokens.Role.ok, colourOf("title-state"))
        }
    }

    @Test
    fun epicWordsThePillTimeIsHowLongItsPhaseHasLasted() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", buildTasks = listOf(buildTask("example-task-1")), phaseAt = NOW - 2_940))
            setScreen(server)
            awaitTag("title-state")

            composeRule.onNodeWithTag("title-state", useUnmergedTree = true).assertTextEquals("building · 49 min")
        }
    }

    @Test
    fun epicWordsAPillWithNoPhaseTimeReadsTheWordAlone() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", buildTasks = listOf(buildTask("example-task-1")), phaseAt = 0L))
            setScreen(server)
            awaitTag("title-state")

            composeRule.onNodeWithTag("title-state", useUnmergedTree = true).assertTextEquals("building")
        }
    }

    @Test
    fun epicWordsAPillWhosePhaseIsAfterNowReadsZeroSeconds() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", buildTasks = listOf(buildTask("example-task-1")), phaseAt = NOW + 60))
            setScreen(server)
            awaitTag("title-state")

            composeRule.onNodeWithTag("title-state", useUnmergedTree = true).assertTextEquals("building · 0 s")
        }
    }

    @Test
    fun epicWordsAnOpenQuestionMakesTheBlockerAttentionWithItsWait() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "built",
                    buildTasks = listOf(buildTask("example-task-1")),
                    questions = listOf(question("example-question-1", "Example question?", NOW - 120)),
                ),
            )
            setScreen(server)
            awaitTag("title-blocker")

            composeRule.onNodeWithTag("title-blocker", useUnmergedTree = true)
                .assertTextEquals("asks you a question · 2 min")
            assertEquals(OrreryTokens.Role.attention, colourOf("title-blocker"))
        }
    }

    @Test
    fun epicCommandsTheTitleDrawsNoCommandButtons() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built"))
            setScreen(server)
            awaitTag("title-back")

            assertEquals(0, countNodes(hasTestTag("epic-pause")))
            assertEquals(0, countNodes(hasTestTag("epic-resume")))
            assertEquals(0, countNodes(hasTestTag("epic-stop")))
        }
    }

    @Test
    fun epicCommandsAnEndedEpicDrawsNoMore() {
        FakeOrreryServer().use { server ->
            serve(server, body(source = "record", live = false))
            setScreen(server)
            awaitLoaded()

            assertEquals(0, countNodes(hasTestTag("epic-more")))
        }
    }

    @Test
    fun epicCommandsAnOrrerydEpicInCleanOffersStopAndNotPause() {
        assertTrackPhaseOffersStopNotPause("clean")
    }

    @Test
    fun epicCommandsAnOrrerydEpicInReleasingOffersStopAndNotPause() {
        assertTrackPhaseOffersStopNotPause("releasing")
    }

    @Test
    fun epicCommandsARecordEpicWithNoOutcomeOffersItsCommands() {
        FakeOrreryServer().use { server ->
            serve(server, body(source = "record", outcome = ""))
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-pause")
            assertEquals(1, countNodes(hasTestTag("epic-sheet-stop")))
            assertEquals(0, countNodes(hasTestTag("epic-sheet-resume")))
        }
    }

    @Test
    fun epicCommandsARecordEpicWithAnOutcomeOffersNoCommands() {
        FakeOrreryServer().use { server ->
            serve(server, body(source = "record", outcome = "built"))
            setScreen(server)
            awaitLoaded()

            assertEquals(0, countNodes(hasTestTag("epic-more")))
        }
    }

    /** Opens the More sheet of a live orreryd epic in [outcome]: it offers Stop, and does not offer Pause. */
    private fun assertTrackPhaseOffersStopNotPause(outcome: String) {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = outcome))
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-stop")
            assertEquals(0, countNodes(hasTestTag("epic-sheet-pause")))
        }
    }

    @Test
    fun epicCommandsAnOwnerParkedEpicsMoreListsStopAndNotPause() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", parked = "rate limit"))
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-stop")
            assertEquals(0, countNodes(hasTestTag("epic-sheet-pause")))
        }
    }

    @Test
    fun epicCommandsAPlannerParkedEpicsMoreListsPause() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", parked = "rate limit", plannerParked = true))
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-pause")
            assertEquals(1, countNodes(hasTestTag("epic-sheet-stop")))
        }
    }

    @Test
    fun epicCommandsMoreListsPauseAndPauseCallsOnce() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built"))
            server.respond(EPICS_PAUSE, EpicsPauseResponse.getDefaultInstance())
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-pause")
            assertEquals(0, countNodes(hasTestTag("epic-sheet-resume")))
            tapInSheet("epic-sheet-pause")

            composeRule.awaitUntil { requestsTo(server, EPICS_PAUSE).isNotEmpty() }
            assertEquals(1, requestsTo(server, EPICS_PAUSE).size)
            assertEquals(EPIC_ID, EpicsPauseRequest.parseFrom(requestsTo(server, EPICS_PAUSE).single().body).id)
        }
    }

    @Test
    fun epicCommandsMoreListsResumeWhenPausedAndResumeCallsOnce() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", paused = true))
            server.respond(EPICS_RESUME, EpicsResumeResponse.getDefaultInstance())
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-resume")
            assertEquals(0, countNodes(hasTestTag("epic-sheet-pause")))
            tapInSheet("epic-sheet-resume")

            composeRule.awaitUntil { requestsTo(server, EPICS_RESUME).isNotEmpty() }
            assertEquals(1, requestsTo(server, EPICS_RESUME).size)
            assertEquals(EPIC_ID, EpicsResumeRequest.parseFrom(requestsTo(server, EPICS_RESUME).single().body).id)
        }
    }

    @Test
    fun epicCommandsStopAsksFirstAndKeepSendsNothing() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built"))
            server.respond(EPICS_STOP, EpicsStopResponse.getDefaultInstance())
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-stop")
            tapInSheet("epic-sheet-stop")
            awaitTag("epic-stop-title")
            composeRule.onNodeWithTag("epic-stop-title", useUnmergedTree = true).assertTextEquals("Stop example-epic?")
            tapInSheet("epic-stop-keep")

            awaitGone("epic-stop-title")
            assertEquals(0, requestsTo(server, EPICS_STOP).size)
        }
    }

    @Test
    fun epicCommandsStopAsksFirstAndConfirmSendsStopOnce() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built"))
            server.respond(EPICS_STOP, EpicsStopResponse.getDefaultInstance())
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-stop")
            tapInSheet("epic-sheet-stop")
            awaitTag("epic-stop-confirm")
            tapInSheet("epic-stop-confirm")

            composeRule.awaitUntil { requestsTo(server, EPICS_STOP).isNotEmpty() }
            assertEquals(1, requestsTo(server, EPICS_STOP).size)
            assertEquals(EPIC_ID, EpicsStopRequest.parseFrom(requestsTo(server, EPICS_STOP).single().body).id)
        }
    }

    @Test
    fun epicCommandsTheParkStopAsksFirstAndConfirmSendsStopOnce() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", parked = "rate limit"))
            server.respond(EPICS_STOP, EpicsStopResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("epic-park-stop")

            clickTag("epic-park-stop")
            assertEquals(1, countNodes(hasTestTag("now-stop-sure")))
            assertEquals(0, requestsTo(server, EPICS_STOP).size)
            clickTag("now-stop-confirm")

            composeRule.awaitUntil { requestsTo(server, EPICS_STOP).isNotEmpty() }
            assertEquals(1, requestsTo(server, EPICS_STOP).size)
            assertEquals(EPIC_ID, EpicsStopRequest.parseFrom(requestsTo(server, EPICS_STOP).single().body).id)
        }
    }

    @Test
    fun epicCommandsTheParkRetryCallsRetryOnce() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", parked = "rate limit"))
            server.respond(EPICS_RETRY, EpicsRetryResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("ask-option:1")

            clickTag("ask-option:1")
            assertEquals(0, requestsTo(server, EPICS_RETRY).size)
            clickTag("ask-confirm")

            composeRule.awaitUntil { requestsTo(server, EPICS_RETRY).isNotEmpty() }
            assertEquals(1, requestsTo(server, EPICS_RETRY).size)
            assertEquals(EPIC_ID, EpicsRetryRequest.parseFrom(requestsTo(server, EPICS_RETRY).single().body).id)
        }
    }

    @Test
    fun epicCommandsADidNotLandLineShowsTheReasonUnderTheTitle() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built"))
            server.fail(EPICS_PAUSE, Code.FAILED_PRECONDITION, "epic is closed")
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-pause")
            tapInSheet("epic-sheet-pause")

            awaitTag("epic-notice")
            composeRule.onNodeWithTag("epic-notice", useUnmergedTree = true)
                .assertTextEquals("Pause did not land: epic is closed.")
        }
    }

    @Test
    fun epicCommandsALandedCommandClearsTheDidNotLandLine() {
        val pauses = AtomicInteger()
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built"))
            server.route(EPICS_PAUSE) {
                if (pauses.incrementAndGet() == 1) {
                    Reply.Failure(Code.FAILED_PRECONDITION, "epic is closed", 0L)
                } else {
                    Reply.Message(EpicsPauseResponse.getDefaultInstance(), 0L)
                }
            }
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-pause")
            tapInSheet("epic-sheet-pause")
            awaitTag("epic-notice")

            clickTag("epic-more")
            awaitTag("epic-sheet-pause")
            tapInSheet("epic-sheet-pause")

            composeRule.awaitUntil { pauses.get() == 2 }
            composeRule.awaitUntil { countNodes(hasTestTag("epic-notice")) == 0 }
        }
    }

    @Test
    fun epicCommandsATransportFailureSaysTheServerDidNotAnswer() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built"))
            server.drop(EPICS_PAUSE)
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-pause")
            tapInSheet("epic-sheet-pause")

            awaitTag("epic-notice")
            composeRule.onNodeWithTag("epic-notice", useUnmergedTree = true)
                .assertTextEquals("Pause did not land: the server did not answer.")
        }
    }

    @Test
    fun epicCommandsARetryThatDidNotLandNamesRetryAndItsReason() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", parked = "rate limit"))
            server.fail(EPICS_RETRY, Code.FAILED_PRECONDITION, "epic is closed")
            setScreen(server)
            awaitTag("ask-option:1")

            clickTag("ask-option:1")
            clickTag("ask-confirm")

            awaitTag("epic-notice")
            composeRule.onNodeWithTag("epic-notice", useUnmergedTree = true)
                .assertTextEquals("Retry did not land: epic is closed.")
        }
    }

    @Test
    fun epicCommandsTheLoadingLineShowsBeforeTheEpicIsRead() {
        val release = CountDownLatch(1)
        FakeOrreryServer().use { server ->
            server.route(EPICS_GET) {
                release.await()
                Reply.Message(body(outcome = "built"), 0L)
            }
            server.respond(EPICS_HISTORY, historyOf())
            setScreen(server)
            try {
                awaitTag("title-back")
                composeRule.onNodeWithText("loading the epic…").assertExists()
                assertEquals(0, countNodes(hasTestTag("epic-section:now")))
            } finally {
                release.countDown()
            }
            awaitLoaded()
        }
    }

    @Test
    fun epicCommandsAFailedReadShowsCouldNotReadAndTryAgainReadsTheEpic() {
        FakeOrreryServer().use { server ->
            server.fail(EPICS_GET, Code.UNAVAILABLE, "synthetic outage")
            server.respond(EPICS_HISTORY, historyOf())
            setScreen(server)
            composeRule.awaitUntil { countNodes(hasText("The server could not read this epic.")) > 0 }

            server.respond(EPICS_GET, body(outcome = "built", problem = "Loaded after retry"))
            composeRule.onNodeWithText("Try again").performClick()

            awaitLoaded()
            composeRule.onNodeWithText("Loaded after retry").assertExists()
        }
    }

    @Test
    fun epicCommandsADidNotLandLineIsDrawnAloneWithNoLoadingOrReadLine() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built"))
            server.fail(EPICS_PAUSE, Code.FAILED_PRECONDITION, "epic is closed")
            setScreen(server)
            awaitLoaded()

            clickTag("epic-more")
            awaitTag("epic-sheet-pause")
            tapInSheet("epic-sheet-pause")

            awaitTag("epic-notice")
            composeRule.onNodeWithTag("epic-notice", useUnmergedTree = true)
                .assertTextEquals("Pause did not land: epic is closed.")
            assertEquals(0, countNodes(hasText("loading the epic…")))
            assertEquals(0, countNodes(hasText("The server could not read this epic.")))
            assertEquals(0, countNodes(hasTestTag("epic-problem")))
        }
    }

    @Test
    fun epicCommandsTheLoadingLineIsDrawnAloneWithNoProblemNoNoticeAndNoReadLine() {
        val release = CountDownLatch(1)
        FakeOrreryServer().use { server ->
            server.route(EPICS_GET) {
                release.await()
                Reply.Message(body(outcome = "built", problem = "Example problem."), 0L)
            }
            server.respond(EPICS_HISTORY, historyOf())
            setScreen(server)
            try {
                awaitTag("title-back")
                assertEquals(1, countNodes(hasText("loading the epic…")))
                assertEquals(0, countNodes(hasText("Example problem.")))
                assertEquals(0, countNodes(hasTestTag("epic-notice")))
                assertEquals(0, countNodes(hasText("The server could not read this epic.")))
            } finally {
                release.countDown()
            }
            awaitLoaded()
        }
    }

    @Test
    fun epicCommandsACouldNotReadLineIsDrawnAloneWithNoLoadingNoNoticeAndNoNow() {
        FakeOrreryServer().use { server ->
            server.fail(EPICS_GET, Code.UNAVAILABLE, "synthetic outage")
            server.respond(EPICS_HISTORY, historyOf())
            setScreen(server)
            composeRule.awaitUntil { countNodes(hasText("The server could not read this epic.")) > 0 }

            assertEquals(0, countNodes(hasText("loading the epic…")))
            assertEquals(0, countNodes(hasTestTag("epic-notice")))
            assertEquals(0, countNodes(hasTestTag("epic-section:now")))
            assertEquals(0, countNodes(hasTestTag("epic-problem")))
        }
    }

    @Test
    fun epicCommandsTheListKeepsItsScrollWhenACommandReloadsTheEpic() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "built",
                    requirements = (1..40).map { requirement("R$it", "Example requirement $it.", "check") },
                ),
            )
            server.respond(EPICS_PAUSE, EpicsPauseResponse.getDefaultInstance())
            setScreen(server, listState = listState)
            awaitLoaded()

            sectionIndex(listState, "requirements")
            val before = composeRule.runOnIdle { listState.firstVisibleItemIndex }
            assertTrue(before > 0)

            val reads = getCount(server)
            clickTag("epic-more")
            awaitTag("epic-sheet-pause")
            tapInSheet("epic-sheet-pause")
            awaitReload(server, reads)
            composeRule.waitForIdle()

            assertEquals(before, composeRule.runOnIdle { listState.firstVisibleItemIndex })
        }
    }

    @Test
    fun epicCommandsTheTalkButtonOpensTheSteererThroughOnTalk() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built", steering = STEERING))
            setScreen(server)
            awaitTag("epic-talk-bar")

            clickTag("epic-talk-bar")

            assertEquals(STEERING, talked.get())
        }
    }

    @Test
    fun epicCommandsNoTalkButtonIsDrawnWithoutASteerer() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "built"))
            setScreen(server)
            awaitLoaded()

            assertEquals(0, countNodes(hasTestTag("epic-talk-bar")))
        }
    }

    /** Launches the app on the epic [EPIC_ID] at the top, as a notice opens it, with its settings pointed at [server]. */
    private fun launchOnEpic(server: FakeOrreryServer): ActivityController<MainActivity> {
        val context = ApplicationProvider.getApplicationContext<Context>()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
        ServerSettings(context).saveServerUrl(server.baseUrl)
        Settings.Global.putFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 0f)
        val intent = Intent(context, MainActivity::class.java).putExtra(PushNotifier.EXTRA_EPIC, EPIC_ID)
        return Robolectric.buildActivity(MainActivity::class.java, intent).setup()
    }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    private fun titled(label: String): SemanticsMatcher = hasTestTag("head-text").and(hasText(label))

    /** The steering headless session, working: the foot reads it as running, so the foot keeps its Talk. */
    private fun steeringSession(): HeadlessSession = HeadlessSession.newBuilder()
        .setId(STEERING)
        .setDir("/home/user/example-repo")
        .setLabel(STEERING_LABEL)
        .setState("working")
        .setChanged(CHANGED)
        .build()

    /** The watch stream of the steering session: its state, as the session's screen opens. */
    private fun steeringState(): SessionsWatchConversationResponse = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(STEERING)
        .setSeq(1L)
        .setState(
            ConversationState.newBuilder()
                .setOrigin("synthetic origin")
                .setSession(steeringSession()),
        )
        .build()

    /** The id a watch request names: its enveloped body, one flag byte, a big-endian length, then the payload. */
    private fun watchedId(request: RecordedRequest): String {
        val length = ByteBuffer.wrap(request.body, 1, 4).int
        return SessionsWatchConversationRequest.parseFrom(
            request.body.copyOfRange(ENVELOPE_HEADER_BYTES, ENVELOPE_HEADER_BYTES + length),
        ).id
    }

    @Test
    fun epicCommandsTalkOpensTheSteeringHeadlessSessionOnItsOwnScreen() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body(outcome = "built", steering = STEERING))
            server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
            server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
            server.respond(LIST_HEADLESS, ListHeadlessResponse.newBuilder().addSessions(steeringSession()).build())
            server.route(WATCH) { request ->
                if (watchedId(request) == STEERING) {
                    Reply.Stream(listOf(steeringState()), StreamEnd.Open)
                } else {
                    Reply.Failure(Code.NOT_FOUND, "", 0L)
                }
            }
            val app = launchOnEpic(server)
            try {
                awaitNode(hasTestTag("epic-talk-bar"))

                composeRule.onNodeWithTag("epic-talk-bar").performClick()

                awaitNode(titled(STEERING_LABEL))
            } finally {
                app.pause().stop().destroy()
            }
        }
    }

    @Test
    fun epicCommandsTalkOpensTheSteeringTmuxPaneOnItsOwnScreen() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, body(outcome = "built", steering = PANE_ORRERY_ID))
            server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
            server.respond(
                PANES_LIST,
                PanesListResponse.newBuilder()
                    .addPanes(PaneProto.newBuilder().setId(PANE_ID).setOrreryId(PANE_ORRERY_ID).setLabel(PANE_LABEL))
                    .build(),
            )
            val app = launchOnEpic(server)
            try {
                awaitNode(hasTestTag("epic-talk-bar"))

                composeRule.onNodeWithTag("epic-talk-bar").performClick()

                awaitNode(titled(PANE_LABEL))
            } finally {
                app.pause().stop().destroy()
            }
        }
    }
}
