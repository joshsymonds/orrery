package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicBuildTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicHistoryEntry
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestionChoice
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsAnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsAnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsPauseResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsSeenResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicScreenTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val OTHER_EPIC_ID = "example-repo/other-epic"
        const val NOW = 1_800_000_000L
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val EPICS_PAUSE = "/orrery.v1.Epics/Pause"
        const val EPICS_ANSWER = "/orrery.v1.Epics/Answer"
        const val EPICS_SEEN = "/orrery.v1.Epics/Seen"
        // The web's dwell before an ended epic is marked seen (web/src/seen.ts DWELL_MS). A window that must send advances
        // by twice it: advanceTimeBy leaves a task due at its very end, and a dwell can begin at the instant it is at.
        const val DWELL_MILLIS = 1_000L
        const val CHANGED = 4_102_444_800L
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

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
        questions: List<EpicQuestion> = emptyList(),
        contract: EpicContract? = null,
        summary: String = "",
        outcome: String = "",
        live: Boolean = true,
        paused: Boolean = false,
        parked: String = "",
        phaseAt: Long = 0L,
        buildTasks: List<EpicBuildTask> = emptyList(),
        seen: Long = 0L,
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
            .setPhaseAt(phaseAt)
            .setSource("orreryd")
            .setSeen(seen)
        val response = EpicsGetResponse.newBuilder()
            .setEpic(epic)
            .addAllSteps(steps)
            .addAllRequirements(requirements)
            .addAllQuestions(questions)
            .addAllBuildTasks(buildTasks)
            .setSummary(summary)
            .setPaused(paused)
            .setParked(parked)
        contract?.let { response.setContract(it) }
        return response.build()
    }

    private fun question(id: String, text: String, asked: Long, choices: List<EpicQuestionChoice> = emptyList()): EpicQuestion =
        EpicQuestion.newBuilder().setId(id).setText(text).setTurn(1L).setAsked(asked).addAllChoices(choices).build()

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
        owner: LifecycleOwner? = null,
        shown: MutableState<Boolean> = mutableStateOf(true),
        openEpic: MutableState<String> = mutableStateOf(EPIC_ID),
    ) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            CompositionLocalProvider(LocalLifecycleOwner provides (owner ?: LocalLifecycleOwner.current)) {
                if (shown.value) {
                    EpicScreen(
                        client = client,
                        epicId = openEpic.value,
                        onBack = { backed.incrementAndGet() },
                        onOpenStep = {},
                        onOpenContract = {},
                        onTalk = {},
                        listedSteerer = { null },
                        listState = listState,
                        now = { NOW },
                    )
                }
            }
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

    /** Taps [tag] through its click action, as the content of a sheet is tapped under Robolectric. */
    private fun tapInSheet(tag: String) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).performSemanticsAction(SemanticsActions.OnClick)
    }

    private fun requestsTo(server: FakeOrreryServer, path: String) = server.requests.filter { it.rawPath == path }

    private fun getCount(server: FakeOrreryServer): Int = requestsTo(server, EPICS_GET).size

    private fun seenRequests(server: FakeOrreryServer) = requestsTo(server, EPICS_SEEN)

    /** Waits until the epic has been read again, which a command does once it has been sent. */
    private fun awaitReload(server: FakeOrreryServer, afterReads: Int) {
        composeRule.awaitUntil { getCount(server) > afterReads }
    }

    /** Lets the main thread and every request it started finish, so a Seen the screen sent is on the server. */
    private fun settle() {
        Quiescence.awaitUntil("the screen's work settles", step = { composeRule.waitForIdle() }) {
            Quiescence.inFlight() == null
        }
    }

    /** Shows [epic] well past the dwell on a stopped clock, then asserts the screen sent no Seen. */
    private fun assertNoSeenFor(epic: EpicsGetResponse) {
        FakeOrreryServer().use { server ->
            serve(server, epic)
            server.respond(EPICS_SEEN, EpicsSeenResponse.getDefaultInstance())
            setScreen(server)
            awaitLoaded()
            composeRule.mainClock.autoAdvance = false

            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS * 2)
            settle()

            assertEquals(0, seenRequests(server).size)
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
            awaitTag("ask-other")

            clickTag("ask-other")
            composeRule.onNodeWithTag("ask-other-text", useUnmergedTree = true).performTextInput("Example answer")
            val reads = getCount(server)
            clickTag("ask-confirm")
            awaitReload(server, reads)

            val sent = EpicsAnswerRequest.parseFrom(requestsTo(server, EPICS_ANSWER).single().body)
            assertEquals(EPIC_ID, sent.epicId)
            assertEquals("example-question-1", sent.question)
            assertEquals("other", sent.choice)
            assertEquals("Example answer", sent.text)
        }
    }

    @Test
    fun aPickedChoiceSendsItsIdAndItsDoesTextOnlyOnConfirm() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    questions = listOf(
                        question(
                            "example-question-1",
                            "Example question?",
                            NOW - 120,
                            choices = listOf(
                                EpicQuestionChoice.newBuilder().setId("example-choice").setDoes("example does").build(),
                            ),
                        ),
                    ),
                ),
            )
            server.respond(EPICS_ANSWER, EpicsAnswerResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("ask-option:1")

            clickTag("ask-option:1")
            assertEquals(0, requestsTo(server, EPICS_ANSWER).size)
            val reads = getCount(server)
            clickTag("ask-confirm")
            awaitReload(server, reads)

            val sent = EpicsAnswerRequest.parseFrom(requestsTo(server, EPICS_ANSWER).single().body)
            assertEquals("example-question-1", sent.question)
            assertEquals("example-choice", sent.choice)
            assertEquals("example does", sent.text)
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
            awaitTag("ask-other")

            clickTag("ask-other")
            composeRule.onNodeWithTag("ask-other-text", useUnmergedTree = true).performTextInput("Example answer")
            clickTag("ask-confirm")

            awaitText("Answer did not land: question is closed. Your answer: Example answer")
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
    fun unknownEpicShowsEpicNotFound() {
        FakeOrreryServer().use { server ->
            setScreen(server)

            awaitText("epic not found")
            assertEquals(0, countNodes(hasText("Retry")))
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

    @Test
    fun anUnseenEndedEpicShownForTheDwellIsMarkedSeenWithItsIdOnce() {
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "released", live = false, phaseAt = NOW - 120))
            server.respond(EPICS_SEEN, EpicsSeenResponse.getDefaultInstance())
            setScreen(server)
            awaitLoaded()
            composeRule.mainClock.autoAdvance = false

            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS * 2)
            composeRule.awaitUntil("the epic is marked seen") { seenRequests(server).isNotEmpty() }
            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS * 5)
            settle()

            val seen = seenRequests(server)
            assertEquals(1, seen.size)
            assertEquals(EPIC_ID, EpicsSeenRequest.parseFrom(seen.single().body).id)
        }
    }

    @Test
    fun anUnseenEndedEpicLeftBeforeTheDwellIsNotMarkedSeen() {
        val shown = mutableStateOf(true)
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "released", live = false, phaseAt = NOW - 120))
            server.respond(EPICS_SEEN, EpicsSeenResponse.getDefaultInstance())
            setScreen(server, shown = shown)
            awaitLoaded()
            composeRule.mainClock.autoAdvance = false

            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS / 2)
            composeRule.runOnIdle { shown.value = false }
            // Frames move the clock only a few frames here, well short of the dwell, until the screen has left.
            composeRule.mainClock.autoAdvance = true
            composeRule.awaitUntil("the epic has left the composition") { countNodes(hasTestTag("epic-section:now")) == 0 }
            composeRule.mainClock.autoAdvance = false
            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS * 2)
            settle()

            assertEquals(0, seenRequests(server).size)
        }
    }

    @Test
    fun anUnseenEndedEpicIsMarkedSeenOnceAndOnlyAfterAFullDwellWhileResumed() {
        val owner = ManualLifecycleOwner()
        owner.lifecycle.currentState = Lifecycle.State.RESUMED
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "released", live = false, phaseAt = NOW - 120))
            server.respond(EPICS_SEEN, EpicsSeenResponse.getDefaultInstance())
            setScreen(server, owner = owner)
            awaitLoaded()
            composeRule.mainClock.autoAdvance = false

            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS / 2)
            composeRule.runOnIdle { owner.lifecycle.currentState = Lifecycle.State.STARTED }
            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS * 2)
            settle()
            assertEquals(0, seenRequests(server).size)

            composeRule.runOnIdle { owner.lifecycle.currentState = Lifecycle.State.RESUMED }
            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS * 2)
            composeRule.awaitUntil("the epic is marked seen") { seenRequests(server).isNotEmpty() }
            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS * 5)
            settle()
            assertEquals(1, seenRequests(server).size)

            // Once marked, the ending stays marked: a pause and a resume after the mark send nothing more.
            composeRule.runOnIdle { owner.lifecycle.currentState = Lifecycle.State.STARTED }
            composeRule.runOnIdle { owner.lifecycle.currentState = Lifecycle.State.RESUMED }
            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS * 2)
            settle()
            assertEquals(1, seenRequests(server).size)
        }
    }

    @Test
    fun aSwitchToAnotherEpicBeforeTheDwellMarksOnlyTheEpicThatStaysOpen() {
        val openEpic = mutableStateOf(EPIC_ID)
        FakeOrreryServer().use { server ->
            serve(server, body(outcome = "released", live = false, phaseAt = NOW - 120))
            server.respond(EPICS_SEEN, EpicsSeenResponse.getDefaultInstance())
            setScreen(server, openEpic = openEpic)
            awaitLoaded()
            composeRule.mainClock.autoAdvance = false

            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS / 2)
            val reads = getCount(server)
            composeRule.runOnIdle { openEpic.value = OTHER_EPIC_ID }
            // The screen reads the other epic and draws it again. Its dwell starts only once it is drawn, and the first
            // epic's dwell ended with the switch.
            composeRule.mainClock.autoAdvance = true
            composeRule.awaitUntil("the other epic is drawn") {
                getCount(server) > reads && countNodes(hasTestTag("epic-section:now")) > 0
            }
            composeRule.mainClock.autoAdvance = false
            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS * 2)
            composeRule.awaitUntil("the epic that stays open is marked seen") { seenRequests(server).isNotEmpty() }
            settle()

            val seen = seenRequests(server)
            assertEquals(1, seen.size)
            assertEquals(OTHER_EPIC_ID, EpicsSeenRequest.parseFrom(seen.single().body).id)
        }
    }

    @Test
    fun aSeenEndedEpicSendsNoSeen() {
        assertNoSeenFor(body(outcome = "released", live = false, phaseAt = NOW - 120, seen = NOW - 60))
    }

    @Test
    fun aLiveEpicSendsNoSeen() {
        assertNoSeenFor(body(live = true, phaseAt = NOW - 120))
    }

    @Test
    fun aLiveEpicWhoseNextLoadAnswersReleasedStaysOnScreenAndIsMarkedSeen() {
        val reads = AtomicInteger()
        FakeOrreryServer().use { server ->
            server.route(EPICS_GET) {
                val live = reads.incrementAndGet() == 1
                Reply.Message(body(live = live, outcome = if (live) "built" else "released", phaseAt = NOW - 60), 0L)
            }
            server.respond(EPICS_HISTORY, historyOf())
            server.respond(EPICS_PAUSE, EpicsPauseResponse.getDefaultInstance())
            server.respond(EPICS_SEEN, EpicsSeenResponse.getDefaultInstance())
            setScreen(server)
            awaitTag("epic-more")

            val reloads = getCount(server)
            clickTag("epic-more")
            awaitTag("epic-sheet-pause")
            tapInSheet("epic-sheet-pause")
            awaitReload(server, reloads)
            composeRule.awaitUntil("the epic reads as not live") { countNodes(hasTestTag("epic-more")) == 0 }

            assertEquals(0, countNodes(hasText("epic not found")))
            assertEquals(1, countNodes(hasTestTag("epic-section:now")))

            composeRule.mainClock.autoAdvance = false
            composeRule.mainClock.advanceTimeBy(DWELL_MILLIS * 2)
            composeRule.awaitUntil("the epic is marked seen") { seenRequests(server).isNotEmpty() }
            settle()

            val seen = seenRequests(server)
            assertEquals(1, seen.size)
            assertEquals(EPIC_ID, EpicsSeenRequest.parseFrom(seen.single().body).id)
        }
    }
}

/** An owner whose lifecycle the test moves, as the activity's is moved when the app pauses or resumes. */
private class ManualLifecycleOwner : LifecycleOwner {
    private val registry = LifecycleRegistry(this)
    override val lifecycle: LifecycleRegistry get() = registry
}
