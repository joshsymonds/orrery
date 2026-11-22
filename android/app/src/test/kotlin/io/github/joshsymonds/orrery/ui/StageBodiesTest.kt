package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.StepVerdictRow
import io.github.joshsymonds.orrery.api.TaskFindingRow
import io.github.joshsymonds.orrery.api.TaskReviewRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

private const val TASK = "T1"

// What each opened stage of a task shows, as a model over synthetic epic details.
class StageBodiesTest {
    @Test
    fun aRedRunIsShownWhenThereIsOne() {
        val red = run("red-1", "red", passed = false)
        val bodies = bodiesOf(detail(buildTasks = listOf(buildTask(red = red))))

        assertEquals(red, bodies.red.run)
    }

    @Test
    fun aMissingRedRunSaysNoRedRunYet() {
        val bodies = bodiesOf(detail(buildTasks = listOf(buildTask())))

        assertNull(bodies.red.run)
        assertEquals(ToneLine("No red run yet.", BodyTone.QUIET), bodies.red.none)
    }

    @Test
    fun attemptChipsAreRunningHeldDoneAndSpent() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step("w1", end = "", doing = "synthetic doing"),
                step("w2", end = "", held = true, doing = "synthetic doing"),
                step("w3", end = "done", ended = 200, summary = "synthetic summary"),
                step("w4", end = "checks_failed", ended = 300, reason = "synthetic reason"),
            ),
        )

        assertEquals(
            listOf(
                AttemptRow(1, "synthetic doing", ToneLine("running", BodyTone.LIVE)),
                AttemptRow(2, "synthetic doing", ToneLine("held", BodyTone.ATTENTION)),
                AttemptRow(3, "synthetic summary", ToneLine("done", BodyTone.OK)),
                AttemptRow(4, "synthetic reason", ToneLine("spent", BodyTone.BAD)),
            ),
            bodiesOf(epic).implement.attempts,
        )
    }

    @Test
    fun anAttemptsWordsAreItsSummaryWhenEndedThenItsReasonThenItsDoing() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step("w1", end = "done", ended = 200, summary = "synthetic summary", reason = "synthetic reason"),
                step("w2", end = "checks_failed", ended = 300, summary = "", reason = "synthetic reason"),
                step("w3", end = "", doing = "synthetic doing", summary = "synthetic summary"),
            ),
        )

        assertEquals(
            listOf("synthetic summary", "synthetic reason", "synthetic doing"),
            bodiesOf(epic).implement.attempts.map { it.words },
        )
    }

    @Test
    fun attemptsAreInStartedOrder() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step("late", started = 300, end = "done", ended = 310, summary = "synthetic late"),
                step("early", started = 100, end = "done", ended = 110, summary = "synthetic early"),
            ),
        )

        assertEquals(
            listOf(1 to "synthetic early", 2 to "synthetic late"),
            bodiesOf(epic).implement.attempts.map { it.number to it.words },
        )
    }

    @Test
    fun aReadOnlyStepIsNoAttempt() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(step("r1", readOnly = true, end = "done", ended = 200, summary = "synthetic review")),
        )

        assertEquals(emptyList<AttemptRow>(), bodiesOf(epic).implement.attempts)
        assertNull(bodiesOf(epic).implement.latest)
    }

    @Test
    fun aLatestAttemptsRoundsShowTheirChecksAndTheirFailedChecksAsText() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step("w1", end = "checks_failed", ended = 130, checks = listOf(check(1, "gate", false, "synthetic old")), gateName = "old gate"),
                step(
                    "w2",
                    end = "done",
                    ended = 200,
                    gateName = "gradle test",
                    checks = listOf(
                        check(1, "fmt", false, "synthetic fmt detail"),
                        check(1, "gate", false, ""),
                        check(2, "fmt", true),
                        check(2, "gate", true, "synthetic gate detail"),
                    ),
                    verdicts = listOf(StepVerdictRow(round = 2, verdict = "met", reason = "")),
                ),
            ),
        )

        val latest = bodiesOf(epic).implement.latest

        assertEquals("Attempt 2 · check rounds", latest?.label)
        assertEquals(
            listOf(
                RoundBlock(
                    1,
                    listOf(RoundCheck("fmt", false), RoundCheck("gate", false)),
                    listOf("fmt: synthetic fmt detail", "gate failed"),
                ),
                RoundBlock(2, listOf(RoundCheck("fmt", true), RoundCheck("gate", true)), listOf("All checks pass.")),
            ),
            latest?.rounds,
        )
    }

    @Test
    fun aRoundWithNoFailedCheckSaysAllChecksPass() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(step("w1", end = "done", ended = 200, checks = listOf(check(1, "fmt", true)))),
        )

        assertEquals(
            listOf(RoundBlock(1, listOf(RoundCheck("fmt", true)), listOf("All checks pass."))),
            bodiesOf(epic).implement.latest?.rounds,
        )
    }

    @Test
    fun theGateOfTheLastRoundShowsItsNameAndItsNonEmptyTailsStdoutThenStderr() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step(
                    "w1",
                    end = "checks_failed",
                    ended = 130,
                    gateName = "gradle test",
                    checks = listOf(
                        check(1, "gate", false, "synthetic detail", tailStdout = "synthetic stdout", tailStderr = "synthetic stderr"),
                    ),
                ),
            ),
        )

        assertEquals(GateLog("Gate · gradle test", listOf("synthetic stdout", "synthetic stderr")), bodiesOf(epic).implement.latest?.gate)
    }

    @Test
    fun anEmptyTailIsNotShown() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step(
                    "w1",
                    end = "checks_failed",
                    ended = 130,
                    gateName = "gradle test",
                    checks = listOf(check(1, "gate", false, "synthetic detail", tailStderr = "synthetic stderr")),
                ),
            ),
        )

        assertEquals(GateLog("Gate · gradle test", listOf("synthetic stderr")), bodiesOf(epic).implement.latest?.gate)
    }

    @Test
    fun aLastRoundWithoutAGateCheckHasNoGate() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step("w1", end = "checks_failed", ended = 130, gateName = "gradle test", checks = listOf(check(1, "gate", false, "x"))),
                step("w2", end = "done", ended = 200, gateName = "gradle test", checks = listOf(check(1, "fmt", true))),
            ),
        )

        assertNull(bodiesOf(epic).implement.latest?.gate)
    }

    @Test
    fun aMetVerdictJudgesTheGoalMet() {
        assertEquals(
            ToneLine("Judged: the goal is met.", BodyTone.OK),
            judgeOf(verdict = "met", reason = "synthetic reason"),
        )
    }

    @Test
    fun anotherVerdictJudgesItWithItsReason() {
        assertEquals(
            ToneLine("Judged: unmet. synthetic reason", BodyTone.BAD),
            judgeOf(verdict = "unmet", reason = "synthetic reason"),
        )
    }

    @Test
    fun anotherVerdictWithNoReasonEndsAtItsWord() {
        assertEquals(ToneLine("Judged: unmet.", BodyTone.BAD), judgeOf(verdict = "unmet", reason = ""))
    }

    @Test
    fun anAttemptWithNoVerdictWaitsForItsGate() {
        assertEquals(
            ToneLine("The judge reads the change once the gate passes.", BodyTone.QUIET),
            judgeOf(verdict = "", reason = ""),
        )
    }

    @Test
    fun theLatestAttemptHasItsRoundsAndNoEarlierOneDoes() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step("w1", end = "checks_failed", ended = 130, checks = listOf(check(1, "fmt", false, "x"))),
                step("w2", end = "", started = 200),
            ),
        )

        assertEquals(emptyList<RoundBlock>(), bodiesOf(epic).implement.latest?.rounds)
    }

    @Test
    fun theSessionDoorOpensTheLatestWritingStep() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step("w1", end = "checks_failed", ended = 130),
                step("w2", end = "done", ended = 200),
                step("r1", readOnly = true, end = "done", ended = 300),
            ),
        )

        assertEquals("w2", bodiesOf(epic).implement.session)
    }

    @Test
    fun theSessionDoorIsAbsentWithNoWritingStep() {
        assertNull(bodiesOf(detail(buildTasks = listOf(buildTask()))).implement.session)
    }

    @Test
    fun aGreenReproductionLineSaysItHasNotRunYet() {
        assertEquals(
            ToneLine("The red test has not run on the change yet.", BodyTone.QUIET),
            greenOf(green = null).reproduction,
        )
    }

    @Test
    fun aGreenReproductionLineSaysItIsRunning() {
        assertEquals(
            ToneLine("The red test is running on the change.", BodyTone.QUIET),
            greenOf(green = run("green-1", "green", passed = false).copy(state = "capturing")).reproduction,
        )
    }

    @Test
    fun aPassingGreenReproductionLineSaysItNowPasses() {
        assertEquals(
            ToneLine("The red test, run on the change, now passes.", BodyTone.OK),
            greenOf(green = run("green-1", "green", passed = true)).reproduction,
        )
    }

    @Test
    fun aFailingGreenReproductionLineSaysItStillFails() {
        assertEquals(
            ToneLine("The red test, run on the change, still fails.", BodyTone.BAD),
            greenOf(green = run("green-1", "green", passed = false)).reproduction,
        )
    }

    @Test
    fun aMissingGreenRunSaysNoGreenRunYet() {
        val green = greenOf(green = null)

        assertNull(green.run.run)
        assertEquals(ToneLine("No green run yet.", BodyTone.QUIET), green.run.none)
    }

    @Test
    fun aReviewWithNoFindingsIsOneRowWithItsVerdictChip() {
        assertEquals(
            listOf(
                ReviewRow("gate", "no findings", "", ToneLine("holds", BodyTone.OK), ""),
                ReviewRow("gate-2", "no findings", "", ToneLine("fails", BodyTone.BAD), ""),
                ReviewRow("gate-3", "no findings", "", ToneLine("undetermined", BodyTone.NEUTRAL), "its step ended looping"),
            ),
            reviewRowsOf(
                review("gate", "holds"),
                review("gate-2", "fails"),
                review("gate-3", "undetermined", reason = "its step ended looping"),
            ),
        )
    }

    @Test
    fun eachFindingIsARowAndTheGateNameIsOnlyOnTheFirst() {
        assertEquals(
            listOf(
                ReviewRow("gate", "synthetic claim one", "synthetic.kt:1", ToneLine("confirmed", BodyTone.BAD), "synthetic reason one"),
                ReviewRow("", "synthetic claim two", "synthetic.kt:2", ToneLine("dropped", BodyTone.NEUTRAL), "synthetic reason two"),
            ),
            reviewRowsOf(
                review(
                    "gate",
                    "fails",
                    findings = listOf(
                        finding("F1", "synthetic claim one", "synthetic.kt:1", "confirmed", "synthetic reason one"),
                        finding("F2", "synthetic claim two", "synthetic.kt:2", "dropped", "synthetic reason two"),
                    ),
                ),
            ),
        )
    }

    @Test
    fun anUndecidedFindingIsUndecidedAndAFindingNotYetVerifiedIsVerifying() {
        assertEquals(
            listOf(
                ReviewRow("gate", "synthetic claim one", "synthetic.kt:1", ToneLine("undecided", BodyTone.NEUTRAL), "synthetic reason"),
                ReviewRow("", "synthetic claim two", "synthetic.kt:2", ToneLine("undecided", BodyTone.NEUTRAL), "synthetic reason"),
                ReviewRow("", "synthetic claim three", "synthetic.kt:3", ToneLine("verifying", BodyTone.LIVE), "a verifier is checking it"),
            ),
            reviewRowsOf(
                review(
                    "gate",
                    "fails",
                    findings = listOf(
                        finding("F1", "synthetic claim one", "synthetic.kt:1", "undecided", "synthetic reason"),
                        finding("F2", "synthetic claim two", "synthetic.kt:2", "undetermined", "synthetic reason"),
                        finding("F3", "synthetic claim three", "synthetic.kt:3", "", ""),
                    ),
                ),
            ),
        )
    }

    @Test
    fun aGateCellNamesItsAttemptWhenTheReviewsSpanMoreThanOne() {
        val reviews = listOf(
            review("gate", "holds", attempt = 1L),
            review("gate", "holds", attempt = 2L),
        )

        assertEquals(
            listOf("gate · attempt 1", "gate · attempt 2"),
            reviewRowsOf(*reviews.toTypedArray()).map { it.gate },
        )
    }

    @Test
    fun noReviewSaysNoReviewHasRunYet() {
        val review = bodiesOf(detail(buildTasks = listOf(buildTask()))).review

        assertEquals(emptyList<ReviewRow>(), review.rows)
        assertEquals(ToneLine("No review has run yet.", BodyTone.QUIET), review.none)
    }

    @Test
    fun theReviewSessionDoorOpensTheLatestReadOnlyStep() {
        val epic = detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step("r1", readOnly = true, end = "done", ended = 200),
                step("w1", end = "done", ended = 300),
                step("r2", readOnly = true, end = "", started = 400),
            ),
        )

        assertEquals("r2", bodiesOf(epic).review.session)
    }

    @Test
    fun anIntegratedTaskSaysMerged() {
        assertEquals(ToneLine("Merged.", BodyTone.OK), bodiesOf(detail(buildTasks = listOf(buildTask(place = "integrated", tone = "ok")))).integrate.line)
    }

    @Test
    fun aTaskThatIsNotIntegratedWaitsForItsMerge() {
        assertEquals(
            ToneLine("Every gate took it. It waits for its merge.", BodyTone.ACC),
            bodiesOf(detail(buildTasks = listOf(buildTask()))).integrate.line,
        )
    }

    @Test
    fun aTaskWithNoIdHasNoBodies() {
        assertNull(stageBodiesOf(detail(buildTasks = listOf(buildTask())), "T9"))
    }

    private fun bodiesOf(epic: EpicDetail): StageBodies = stageBodiesOf(epic, TASK)!!

    private fun judgeOf(verdict: String, reason: String): ToneLine = bodiesOf(
        detail(
            buildTasks = listOf(buildTask()),
            steps = listOf(
                step(
                    "w1",
                    end = "checks_failed",
                    ended = 130,
                    checks = listOf(check(1, "fmt", true)),
                    verdicts = listOf(StepVerdictRow(round = 1, verdict = verdict, reason = reason)),
                ),
            ),
        ),
    ).implement.latest!!.judge

    private fun greenOf(green: EvidenceRunRow?): GreenBody =
        bodiesOf(detail(buildTasks = listOf(buildTask(green = green)))).green

    private fun reviewRowsOf(vararg reviews: TaskReviewRow): List<ReviewRow> =
        bodiesOf(detail(buildTasks = listOf(buildTask(reviews = reviews.toList())))).review.rows

    // A labelled step of task T1, writing unless readOnly, with the given checks and verdicts.
    private fun step(
        id: String,
        end: String = "",
        readOnly: Boolean = false,
        held: Boolean = false,
        started: Long = 100,
        ended: Long = 0,
        doing: String = "",
        reason: String = "",
        summary: String = "",
        checks: List<StepCheckRow> = emptyList(),
        gateName: String = "",
        verdicts: List<StepVerdictRow> = emptyList(),
    ): StepRow = StepRow(
        id = id,
        task = TASK,
        agent = "builder",
        end = end,
        status = "",
        held = held,
        started = started,
        ended = ended,
        doing = doing,
        checks = checks,
        reason = reason,
        readOnly = readOnly,
        summary = summary,
        gateName = gateName,
        verdicts = verdicts,
    )

    // A check of a round, with the detail and redacted tails the round's check carries.
    private fun check(
        round: Int,
        name: String,
        ok: Boolean,
        detail: String = "",
        tailStdout: String = "",
        tailStderr: String = "",
    ): StepCheckRow = StepCheckRow(
        round = round,
        name = name,
        ok = ok,
        stdout = "",
        stderr = "",
        detail = detail,
        tailStdout = tailStdout,
        tailStderr = tailStderr,
    )

    private fun run(id: String, phase: String, passed: Boolean): EvidenceRunRow = EvidenceRunRow(
        id = id,
        phase = phase,
        source = "captured",
        state = "ended",
        exit = 0,
        timedOut = false,
        notCaptured = "",
        verdict = "",
        judgeReason = "",
        passed = passed,
        revision = "",
        created = 0,
        ended = 0,
        logBytes = 0,
        stderrBytes = 0,
        artifacts = emptyList(),
    )

    private fun review(
        gate: String,
        verdict: String,
        attempt: Long = 1L,
        findings: List<TaskFindingRow> = emptyList(),
        reason: String = "",
    ): TaskReviewRow = TaskReviewRow(
        attempt = attempt,
        gate = gate,
        step = "",
        verdict = verdict,
        findings = findings,
        reason = reason,
    )

    private fun finding(id: String, claim: String, location: String, result: String, reason: String): TaskFindingRow =
        TaskFindingRow(
            id = id,
            claim = claim,
            location = location,
            step = "",
            result = result,
            reason = reason,
        )

    // The build task at the place and tone serve computed for it, running at implement unless the arguments say otherwise.
    private fun buildTask(
        place: String = "implement",
        tone: String = "run",
        red: EvidenceRunRow? = null,
        green: EvidenceRunRow? = null,
        reviews: List<TaskReviewRow> = emptyList(),
    ): EpicBuildTaskRow = EpicBuildTaskRow(
        id = TASK,
        title = "Parser $TASK",
        status = if (place == "integrated") "done" else "open",
        stage = "",
        red = red,
        green = green,
        requirements = listOf("R1"),
        reviews = reviews,
        place = place,
        tone = tone,
    )

    private fun detail(
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
    ): EpicDetail = EpicDetail(
        epic = EpicRow(
            id = "example-repo/example-epic",
            repository = "example-repo",
            slug = "example-epic",
            outcome = "",
            changed = 0,
            live = true,
            runningSteps = emptyList(),
            endedSteps = 0,
            problem = "",
        ),
        tasks = emptyList(),
        steps = steps,
        buildTasks = buildTasks,
    )
}
