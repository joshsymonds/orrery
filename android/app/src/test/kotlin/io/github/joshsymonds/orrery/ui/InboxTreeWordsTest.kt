package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicReleaseProofRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EpicTaskRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

// The words an inbox row says, over synthetic epics, contracts and panes at a synthetic clock.
class InboxTreeWordsTest {
    private val now = 100_000L

    private fun epic(
        outcome: String = "accepted",
        live: Boolean = true,
        phaseAt: Long = now - 49 * 60,
        paused: Boolean = false,
        pauseSource: String = "",
        parked: String = "",
    ) = EpicRow(
        id = "lighthouse/rota-export",
        repository = "/home/user/lighthouse",
        slug = "rota-export",
        outcome = outcome,
        changed = 0L,
        live = live,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        phaseAt = phaseAt,
        paused = paused,
        pauseSource = pauseSource,
        parked = parked,
    )

    private fun task(status: String) = EpicTaskRow(effort = 1, slug = "task", subject = "", status = status)

    private fun question(asked: Long) = EpicQuestionRow(id = "Q1", text = "Which branch?", turn = 1L, asked = asked)

    private fun failedRelease() = EpicReleaseProofRow(
        position = 3L,
        step = "tag",
        target = "",
        undo = "",
        state = "failed",
        evidence = "",
        reason = "tag exists",
        attempt = 1L,
        at = 0L,
    )

    private fun heldStep(started: Long) = StepRow(
        id = "s1",
        task = "build",
        agent = "",
        end = "",
        status = "",
        held = true,
        started = started,
        ended = 0L,
        doing = "",
        checks = emptyList(),
        reason = "",
    )

    private fun detail(
        epic: EpicRow = epic(),
        tasks: List<EpicTaskRow> = emptyList(),
        questions: List<EpicQuestionRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
        releaseProof: List<EpicReleaseProofRow> = emptyList(),
        paused: Boolean = false,
        parked: String = "",
    ): EpicDetail = EpicDetail(
        epic = epic,
        tasks = tasks,
        steps = steps,
        questions = questions,
        paused = paused,
        parked = parked,
        releaseProof = releaseProof,
    )

    private fun node(
        epic: EpicRow = epic(),
        detail: EpicDetail? = detail(epic),
        contract: ContractRow? = null,
    ) = EpicNode(epic, detail, contract)

    private fun contract(
        state: String = "proposed",
        version: Long = 3L,
        changed: Long = now - 180,
    ) = ContractRow(
        id = "lighthouse/tide-alerts",
        repository = "/home/user/lighthouse",
        title = "tide alerts",
        care = "",
        state = state,
        version = version,
        openDecisions = 0L,
        conversation = "conv-1",
        created = 0L,
        changed = changed,
    )

    private fun pane(
        state: String,
        summary: String = "",
        question: String = "",
        tool: String = "",
        changed: Long = now - 300,
    ) = Pane(
        id = "p1",
        host = "laptop",
        session = "work",
        window = 0,
        pane = 0,
        cwd = "/home/user/lighthouse",
        command = "",
        state = state,
        summary = summary,
        label = "",
        changed = changed,
        seen = 0L,
        question = question,
        questionHash = "",
        tool = tool,
        orreryId = "conv-1",
    )

    private fun convo(pane: Pane? = null, standIn: StandIn = StandIn.NONE) =
        Convo(key = "k", pane = pane, standIn = standIn, again = false, hung = emptyList())

    private fun epicWords(node: EpicNode): EpicRowWords = epicRowWords(node, now)

    @Test
    fun aPlanningEpicReadsPlanningWithItsPhaseTimeAndALiveRing() {
        val words = epicWords(node(detail = detail()))

        assertEquals("rota-export", words.name)
        assertEquals("0 of 0 tasks done", words.count)
        assertEquals("planning · 49 min", words.words)
        assertEquals("", words.blocker)
        assertEquals(GaugeShare(done = 0, total = 0, tone = GaugeTone.LIVE, inner = null), words.gauge)
    }

    @Test
    fun aBuildingEpicCountsItsDoneAndIntegratedTasksAsDone() {
        val tasks = listOf(task("done"), task("integrated"), task("pending"))
        val words = epicWords(node(detail = detail(tasks = tasks)))

        assertEquals("2 of 3 tasks done", words.count)
        assertEquals("building · 49 min", words.words)
        assertEquals(GaugeShare(done = 2, total = 3, tone = GaugeTone.LIVE, inner = null), words.gauge)
    }

