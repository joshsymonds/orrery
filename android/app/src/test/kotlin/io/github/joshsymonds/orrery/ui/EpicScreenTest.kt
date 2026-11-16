package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicBuildTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicHistoryEntry
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicNode
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseProof
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReport
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsAnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsAnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryRequest
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
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicScreenTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val NOW = 1_800_000_000L
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val EPICS_PAUSE = "/orrery.v1.Epics/Pause"
        const val EPICS_RESUME = "/orrery.v1.Epics/Resume"
        const val EPICS_STOP = "/orrery.v1.Epics/Stop"
        const val EPICS_RETRY = "/orrery.v1.Epics/Retry"
        const val EPICS_ANSWER = "/orrery.v1.Epics/Answer"
        const val CHANGED = 4_102_444_800L
        const val REVISION = "0123456789abcdef0123456789abcdef01234567"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val openedStep = AtomicReference<StepRow?>(null)
    private val contractOpened = AtomicInteger()
    private val backed = AtomicInteger()

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
        nodes: List<EpicNode> = emptyList(),
        questions: List<EpicQuestion> = emptyList(),
        contract: EpicContract? = null,
        summary: String = "",
        problem: String = "",
        outcome: String = "",
        live: Boolean = true,
        paused: Boolean = false,
        parked: String = "",
        phaseAt: Long = 0L,
        buildTasks: List<EpicBuildTask> = emptyList(),
        releaseProof: List<EpicReleaseProof> = emptyList(),
        report: EpicReport? = null,
    ): EpicsGetResponse {
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug("example-epic")
            .setOutcome(outcome)
            .setChanged(CHANGED)
            .setLive(live)
            .addAllRunningSteps(steps.filter { it.end.isEmpty() })
            .setEndedSteps(steps.count { it.end.isNotEmpty() })
            .setProblem(problem)
            .setPhaseAt(phaseAt)
        val response = EpicsGetResponse.newBuilder()
            .setEpic(epic)
            .addAllSteps(steps)
            .addAllRequirements(requirements)
            .addAllNodes(nodes)
            .addAllQuestions(questions)
            .addAllBuildTasks(buildTasks)
            .addAllReleaseSteps(releaseProof)
            .setSummary(summary)
            .setPaused(paused)
            .setParked(parked)
        contract?.let { response.setContract(it) }
        report?.let { response.setReport(it) }
        return response.build()
    }

    private fun buildTask(id: String): EpicBuildTask =
        EpicBuildTask.newBuilder()
            .setId(id)
            .setTitle("Example task")
            .setStatus("working")
            .setStage("implementing")
            .build()

    private fun releaseProof(
        position: Long,
        state: String,
        evidence: String = "",
        reason: String = "",
    ): EpicReleaseProof =
        EpicReleaseProof.newBuilder()
            .setPosition(position)
            .setStep("example release step")
            .setTarget("example target")
            .setUndo("example undo")
            .setState(state)
            .setEvidence(evidence)
            .setReason(reason)
            .setAttempt(1L)
            .setAt(CHANGED - 60)
            .build()

    private fun report(outcome: String, shipped: List<String>, notShipped: List<String>, why: String): EpicReport =
        EpicReport.newBuilder()
            .setOutcome(outcome)
            .addAllShipped(shipped)
            .addAllNotShipped(notShipped)
            .setWhy(why)
            .build()

    private fun contract(title: String = "Example contract title"): EpicContract =
        EpicContract.newBuilder()
            .setTitle(title)
            .addReleaseSteps(
                EpicReleaseStep.newBuilder()
                    .setStep("example release step")
                    .setTarget("example target")
                    .setUndo("example undo"),
            )
            .setReleaseThen("example release then")
            .build()

    private fun question(id: String, text: String, asked: Long): EpicQuestion =
        EpicQuestion.newBuilder().setId(id).setText(text).setTurn(1L).setAsked(asked).build()

    private fun node(path: String, kind: String): EpicNode =
        EpicNode.newBuilder()
            .setPath(path)
            .setNode("example-node")
            .setKind(kind)
            .setState("ended")
            .setRound(1L)
            .setRevision("example-revision")
            .build()

    private fun evidenceRun(id: String, phase: String): EvidenceRun =
        EvidenceRun.newBuilder()
            .setId(id)
            .setPhase(phase)
            .setSource("captured")
            .setState("ended")
            .setVerdict("holds")
            .setPassed(true)
            .setRevision(REVISION)
            .setCreated(CHANGED - 600)
            .setEnded(CHANGED - 60)
            .build()

    private fun requirement(
        id: String,
        text: String,
        kind: String,
        conclusion: String = "",
        runs: List<EvidenceRun> = emptyList(),
    ): EpicRequirement =
        EpicRequirement.newBuilder()
            .setId(id)
            .setText(text)
            .setEvidence("example evidence")
            .setKind(kind)
            .setConclusion(conclusion)
            .addAllRuns(runs)
            .build()

    private fun historyEntry(time: Long, cursor: String, step: EpicStep): EpicHistoryEntry =
        EpicHistoryEntry.newBuilder().setTime(time).setCursor(cursor).setStep(step).build()

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
        onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null,
        actions: @Composable RowScope.() -> Unit = {},
        handover: (@Composable (EpicDetail) -> Unit)? = null,
    ) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            EpicScreen(
                client = client,
                epicId = EPIC_ID,
                onBack = { backed.incrementAndGet() },
                onOpenStep = { openedStep.set(it) },
                onOpenContract = { contractOpened.incrementAndGet() },
                listState = listState,
                onOpenPhase = onOpenPhase,
                actions = actions,
                handover = handover,
                now = { NOW },
            )
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun countIn(tag: String, matcher: SemanticsMatcher): Int =
        countNodes(matcher and hasAnyAncestor(hasTestTag(tag)))

    private fun awaitText(text: String) {
        composeRule.awaitUntil { countNodes(hasText(text)) > 0 }
    }

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil { countNodes(hasTestTag(tag)) > 0 }
    }

    /** Waits until the epic has loaded: the now section is drawn only once it has. */
    private fun awaitLoaded() = awaitTag("epic-section:now")

    private fun clickTag(tag: String) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).performClick()
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

    private fun stateOf(tag: String): String? =
        composeRule.onNodeWithTag(tag, useUnmergedTree = true)
            .fetchSemanticsNode()
            .config
            .getOrNull(SemanticsProperties.StateDescription)

    private fun requestsTo(server: FakeOrreryServer, path: String) = server.requests.filter { it.rawPath == path }

    private fun getCount(server: FakeOrreryServer): Int = requestsTo(server, EPICS_GET).size

    /** Waits until the epic has been read again, which a command does once it has been sent. */
    private fun awaitReload(server: FakeOrreryServer, afterReads: Int) {
        composeRule.awaitUntil { getCount(server) > afterReads }
    }

    @Test
    fun theSectionsShowInTheEpicPagesOrder() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    requirements = listOf(requirement("R1", "Example requirement one.", "check")),
                    contract = contract(),
                    summary = "Example summary.",
                ),
            )
            setScreen(server, listState = listState)
            awaitLoaded()

            val names = listOf("summary", "now", "red", "implementation", "green", "release", "report")
            val indexes = names.map { sectionIndex(listState, it) }
            assertTrue("expected the sections in order, got $indexes", indexes.zipWithNext().all { (a, b) -> a < b })
            assertEquals(0, countNodes(hasTestTag("epic-section:handover")))
        }
    }

    @Test
    fun theHandoverShowsBetweenGreenAndReleaseOnlyWhenGiven() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(contract = contract()))
            setScreen(server, listState = listState, handover = { Text("Example handover.") })
            awaitLoaded()

            val indexes = listOf("green", "handover", "release").map { sectionIndex(listState, it) }
            assertTrue("expected green, handover, release in order, got $indexes", indexes.zipWithNext().all { (a, b) -> a < b })
            assertEquals(1, countIn("epic-section:handover", hasText("Example handover.")))
        }
    }

    @Test
    fun theHandoverIsGivenTheLoadedEpic() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(contract = contract()))
            setScreen(
                server,
                listState = listState,
                handover = { detail -> Text("Example handover for ${detail.epic.slug}.") },
            )
            awaitLoaded()

            assertEquals(1, countIn("epic-section:handover", hasText("Example handover for example-epic.")))
        }
    }

    @Test
    fun theGreenProofIsReachedOnceAGreenRunPassesAndTheOthersStayDim() {
        val green = evidenceRun("example-run-2", "green")
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(requirements = listOf(requirement("R1", "Example requirement one.", "check", runs = listOf(green)))),
            )
            setScreen(server)
            awaitLoaded()

            scrollTo("epic-section:green")
            assertEquals("reached", stateOf("proof:green"))
            scrollTo("epic-section:red")
            assertEquals("dim", stateOf("proof:red"))
            scrollTo("epic-section:release")
            assertEquals("dim", stateOf("proof:release"))
            scrollTo("epic-section:report")
            assertEquals("dim", stateOf("proof:report"))
        }
    }

    @Test
    fun theReleaseProofIsReachedOnceTheEpicIsReleased() {
        FakeOrreryServer().use { server ->
            serve(server, body(contract = contract(), outcome = "released", live = false))
            setScreen(server)
            awaitTag("title-state")

            scrollTo("epic-section:release")
            assertEquals("reached", stateOf("proof:release"))
            assertEquals(1, countIn("epic-section:release", hasText("example release step")))
        }
    }

    @Test
    fun theTitleShowsTheStateWithTheTimeItsPhaseHasLasted() {
        FakeOrreryServer().use { server ->
            serve(server, body(buildTasks = listOf(buildTask("example-task-1")), phaseAt = NOW - 2_940))
            setScreen(server)
            awaitTag("title-state")

            composeRule.onNodeWithTag("title-state", useUnmergedTree = true).assertTextEquals("building · 49 min")
        }
    }

    @Test
    fun theTitleShowsTheStateAloneWhenItsPhaseHasNoTime() {
        FakeOrreryServer().use { server ->
            serve(server, body(buildTasks = listOf(buildTask("example-task-1")), phaseAt = 0L))
            setScreen(server)
            awaitTag("title-state")

            composeRule.onNodeWithTag("title-state", useUnmergedTree = true).assertTextEquals("building")
        }
    }

    @Test
    fun theTitleTimeIsZeroWhenThePhaseIsAfterNow() {
        FakeOrreryServer().use { server ->
            serve(server, body(buildTasks = listOf(buildTask("example-task-1")), phaseAt = NOW + 60))
            setScreen(server)
            awaitTag("title-state")

            composeRule.onNodeWithTag("title-state", useUnmergedTree = true).assertTextEquals("building · 0 s")
        }
    }

    @Test
    fun aReleaseStepShowsItsStateEvidenceAndReasonFromTheEpic() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    contract = contract(),
                    releaseProof = listOf(releaseProof(1L, "passed", evidence = "Example evidence.", reason = "Example reason.")),
                ),
            )
            setScreen(server)
            awaitLoaded()

            scrollTo("epic-section:release")
            composeRule.onNodeWithTag("release-step-state:0", useUnmergedTree = true).assertTextEquals("passed")
            composeRule.onNodeWithTag("release-step-evidence:0", useUnmergedTree = true).assertTextEquals("Example evidence.")
            composeRule.onNodeWithTag("release-step-reason:0", useUnmergedTree = true).assertTextEquals("Example reason.")
        }
    }

    @Test
    fun aRunningReleaseStepReachesTheReleaseProof() {
        FakeOrreryServer().use { server ->
            serve(server, body(contract = contract(), releaseProof = listOf(releaseProof(1L, "running"))))
            setScreen(server)
            awaitLoaded()

            scrollTo("epic-section:release")
            assertEquals("reached", stateOf("proof:release"))
        }
    }

    @Test
    fun theReportShowsItsLinesAndIsReachedWhenTheEpicHasOne() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "released",
                    live = false,
                    report = report("released", listOf("R1", "R2"), listOf("R3"), "Example why."),
                ),
            )
            setScreen(server)
            awaitTag("report-outcome")

            scrollTo("epic-section:report")
            composeRule.onNodeWithTag("report-outcome", useUnmergedTree = true).assertTextEquals("released")
            composeRule.onNodeWithTag("report-shipped", useUnmergedTree = true).assertTextEquals("shipped R1, R2")
            composeRule.onNodeWithTag("report-not-shipped", useUnmergedTree = true).assertTextEquals("not shipped R3")
            composeRule.onNodeWithTag("report-why", useUnmergedTree = true).assertTextEquals("Example why.")
            assertEquals("reached", stateOf("proof:report"))
        }
    }

    @Test
    fun theReportIsDimAndHeadingOnlyWithoutOne() {
        FakeOrreryServer().use { server ->
            serve(server, body())
            setScreen(server)
            awaitLoaded()

            scrollTo("epic-section:report")
            assertEquals("dim", stateOf("proof:report"))
            assertEquals(0, countNodes(hasTestTag("report-outcome")))
            assertEquals(0, countNodes(hasTestTag("report-why")))
        }
    }

    @Test
    fun aRunningEpicOffersPauseAndNotResume() {
        FakeOrreryServer().use { server ->
            serve(server, body())
            setScreen(server)
            awaitTag("epic-pause")

            assertEquals(0, countNodes(hasTestTag("epic-resume")))
        }
    }

    @Test
    fun aPausedEpicOffersResumeAndNotPause() {
        FakeOrreryServer().use { server ->
            serve(server, body(paused = true))
            setScreen(server)
            awaitTag("epic-resume")

            assertEquals(0, countNodes(hasTestTag("epic-pause")))
            composeRule.onNodeWithTag("title-state", useUnmergedTree = true).assertTextEquals("paused")
        }
    }

    @Test
    fun anEpicWithAnOutcomeOffersNoCommands() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "released", live = false))
            setScreen(server)
            awaitTag("title-state")

            assertEquals(0, countNodes(hasTestTag("epic-pause")))
            assertEquals(0, countNodes(hasTestTag("epic-resume")))
            assertEquals(0, countNodes(hasTestTag("epic-stop")))
        }
    }

    @Test
    fun aTapOnStopAsksFirstAndConfirmSendsStopOnceWithTheEpicId() {
        FakeOrreryServer().use { server ->
            serve(server, body())
            server.respond(EPICS_STOP, EpicsStopResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("epic-stop")

            clickTag("epic-stop")
            assertEquals(1, countNodes(hasTestTag("epic-stop-sure")))
            assertEquals(0, requestsTo(server, EPICS_STOP).size)

            clickTag("epic-stop-keep")
            assertEquals(0, countNodes(hasTestTag("epic-stop-sure")))
            assertEquals(0, requestsTo(server, EPICS_STOP).size)

            clickTag("epic-stop")
            val reads = getCount(server)
            clickTag("epic-stop-confirm")
            awaitReload(server, reads)

            val stops = requestsTo(server, EPICS_STOP)
            assertEquals(1, stops.size)
            assertEquals(EPIC_ID, EpicsStopRequest.parseFrom(stops.single().body).id)
        }
    }

    @Test
    fun aTapOnTheBlockersStopAsksFirstAndConfirmSendsStopOnceWithTheEpicId() {
        FakeOrreryServer().use { server ->
            serve(server, body(parked = "example park reason"))
            server.respond(EPICS_STOP, EpicsStopResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("blocker-stop")

            clickTag("blocker-stop")
            assertEquals(1, countNodes(hasTestTag("blocker-stop-sure")))
            assertEquals(0, requestsTo(server, EPICS_STOP).size)

            val reads = getCount(server)
            clickTag("blocker-stop-confirm")
            awaitReload(server, reads)

            val stops = requestsTo(server, EPICS_STOP)
            assertEquals(1, stops.size)
            assertEquals(EPIC_ID, EpicsStopRequest.parseFrom(stops.single().body).id)
        }
    }

    @Test
    fun pauseSendsThePauseWithTheEpicId() {
        FakeOrreryServer().use { server ->
            serve(server, body())
            server.respond(EPICS_PAUSE, EpicsPauseResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("epic-pause")

            val reads = getCount(server)
            clickTag("epic-pause")
            awaitReload(server, reads)

            assertEquals(EPIC_ID, EpicsPauseRequest.parseFrom(requestsTo(server, EPICS_PAUSE).single().body).id)
        }
    }

    @Test
    fun resumeSendsTheResumeWithTheEpicId() {
        FakeOrreryServer().use { server ->
            serve(server, body(paused = true))
            server.respond(EPICS_RESUME, EpicsResumeResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("epic-resume")

            val reads = getCount(server)
            clickTag("epic-resume")
            awaitReload(server, reads)

            assertEquals(EPIC_ID, EpicsResumeRequest.parseFrom(requestsTo(server, EPICS_RESUME).single().body).id)
        }
    }

    @Test
    fun retrySendsTheRetryWithTheEpicId() {
        FakeOrreryServer().use { server ->
            serve(server, body(parked = "example park reason"))
            server.respond(EPICS_RETRY, EpicsRetryResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("blocker-retry")

            val reads = getCount(server)
            clickTag("blocker-retry")
            awaitReload(server, reads)

            assertEquals(EPIC_ID, EpicsRetryRequest.parseFrom(requestsTo(server, EPICS_RETRY).single().body).id)
        }
    }

    @Test
    fun answerSendsTheAnswerWithTheEpicIdAndTheQuestion() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(questions = listOf(question("example-question-1", "Example question?", NOW - 120))),
            )
            server.respond(EPICS_ANSWER, EpicsAnswerResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("blocker-answer")

            composeRule.onNodeWithTag("blocker-answer", useUnmergedTree = true).performTextInput("Example answer")
            val reads = getCount(server)
            clickTag("blocker-answer-send")
            awaitReload(server, reads)

            val sent = EpicsAnswerRequest.parseFrom(requestsTo(server, EPICS_ANSWER).single().body)
            assertEquals(EPIC_ID, sent.epicId)
            assertEquals("example-question-1", sent.question)
            assertEquals("Example answer", sent.text)
        }
    }

    @Test
    fun aRefusedAnswerShowsTheReasonAndTheTypedAnswerSoItIsNotLost() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(questions = listOf(question("example-question-1", "Example question?", NOW - 120))),
            )
            server.fail(EPICS_ANSWER, Code.FAILED_PRECONDITION, "question is closed")
            setScreen(server)
            awaitTag("blocker-answer")

            composeRule.onNodeWithTag("blocker-answer", useUnmergedTree = true).performTextInput("Example answer")
            clickTag("blocker-answer-send")

            awaitText("not sent: question is closed. Your answer: Example answer")
        }
    }

    @Test
    fun theActionsRenderFirstInTheTitleRow() {
        FakeOrreryServer().use { server ->
            serve(server, body())
            setScreen(server, actions = { Text("Example action", modifier = Modifier.testTag("example-action")) })
            awaitTag("epic-pause")

            val action = composeRule.onNodeWithTag("example-action", useUnmergedTree = true).fetchSemanticsNode()
            val pause = composeRule.onNodeWithTag("epic-pause", useUnmergedTree = true).fetchSemanticsNode()
            assertTrue(action.boundsInRoot.left < pause.boundsInRoot.left)
        }
    }

    @Test
    fun theTitleShowsHowLongTheBlockerHasWaited() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(questions = listOf(question("example-question-1", "Example question?", NOW - 120))),
            )
            setScreen(server)
            awaitLoaded()

            composeRule.onNodeWithTag("title-blocker", useUnmergedTree = true)
                .assertTextEquals("waits on your answer · 2 min")
        }
    }

    @Test
    fun theSummaryOpensTheContractWhenTapped() {
        FakeOrreryServer().use { server ->
            serve(server, body(summary = "Example summary.", contract = contract()))
            setScreen(server)
            awaitLoaded()

            scrollTo("epic-section:summary")
            assertEquals(1, countIn("epic-section:summary", hasText("Example summary.")))
            clickTag("epic-summary")

            assertEquals(1, contractOpened.get())
        }
    }

    @Test
    fun theSummaryFallsBackToTheContractTitle() {
        FakeOrreryServer().use { server ->
            serve(server, body(contract = contract(title = "Example contract title")))
            setScreen(server)
            awaitLoaded()

            scrollTo("epic-section:summary")
            composeRule.onNodeWithTag("epic-summary", useUnmergedTree = true).assertTextEquals("Example contract title")
        }
    }

    @Test
    fun noSummaryShowsWithoutAContract() {
        FakeOrreryServer().use { server ->
            serve(server, body(summary = "Example summary."))
            setScreen(server)
            awaitLoaded()

            assertEquals(0, countNodes(hasTestTag("epic-section:summary")))
            assertEquals(0, countNodes(hasTestTag("epic-summary")))
            assertEquals(0, countNodes(hasText("Example summary.")))
        }
    }

    @Test
    fun aHistoryStepEntryShowsItsLineAndOpensItsStep() {
        val step = step("example-step-1", "example-task-1", "example-agent")
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(steps = listOf(step)),
                historyOf(historyEntry(NOW - 60, "example-cursor-1", step)),
            )
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countIn("happened:0", hasText("example-task-1 · started")))
            clickTag("happened:0")

            assertEquals("example-step-1", openedStep.get()?.id)
        }
    }

    @Test
    fun justHappenedShowsTheNewestThreeAndOpensTheRestInPlace() {
        val entries = (1..5).map { index ->
            historyEntry(
                NOW - 60,
                "example-cursor-$index",
                step("example-step-$index", "example-task-$index", "example-agent"),
            )
        }
        FakeOrreryServer().use { server ->
            serve(server, body(), historyOf(*entries.toTypedArray()))
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countIn("happened:0", hasText("example-task-5 · started")))
            assertEquals(1, countIn("happened:2", hasText("example-task-3 · started")))
            assertEquals(0, countNodes(hasTestTag("happened:3")))
            clickTag("happened-more")

            assertEquals(1, countIn("happened:4", hasText("example-task-1 · started")))
        }
    }

    @Test
    fun theHistoryIsReadPageByPageUntilNoPageFollows() {
        FakeOrreryServer().use { server ->
            serve(server, body())
            server.route(EPICS_HISTORY) { request ->
                val page = EpicsHistoryRequest.parseFrom(request.body)
                val response = if (page.after.isEmpty()) {
                    EpicsHistoryResponse.newBuilder()
                        .addEntries(historyEntry(NOW - 120, "example-cursor-1", step("example-step-1", "example-task-1", "example-agent")))
                        .setNext("example-cursor-1")
                        .build()
                } else {
                    EpicsHistoryResponse.newBuilder()
                        .addEntries(historyEntry(NOW - 60, "example-cursor-2", step("example-step-2", "example-task-2", "example-agent")))
                        .setNext("")
                        .build()
                }
                Reply.Message(response, 0L)
            }
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countIn("happened:0", hasText("example-task-2 · started")))
            assertEquals(1, countIn("happened:1", hasText("example-task-1 · started")))
            assertEquals(2, requestsTo(server, EPICS_HISTORY).size)
        }
    }

    @Test
    fun theWorkflowNodesShowUnderTheWorkflowHeadingInTheImplementationSection() {
        FakeOrreryServer().use { server ->
            serve(server, body(nodes = listOf(node("example-path", "plan"))))
            setScreen(server)
            awaitLoaded()

            scrollTo("epic-section:implementation")
            assertEquals(1, countNodes(hasText("Workflow")))
            assertEquals(1, countIn("epic-section:implementation", hasTestTag("node-row:0")))
        }
    }

    @Test
    fun aRequirementShowsInTheRedAndGreenProofsWithItsIdTextKindAndConclusion() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(requirements = listOf(requirement("R1", "Example requirement one.", "check", conclusion = "example conclusion"))),
            )
            setScreen(server)
            awaitLoaded()

            scrollTo("epic-section:red")
            assertEquals(1, countIn("proof-card:red:0", hasText("R1")))
            assertEquals(1, countIn("proof-card:red:0", hasText("Example requirement one.")))
            assertEquals(1, countIn("proof-card:red:0", hasText("check")))
            assertEquals(1, countIn("proof-card:red:0", hasText("example conclusion")))
            scrollTo("epic-section:green")
            assertEquals(1, countIn("proof-card:green:0", hasText("R1")))
        }
    }

    @Test
    fun theListKeepsTheScrollTheCallerHoldsWhenACommandReloadsTheEpic() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(server, body(nodes = (1..40).map { node("example-path-$it", "plan") }))
            server.respond(EPICS_PAUSE, EpicsPauseResponse.getDefaultInstance())
            setScreen(server, listState = listState)
            awaitLoaded()

            scrollTo("epic-section:implementation")
            val before = composeRule.runOnIdle { listState.firstVisibleItemIndex }
            assertTrue(before > 0)

            val reads = getCount(server)
            clickTag("epic-pause")
            awaitReload(server, reads)
            composeRule.waitForIdle()

            assertEquals(before, composeRule.runOnIdle { listState.firstVisibleItemIndex })
        }
    }

    @Test
    fun theTitleShowsTheProblemAsSent() {
        FakeOrreryServer().use { server ->
            serve(server, body(problem = "efforts/1/state.json: does not parse"))
            setScreen(server)

            awaitText("efforts/1/state.json: does not parse")
        }
    }

    @Test
    fun theOrreryDrawsInTheImplementationSection() {
        FakeOrreryServer().use { server ->
            serve(server, body())
            setScreen(server)
            awaitLoaded()

            assertEquals(1, countIn("epic-section:implementation", hasTestTag("orrery")))
        }
    }

    @Test
    fun unknownEpicShowsEpicNotFound() {
        FakeOrreryServer().use { server ->
            setScreen(server)

            awaitText("epic not found")
            assertEquals(0, countNodes(hasText("Retry")))
        }
    }

    @Test
    fun failedLoadShowsRetryAndRetryLoadsTheEpic() {
        FakeOrreryServer().use { server ->
            server.fail(EPICS_GET, Code.UNAVAILABLE, "synthetic outage")
            server.respond(EPICS_HISTORY, historyOf())
            setScreen(server)
            awaitText("could not load epic")

            server.respond(EPICS_GET, body(problem = "Loaded after retry"))
            composeRule.onNodeWithText("Retry").performClick()

            awaitText("Loaded after retry")
        }
    }

    @Test
    fun backButtonAndTheOrreryCrumbCallOnBack() {
        FakeOrreryServer().use { server ->
            serve(server, body())
            setScreen(server)
            // Wait for the load to land, so no request is still in flight when the server closes at the end.
            awaitLoaded()

            clickTag("title-back")
            assertEquals(1, backed.get())

            clickTag("crumb:0")
            assertEquals(2, backed.get())
        }
    }
}
