package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicReleaseProofRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicRow
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
        phaseAt = phaseAt,
        paused = paused,
        pauseSource = pauseSource,
        parked = parked,
    )

    private fun task(status: String) = EpicBuildTaskRow(
        id = "task",
        title = "",
        status = status,
        stage = "",
        red = null,
        green = null,
    )

    private fun question(asked: Long) = EpicQuestionRow(id = "Q1", text = "Which branch?", turn = 1L, asked = asked)

    private fun requirement(id: String, state: String) =
        EpicRequirementRow(id = id, text = "", evidence = "", kind = "check", state = state)

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
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        questions: List<EpicQuestionRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
        releaseProof: List<EpicReleaseProofRow> = emptyList(),
        paused: Boolean = false,
        parked: String = "",
        requirements: List<EpicRequirementRow> = emptyList(),
        centre: String = "",
        steererParked: Boolean = false,
    ): EpicDetail = EpicDetail(
        epic = epic,
        buildTasks = buildTasks,
        steps = steps,
        questions = questions,
        paused = paused,
        parked = parked,
        steererParked = steererParked,
        releaseProof = releaseProof,
        requirements = requirements,
        centre = centre,
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

    private fun convo(pane: Pane? = null) = Convo(key = "k", pane = pane, hung = emptyList(), folded = emptyList())

    private fun epicWords(node: EpicNode): EpicRowWords = epicRowWords(node, now)

    @Test
    fun aPlanningEpicReadsPlanningWithItsPhaseTimeAndADottedRingInItsRunningCentre() {
        val words = epicWords(node(detail = detail(centre = "running")))

        assertEquals("rota-export", words.name)
        assertEquals("0 of 0 tasks done", words.count)
        assertEquals("planning · 49 min", words.words)
        assertEquals("", words.blocker)
        assertEquals(
            EpicMarkModel(
                rim = emptyList(),
                centre = CentreMark("running", CentreTone.LIVE, CentreGlyph.COUNT),
                label = "no requirements yet, running",
            ),
            words.mark,
        )
    }

    @Test
    fun aBuildingEpicCountsItsDoneAndIntegratedTasksAsDoneAndItsMarkCountsNoTasks() {
        val tasks = listOf(task("done"), task("integrated"), task("pending"))
        val words = epicWords(node(detail = detail(buildTasks = tasks, centre = "running")))

        assertEquals("2 of 3 tasks done", words.count)
        assertEquals("building · 49 min", words.words)
        assertEquals(
            EpicMarkModel(
                rim = emptyList(),
                centre = CentreMark("running", CentreTone.LIVE, CentreGlyph.COUNT),
                label = "no requirements yet, running",
            ),
            words.mark,
        )
    }

    @Test
    fun anEpicsMarkDrawsItsRequirementsRimAndCountsItsProvenOnes() {
        val requirements = listOf(
            requirement("R1", "proven"),
            requirement("R2", "in_flight"),
            requirement("R3", "not_reached"),
        )
        val words = epicWords(node(detail = detail(requirements = requirements, centre = "running")))

        assertEquals(
            EpicMarkModel(
                rim = listOf(
                    MarkSegment("R1", "proven", RimTone.OK),
                    MarkSegment("R2", "in_flight", RimTone.RUN),
                    MarkSegment("R3", "not_reached", RimTone.FIRM),
                ),
                centre = CentreMark("running", CentreTone.LIVE, CentreGlyph.COUNT, proven = 1),
                label = "1 of 3 requirements proven, running",
            ),
            words.mark,
        )
    }

    @Test
    fun aReleasingEpicReadsReleasingInTheEngineTone() {
        val clean = epic(outcome = "clean")
        val words = epicWords(node(epic = clean, detail = detail(epic = clean, centre = "releasing")))

        assertEquals("releasing · 49 min", words.words)
        assertEquals(CentreTone.ENGINE, words.mark?.centre?.tone)
        assertEquals(CentreGlyph.ARROW, words.mark?.centre?.glyph)
    }

    @Test
    fun aShippedEpicReadsShippedWithItsAgeAndACheck() {
        val ended = epic(outcome = "released", live = false)
        val words = epicWords(node(epic = ended, detail = detail(epic = ended, centre = "shipped")))

        assertEquals("shipped · 49 min ago", words.words)
        assertEquals("", words.blocker)
        assertEquals(CentreMark("shipped", CentreTone.OK, CentreGlyph.CHECK), words.mark?.centre)
    }

    @Test
    fun anEndedShortEpicReadsEndedShortWithADash() {
        val ended = epic(outcome = "abandoned", live = false)
        val words = epicWords(node(epic = ended, detail = detail(epic = ended, centre = "ended_short")))

        assertEquals("ended short · 49 min ago", words.words)
        assertEquals(CentreMark("ended short", CentreTone.QUIET, CentreGlyph.DASH), words.mark?.centre)
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
        val words = epicWords(
            node(epic = parked, detail = detail(epic = parked, parked = "waits for the owner", centre = "asks")),
        )

        assertEquals("parked · 49 min", words.words)
        assertEquals("waits for the owner", words.blocker)
        assertEquals(CentreMark("asks you a question", CentreTone.ATTENTION, CentreGlyph.QUESTION), words.mark?.centre)
    }

    @Test
    fun aPausedEpicReadsPausedByYouAndDrawsThePauseCentre() {
        val paused = epic(paused = true, pauseSource = "owner")
        val words = epicWords(node(epic = paused, detail = detail(epic = paused, paused = true, centre = "paused")))

        assertEquals("paused by you · 49 min", words.words)
        assertEquals(CentreMark("paused", CentreTone.QUIET, CentreGlyph.PAUSE), words.mark?.centre)
    }

    @Test
    fun aPausedOnAUsageLimitEpicReadsSoInItsState() {
        val paused = epic(paused = true, pauseSource = "limit")
        val words = epicWords(node(epic = paused, detail = detail(epic = paused, paused = true)))

        assertEquals("paused on a usage limit · 49 min", words.words)
    }

    @Test
    fun anOpenQuestionIsTheBlockerWithItsWaitAndAnAskCentre() {
        val words = epicWords(node(detail = detail(questions = listOf(question(now - 120)), centre = "asks")))

        assertEquals("waits on your answer · 2 min", words.blocker)
        assertEquals(CentreMark("asks you a question", CentreTone.ATTENTION, CentreGlyph.QUESTION), words.mark?.centre)
    }

    @Test
    fun aQuestionItsSteererHoldsIsNoBlockerUntilItsSteererEscalatesIt() {
        val held = question(now - 120).copy(steererHeld = true)
        // The server's centre for a question its steerer holds is not asks: it is the steerer's move until escalated.
        val words = epicWords(node(detail = detail(questions = listOf(held), centre = "running")))

        assertEquals("", words.blocker)
        assertEquals(CentreTone.LIVE, words.mark?.centre?.tone)

        val escalated = question(now - 120).copy(escalated = now - 60, escalationBy = "conversation")
        val asked = epicWords(node(detail = detail(questions = listOf(escalated), centre = "asks")))

        assertEquals("waits on your answer · 2 min", asked.blocker)
        assertEquals(CentreTone.ATTENTION, asked.mark?.centre?.tone)
    }

    @Test
    fun aParkItsSteererHoldsReadsParkedWithNoBlockerAndAHeldStepBesideItIsTheBlocker() {
        val parked = epic(parked = "waits for the steerer")
        val words = epicWords(
            node(
                epic = parked,
                // The server's centre for a park its steerer holds is not asks.
                detail = detail(
                    epic = parked,
                    parked = "waits for the steerer",
                    steererParked = true,
                    centre = "running",
                ),
            ),
        )

        assertEquals("parked · 49 min", words.words)
        assertEquals("", words.blocker)
        assertEquals(CentreTone.LIVE, words.mark?.centre?.tone)

        val stepped = epicWords(
            node(
                epic = parked,
                detail = detail(
                    epic = parked,
                    parked = "waits for the steerer",
                    steererParked = true,
                    steps = listOf(heldStep(started = now - 60)),
                    centre = "asks",
                ),
            ),
        )

        assertEquals("build held", stepped.blocker)
    }

    @Test
    fun aFailedReleaseStepIsTheBlockerWithItsReasonAndNoSlug() {
        val words = epicWords(node(detail = detail(releaseProof = listOf(failedRelease()), centre = "asks")))

        assertEquals("Release step 3 failed: tag exists", words.blocker)
        assertEquals(CentreTone.ATTENTION, words.mark?.centre?.tone)
    }

    @Test
    fun aHeldStepIsTheBlockerNamedByItsHold() {
        val words = epicWords(node(detail = detail(steps = listOf(heldStep(started = now - 60)), centre = "asks")))

        assertEquals("build held", words.blocker)
        assertEquals(CentreTone.ATTENTION, words.mark?.centre?.tone)
    }

    @Test
    fun aProposedContractReadsItsNameVersionAgeAndWaitsToBeAccepted() {
        val words = epicRowWords(ContractNode(contract()), now)

        assertEquals("tide-alerts", words.name)
        assertEquals("v3", words.count)
        assertEquals("contract · proposed · 3 min ago", words.words)
        assertEquals("waits to be read and accepted", words.blocker)
        assertNull(words.mark)
    }

    @Test
    fun anEpicCarryingAProposedContractReadsAsTheContract() {
        val words = epicWords(node(contract = contract()))

        assertEquals("tide-alerts", words.name)
        assertEquals("contract · proposed · 3 min ago", words.words)
        assertNull(words.mark)
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
    fun aRowWithNoPaneHasNoConversationLineOfItsOwn() {
        assertEquals("", convoLineOf(convo(), now))
    }
}