    @Test
    fun aReleasingEpicReadsReleasingInTheEngineTone() {
        val words = epicWords(node(epic = epic(outcome = "clean"), detail = detail(epic = epic(outcome = "clean"))))

        assertEquals("releasing · 49 min", words.words)
        assertEquals(GaugeTone.ENGINE, words.gauge?.tone)
    }

    @Test
    fun aShippedEpicReadsShippedWithItsAgeAndACheck() {
        val ended = epic(outcome = "released", live = false)
        val words = epicWords(node(epic = ended, detail = detail(epic = ended)))

        assertEquals("shipped · 49 min ago", words.words)
        assertEquals("", words.blocker)
        assertEquals(GaugeShare(done = 0, total = 0, tone = GaugeTone.OK, inner = GaugeInner.CHECK), words.gauge)
    }

    @Test
    fun anEndedShortEpicReadsEndedShortWithADash() {
        val ended = epic(outcome = "abandoned", live = false)
        val words = epicWords(node(epic = ended, detail = detail(epic = ended)))

        assertEquals("ended short · 49 min ago", words.words)
        assertEquals(GaugeShare(done = 0, total = 0, tone = GaugeTone.NONE, inner = GaugeInner.DASH), words.gauge)
    }

    @Test
    fun anEndedEpicWaitsOnNothingEvenWithAnOpenQuestion() {
        val ended = epic(outcome = "released", live = false)
        val words = epicWords(node(epic = ended, detail = detail(epic = ended, questions = listOf(question(now - 120)))))

        assertEquals("", words.blocker)
    }

    @Test
    fun aParkedEpicReadsParkedAndItsReasonIsTheBlocker() {
        val parked = epic(parked = "waits for the owner")
        val words = epicWords(node(epic = parked, detail = detail(epic = parked, parked = "waits for the owner")))

        assertEquals("parked · 49 min", words.words)
        assertEquals("waits for the owner", words.blocker)
        assertEquals(GaugeShare(done = 0, total = 0, tone = GaugeTone.ATTENTION, inner = GaugeInner.DOT), words.gauge)
    }

    @Test
    fun aPausedEpicReadsPausedByYouAndTakesNoGaugeTone() {
        val paused = epic(paused = true, pauseSource = "owner")
        val words = epicWords(node(epic = paused, detail = detail(epic = paused, paused = true)))

        assertEquals("paused by you · 49 min", words.words)
        assertEquals(GaugeShare(done = 0, total = 0, tone = GaugeTone.NONE, inner = null), words.gauge)
    }

    @Test
    fun aPausedOnAUsageLimitEpicReadsSoInItsState() {
        val paused = epic(paused = true, pauseSource = "limit")
        val words = epicWords(node(epic = paused, detail = detail(epic = paused, paused = true)))

        assertEquals("paused on a usage limit · 49 min", words.words)
    }

    @Test
    fun anOpenQuestionIsTheBlockerWithItsWaitAndAttention() {
        val words = epicWords(node(detail = detail(questions = listOf(question(now - 120)))))

        assertEquals("waits on your answer · 2 min", words.blocker)
        assertEquals(GaugeShare(done = 0, total = 0, tone = GaugeTone.ATTENTION, inner = GaugeInner.DOT), words.gauge)
    }

    @Test
    fun aFailedReleaseStepIsTheBlockerWithItsReasonAndNoSlug() {
        val words = epicWords(node(detail = detail(releaseProof = listOf(failedRelease()))))

        assertEquals("Release step 3 failed: tag exists", words.blocker)
        assertEquals(GaugeTone.ATTENTION, words.gauge?.tone)
    }

    @Test
    fun aHeldStepIsTheBlockerNamedByItsHold() {
        val words = epicWords(node(detail = detail(steps = listOf(heldStep(started = now - 60)))))

        assertEquals("build held", words.blocker)
        assertEquals(GaugeTone.ATTENTION, words.gauge?.tone)
    }

    @Test
    fun aProposedContractReadsItsNameVersionAgeAndWaitsToBeAccepted() {
        val words = epicRowWords(ContractNode(contract()), now)

        assertEquals("tide-alerts", words.name)
        assertEquals("v3", words.count)
        assertEquals("contract · proposed · 3 min ago", words.words)
        assertEquals("waits to be read and accepted", words.blocker)
        assertNull(words.gauge)
    }

    @Test
    fun anEpicCarryingAProposedContractReadsAsTheContract() {
        val words = epicWords(node(contract = contract()))

        assertEquals("tide-alerts", words.name)
        assertEquals("contract · proposed · 3 min ago", words.words)
        assertNull(words.gauge)
    }

    @Test
    fun anEpicWhoseContractIsNotProposedReadsAsTheEpic() {
        val words = epicWords(node(contract = contract(state = "accepted")))

        assertEquals("rota-export", words.name)
        assertEquals("planning · 49 min", words.words)
    }

    @Test
    fun aWaitingPaneLeadsItsAskedQuestionWithAQuestionMark() {
        val asked = """{"questions":[{"question":"Which branch?","options":[{"label":"main"},{"label":"dev"}]}]}"""
        val line = convoLineOf(convo(pane("waiting", question = asked)), now)

        assertEquals("? Which branch?", line)
    }

    @Test
    fun aWaitingPermissionReadsItsToolAndArgumentAsItsQuestion() {
        val permission = """{"tool":"Bash","input":{"command":"rm -rf build"}}"""
        val line = convoLineOf(convo(pane("waiting", question = permission)), now)

        assertEquals("? Bash: rm -rf build", line)
    }

    @Test
    fun aWaitingPaneWithNoQuestionWaitsOnYou() {
        assertEquals("waits on you", convoLineOf(convo(pane("waiting")), now))
    }

    @Test
    fun aWaitingHeadlessSessionWithNoQuestionSaysItsLastWordsAndAPaneNeverRepeatsItsTitle() {
        val session = pane("waiting", summary = "Its turn ended on a question in words").copy(headless = true)
        assertEquals("Its turn ended on a question in words", convoLineOf(convo(session), now))
        assertEquals("waits on you", convoLineOf(convo(pane("waiting", summary = "build green")), now))
    }

    @Test
    fun aWorkingPaneReadsItsToolItsArgumentAndItsClock() {
        val tool = """{"id":"t1","name":"Bash","input":"go test ./...","start":${now - 46}}"""
        val line = convoLineOf(convo(pane("working", tool = tool)), now)

        assertEquals("Bash go test ./... · 0:46", line)
    }

    @Test
    fun aWorkingPaneDrawsNoClockWhenTheToolHasNoStart() {
        val tool = """{"id":"t1","name":"Bash","input":"go test ./...","start":0}"""
        val line = convoLineOf(convo(pane("working", tool = tool, changed = now - 300)), now)

        assertEquals("Bash go test ./...", line)
    }

    @Test
    fun anMcpToolReadsItsShortName() {
        val tool = """{"id":"t1","name":"mcp__orrery__step_start","input":"","start":${now - 46}}"""
        val line = convoLineOf(convo(pane("working", tool = tool)), now)

        assertEquals("step_start · 0:46", line)
    }

    @Test
    fun aWorkingSessionWithNoToolReadsItsLastWordsAndAPaneNothing() {
        val session = pane("working", summary = "Reading the parser.").copy(headless = true)

        assertEquals("Reading the parser.", convoLineOf(convo(session), now))
        assertEquals("", convoLineOf(convo(pane("working", summary = "Reading the parser.")), now))
    }

    @Test
    fun aSessionThatIsNotWaitingOrWorkingReadsItsLastWordsAndAPaneNeverRepeatsItsTitle() {
        val session = pane("done", summary = "Wrote the tests.").copy(headless = true)

        assertEquals("Wrote the tests.", convoLineOf(convo(session), now))
        assertEquals("", convoLineOf(convo(pane("done", summary = "Wrote the tests.")), now))
    }

    @Test
    fun aPaneWithNoStateIsATerminalWithNoConversation() {
        assertEquals("a terminal with no conversation", convoLineOf(convo(pane("")), now))
    }

    @Test
    fun aStandInWhoseConversationIsNotListedSaysSo() {
        assertEquals("its conversation is not listed", convoLineOf(convo(standIn = StandIn.UNLISTED), now))
    }

    @Test
    fun aStandInWhoseConversationIsArchivedSaysSo() {
        assertEquals("its conversation is archived", convoLineOf(convo(standIn = StandIn.ARCHIVED), now))
    }
}
