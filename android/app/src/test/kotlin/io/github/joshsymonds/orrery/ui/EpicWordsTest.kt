package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicHistoryRow
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicReleaseProofRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EpicTaskRow
import io.github.joshsymonds.orrery.api.StepRow
import java.time.Instant
import java.time.ZoneId
import org.junit.Assert.assertEquals
import org.junit.Test

class EpicWordsTest {
    @Test
    fun aBuildingEpicsTrackIsDoneBeforeBuildAndCountsItsTasks() {
        val d = detail(
            epic = epic(outcome = "built"),
            contract = contract(accepted = NOW - 3600),
            buildTasks = listOf(buildTask("example-task-a", "integrated"), buildTask("example-task-b", "planned")),
        )

        assertEquals(
            listOf(
                Stage("Contract", StageState.DONE, WordTone.OK, "accepted ${clock(NOW - 3600)}"),
                Stage("Build", StageState.NOW, WordTone.LIVE, "1 of 2 tasks done"),
                ahead("Review"),
                ahead("Release"),
                ahead("Report"),
            ),
            trackOf(d),
        )
    }

    @Test
    fun aPlanningEpicWithNoTaskSaysTheContractWasAcceptedWithNoTimeWhenItIsZero() {
        val d = detail(epic = epic(outcome = "accepted"), contract = contract(accepted = 0L))

        assertEquals(
            listOf(
                Stage("Contract", StageState.DONE, WordTone.OK, "accepted"),
                Stage("Build", StageState.NOW, WordTone.LIVE, "the planner is outlining tasks"),
                ahead("Review"),
                ahead("Release"),
                ahead("Report"),
            ),
            trackOf(d),
        )
    }

    @Test
    fun aBuildWithNoTaskSaysOnlyThatThePlannerIsOutliningEvenWhenAQuestionAsks() {
        val d = detail(
            epic = epic(outcome = "accepted"),
            contract = contract(accepted = NOW - 3600),
            questions = listOf(question(asked = NOW - 120)),
        )

        assertEquals(
            Stage("Build", StageState.NOW, WordTone.ATTENTION, "the planner is outlining tasks"),
            trackOf(d)[1],
        )
    }

    @Test
    fun aPhaseTheClientDoesNotKnowIsContractNowInItsOwnWords() {
        val d = detail(epic = epic(outcome = "proposing"))

        assertEquals(Stage("Contract", StageState.NOW, WordTone.LIVE, "proposing"), trackOf(d)[0])
    }

    @Test
    fun anEpicThatEndedWithGapsIsReportNowAndAttention() {
        val d = detail(epic = epic(outcome = "ended_with_gaps"), buildTasks = listOf(buildTask("a", "integrated")))

        assertEquals(Stage("Report", StageState.NOW, WordTone.ATTENTION, "ended with gaps"), trackOf(d)[4])
    }

    @Test
    fun aReviewingEpicsTrackHasItsBuildDoneAndReviewingNow() {
        val d = detail(
            epic = epic(outcome = "reviewing"),
            contract = contract(accepted = NOW - 3600),
            buildTasks = listOf(buildTask("example-task-a", "integrated"), buildTask("example-task-b", "integrated")),
        )

        assertEquals(
            listOf(
                Stage("Contract", StageState.DONE, WordTone.OK, "accepted ${clock(NOW - 3600)}"),
                Stage("Build", StageState.DONE, WordTone.OK, "2 of 2 tasks done"),
                Stage("Review", StageState.NOW, WordTone.LIVE, "reviewing"),
                ahead("Release"),
                ahead("Report"),
            ),
            trackOf(d),
        )
    }

    @Test
    fun aCorrectingEpicsReviewStageSaysCorrecting() {
        val d = detail(
            epic = epic(outcome = "correcting"),
            buildTasks = listOf(buildTask("example-task-a", "integrated")),
        )

        assertEquals(Stage("Review", StageState.NOW, WordTone.LIVE, "correcting"), trackOf(d)[2])
    }

    @Test
    fun aCleanEpicsReviewIsDoneAndItIsReadyToRelease() {
        val d = detail(epic = epic(outcome = "clean"), buildTasks = listOf(buildTask("example-task-a", "integrated")))

        assertEquals(
            listOf(
                Stage("Contract", StageState.DONE, WordTone.OK, "accepted"),
                Stage("Build", StageState.DONE, WordTone.OK, "1 of 1 tasks done"),
                Stage("Review", StageState.DONE, WordTone.OK, "clean"),
                Stage("Release", StageState.NOW, WordTone.QUIET, "ready to release"),
                ahead("Report"),
            ),
            trackOf(d),
        )
    }

    @Test
    fun aReleasingOrStoppedEpicsReleaseStageNamesItsStateWithItsTone() {
        val cases = listOf(
            Stage("Release", StageState.NOW, WordTone.ENGINE, "releasing"),
            Stage("Release", StageState.NOW, WordTone.BAD, "stopped"),
        )

        assertEquals(
            cases,
            listOf("releasing", "stopped").map { trackOf(detail(epic = epic(outcome = it)))[3] },
        )
    }

    @Test
    fun aReleasedEpicsReleaseIsDoneAndReportSaysReleased() {
        val d = detail(
            epic = epic(outcome = "released"),
            buildTasks = listOf(buildTask("example-task-a", "integrated")),
        )

        assertEquals(Stage("Release", StageState.DONE, WordTone.OK, "released"), trackOf(d)[3])
        assertEquals(Stage("Report", StageState.NOW, WordTone.OK, "released"), trackOf(d)[4])
    }

    @Test
    fun aBuildWithAQuestionAsksAndSaysHowManyWait() {
        val d = detail(
            epic = epic(outcome = "built"),
            buildTasks = listOf(buildTask("example-task-a", "planned")),
            questions = listOf(question(asked = NOW - 120)),
        )

        assertEquals(
            Stage("Build", StageState.NOW, WordTone.ATTENTION, "0 of 1 tasks done · 1 waits on you"),
            trackOf(d)[1],
        )
    }

    @Test
    fun aBuildWhoseQuestionOrParkItsSteererHoldsDoesNotSayItWaitsOnYou() {
        val held = detail(
            epic = epic(outcome = "built"),
            buildTasks = listOf(buildTask("example-task-a", "planned")),
            questions = listOf(question(asked = NOW - 120, steererHeld = true)),
        )
        val parked = detail(
            epic = epic(outcome = "built"),
            buildTasks = listOf(buildTask("example-task-a", "planned")),
            parked = "budget",
            steererParked = true,
        )

        assertEquals(Stage("Build", StageState.NOW, WordTone.LIVE, "0 of 1 tasks done"), trackOf(held)[1])
        assertEquals(Stage("Build", StageState.NOW, WordTone.LIVE, "0 of 1 tasks done"), trackOf(parked)[1])
    }

    @Test
    fun aBuildWithEveryRunningStepLimitedIsQuietAndSaysItWaitsOnALimit() {
        val d = detail(
            epic = epic(outcome = "built"),
            buildTasks = listOf(buildTask("example-task-a", "integrated"), buildTask("example-task-b", "planned")),
            steps = listOf(step(limited = true)),
        )

        assertEquals(
            Stage("Build", StageState.NOW, WordTone.QUIET, "1 of 2 tasks done · waiting on a limit"),
            trackOf(d)[1],
        )
    }

    @Test
    fun aPausedBuildIsQuietAndSaysPaused() {
        val d = detail(
            epic = epic(outcome = "built"),
            buildTasks = listOf(buildTask("example-task-a", "integrated"), buildTask("example-task-b", "planned")),
            paused = true,
        )

        assertEquals(Stage("Build", StageState.NOW, WordTone.QUIET, "1 of 2 tasks done · paused"), trackOf(d)[1])
    }

    @Test
    fun aPlanningEpicReachesItsSummaryTrackNowHistoryAndRequirementsButNoTasks() {
        val d = detail(
            epic = epic(outcome = "accepted"),
            summary = "Example summary.",
            requirements = listOf(requirement("R1")),
        )

        assertEquals(
            Reached(summary = true, track = true, now = true, history = true, tasks = false, requirements = true),
            reachedOf(d, listOf(historyEntry())),
        )
    }

    @Test
    fun aSummaryFallsBackToTheContractTitle() {
        val d = detail(epic = epic(outcome = "accepted"), contract = contract(title = "Example epic title"))

        assertEquals(
            Reached(summary = true, track = true, now = true, history = false, tasks = false, requirements = false),
            reachedOf(d, emptyList()),
        )
    }

    @Test
    fun theContractDoorNamesItsAcceptanceCareAndRequirements() {
        val d = detail(
            contract = contract(accepted = NOW - 3600, care = "serious"),
            requirements = listOf(requirement("R1"), requirement("R2")),
        )

        assertEquals("accepted ${clock(NOW - 3600)} · serious care · 2 requirements", contractDoorOf(d))
    }

    @Test
    fun theContractDoorIsEmptyWithNoContract() {
        assertEquals("", contractDoorOf(detail()))
    }

    @Test
    fun theStatePillWordAndToneForEachOutcome() {
        val cases = listOf(
            detail(epic = epic(outcome = "built"), paused = true) to Pill("paused", WordTone.QUIET),
            detail(epic = epic(outcome = "built"), parked = "budget") to Pill("parked", WordTone.ATTENTION),
            detail(epic = epic(outcome = "accepted"), parked = "budget", plannerParked = true) to
                Pill("planning", WordTone.LIVE),
            detail(epic = epic(outcome = "accepted")) to Pill("planning", WordTone.LIVE),
            detail(epic = epic(outcome = "accepted"), buildTasks = listOf(buildTask("a", "planned"))) to
                Pill("building", WordTone.LIVE),
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                steps = listOf(step(limited = true)),
            ) to Pill("building", WordTone.QUIET),
            detail(epic = epic(outcome = "reviewing")) to Pill("reviewing", WordTone.LIVE),
            detail(epic = epic(outcome = "correcting")) to Pill("reviewing", WordTone.LIVE),
            detail(epic = epic(outcome = "clean")) to Pill("ready to release", WordTone.QUIET),
            detail(epic = epic(outcome = "releasing")) to Pill("releasing", WordTone.ENGINE),
            detail(epic = epic(outcome = "stopped")) to Pill("stopped", WordTone.BAD),
            detail(epic = epic(outcome = "released")) to Pill("released", WordTone.OK),
            detail(epic = epic(outcome = "done")) to Pill("done", WordTone.OK),
            detail(epic = epic(outcome = "met_at_base")) to Pill("already met", WordTone.QUIET),
            detail(epic = epic(outcome = "ended_with_gaps")) to Pill("ended with gaps", WordTone.ATTENTION),
            detail(epic = epic(outcome = "closed_superseded")) to Pill("superseded", WordTone.QUIET),
            detail(epic = epic(outcome = "closed_shipped")) to Pill("shipped outside orrery", WordTone.OK),
            detail(epic = epic(outcome = "closed_abandoned")) to Pill("abandoned", WordTone.QUIET),
            detail(epic = epic(outcome = "proposing")) to Pill("proposing", WordTone.QUIET),
            detail(epic = epic(outcome = "in progress")) to Pill("in progress", WordTone.QUIET),
        )

        assertEquals(cases.map { it.second }, cases.map { statePillOf(it.first, NOW) })
    }

    @Test
    fun theStatePillCarriesHowLongThePhaseHasLasted() {
        val d = detail(epic = epic(outcome = "reviewing", phaseAt = NOW - 300))

        assertEquals(Pill("reviewing · 5 min", WordTone.LIVE), statePillOf(d, NOW))
    }

    @Test
    fun theBlockerPillOnlyNamesWhatAsks() {
        val cases = listOf(
            detail(epic = epic(outcome = "built"), questions = listOf(question(asked = NOW - 120))) to
                Pill("asks you a question · 2 min", WordTone.ATTENTION),
            detail(epic = epic(outcome = "built"), parked = "budget") to
                Pill("asks you what to do", WordTone.ATTENTION),
            detail(epic = epic(outcome = "built"), steps = listOf(step(held = true))) to
                Pill("example-task held", WordTone.ATTENTION),
            detail(epic = epic(outcome = "built"), steps = listOf(step(limited = true))) to null,
            detail(epic = epic(outcome = "built"), parked = "budget", plannerParked = true) to null,
            detail(epic = epic(outcome = "built"), paused = true) to null,
        )

        assertEquals(cases.map { it.second }, cases.map { blockerPillOf(it.first, NOW) })
    }

    @Test
    fun theBlockerPillNamesTheSteerersParkAndQuestionQuietlyAfterWhatAsksTheOwner() {
        val steererQuestion = question(asked = NOW - 300, id = "Q1", steererHeld = true)
        val ownerQuestion = question(asked = NOW - 120, id = "Q2")
        val cases = listOf(
            detail(epic = epic(outcome = "built"), parked = "budget", steererParked = true) to
                Pill("its steerer holds the park", WordTone.QUIET),
            detail(epic = epic(outcome = "built"), questions = listOf(steererQuestion)) to
                Pill("asks its steerer a question · 5 min", WordTone.QUIET),
            detail(epic = epic(outcome = "built"), questions = listOf(steererQuestion, ownerQuestion)) to
                Pill("asks you a question · 2 min", WordTone.ATTENTION),
            detail(
                epic = epic(outcome = "built"),
                questions = listOf(steererQuestion),
                parked = "budget",
                steererParked = true,
            ) to Pill("its steerer holds the park", WordTone.QUIET),
            detail(
                epic = epic(outcome = "built"),
                parked = "budget",
                steererParked = true,
                steps = listOf(step(held = true)),
            ) to Pill("example-task held", WordTone.ATTENTION),
            detail(
                epic = epic(outcome = "stopped"),
                releaseProof = listOf(releaseStep(1L, "passed"), releaseStep(2L, "failed", undo = "none")),
                parked = "release step 2 failed",
                steererParked = true,
            ) to Pill("its steerer holds the park", WordTone.QUIET),
            detail(epic = epic(outcome = "ended_with_gaps"), questions = listOf(steererQuestion)) to
                Pill("release it anyway, or close it", WordTone.ATTENTION),
        )

        assertEquals(cases.map { it.second }, cases.map { blockerPillOf(it.first, NOW) })
    }

    @Test
    fun theNowSentenceNamesTheSteerersParkAndASteerersQuestionAsItsSteerers() {
        val steererQuestion = question(asked = NOW - 120, steererHeld = true)
        val cases = listOf(
            detail(epic = epic(outcome = "built"), parked = "budget", steererParked = true) to
                "Parked. Its steerer holds the question; nothing is asked of you unless it escalates it.",
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                questions = listOf(steererQuestion),
            ) to "It waits on its steerer's answer.",
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                steps = listOf(step(task = "example-task")),
                questions = listOf(steererQuestion),
            ) to "One task is being worked on, and one waits on its steerer.",
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                steps = listOf(step(id = "s1", task = "example-task-a"), step(id = "s2", task = "example-task-b")),
                questions = listOf(steererQuestion),
            ) to "2 tasks are being worked on, and one waits on its steerer.",
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                questions = listOf(steererQuestion, question(asked = NOW - 60, id = "Q2")),
            ) to "It waits on your answer.",
        )

        assertEquals(cases.map { it.second }, cases.map { nowSentenceOf(it.first) })
    }

    @Test
    fun theNowSentenceNamesTheFirstStateThatHolds() {
        val cases = listOf(
            detail(epic = epic(outcome = "built"), parked = "budget") to
                "Parked. Nothing runs until you answer.",
            detail(epic = epic(outcome = "built"), paused = true) to
                "Paused. The steps that were running finish; nothing new starts until you resume.",
            detail(epic = epic(outcome = "built"), parked = "budget", plannerParked = true) to
                "The planner holds a park and is deciding what to do. Nothing is asked of you.",
            detail(epic = epic(outcome = "accepted")) to
                "The planner is reading the contract and outlining tasks. No task has started.",
            detail(epic = epic(outcome = "released"), buildTasks = listOf(buildTask("a", "integrated"))) to
                "It ended: released.",
            detail(epic = epic(outcome = "built"), buildTasks = listOf(buildTask("a", "planned"))) to
                "Nothing is running.",
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                steps = listOf(
                    step(id = "s1", task = "example-task-a"),
                    step(id = "s2", task = "example-task-a"),
                    step(id = "s3", task = "example-task-b"),
                ),
            ) to "2 tasks are being worked on.",
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                steps = listOf(step(task = "example-task")),
                questions = listOf(question(asked = NOW - 120)),
            ) to "One task is being worked on, and one waits on you.",
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                questions = listOf(question(asked = NOW - 120)),
            ) to "It waits on your answer.",
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                steps = listOf(step(id = "s1", limited = true, limitKind = "weekly", limitResets = NOW + 3600)),
            ) to "1 step waits on the weekly limit, which resets at ${clock(NOW + 3600)}. It resumes on its own.",
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                steps = listOf(step(id = "s1", limited = true), step(id = "s2", limited = true)),
            ) to "2 steps wait on a usage limit. They resume on their own.",
        )

        assertEquals(cases.map { it.second }, cases.map { nowSentenceOf(it.first) })
    }

    @Test
    fun theNowSentenceTakesTheOwnersParkBeforeAPauseAndALimitBeforeAQuestion() {
        val cases = listOf(
            detail(epic = epic(outcome = "built"), parked = "budget", paused = true) to
                "Parked. Nothing runs until you answer.",
            detail(
                epic = epic(outcome = "built"),
                buildTasks = listOf(buildTask("a", "planned")),
                questions = listOf(question(asked = NOW - 120)),
                steps = listOf(step(id = "s1", limited = true)),
            ) to "1 step waits on a usage limit. It resumes on its own.",
        )

        assertEquals(cases.map { it.second }, cases.map { nowSentenceOf(it.first) })
    }

    @Test
    fun aLimitedStepNamesItsKindOnlyWhenItHasOneAndItsResetOnlyWhenItHasOne() {
        val d = detail(
            epic = epic(outcome = "built"),
            buildTasks = listOf(buildTask("example-task-a", "planned")),
            steps = listOf(
                step(id = "s-bare", task = "example-task-a", limited = true),
                step(id = "s-kind", task = "example-task-b", limited = true, limitKind = "weekly"),
            ),
        )

        assertEquals(
            listOf(
                StepLine(
                    id = "s-bare",
                    glyph = StepGlyph.LIMITED,
                    label = "example-task-a",
                    task = "example-task-a",
                    doing = "waits on a usage limit",
                    since = NOW - 600,
                ),
                StepLine(
                    id = "s-kind",
                    glyph = StepGlyph.LIMITED,
                    label = "example-task-b",
                    task = "example-task-b",
                    doing = "waits on the weekly limit",
                    since = NOW - 600,
                ),
            ),
            stepLinesOf(d),
        )
    }

    @Test
    fun askingNamesTheQuestionTheOwnersParkThePlannersParkOrTheLimit() {
        val question = question(asked = NOW - 120)
        val cases = listOf(
            detail(epic = epic(outcome = "built"), questions = listOf(question)) to Asking.Question(question),
            detail(epic = epic(outcome = "built"), parked = "budget exhausted") to
                Asking.Park("Budget exhausted."),
            detail(epic = epic(outcome = "built"), parked = "budget exhausted", plannerParked = true) to
                Asking.Planner("The planner holds this park: budget exhausted. It will retry, replan, or ask you."),
            detail(epic = epic(outcome = "built"), steps = listOf(step(limited = true))) to Asking.Limited,
            detail(epic = epic(outcome = "built"), buildTasks = listOf(buildTask("a", "planned"))) to null,
        )

        assertEquals(cases.map { it.second }, cases.map { askingOf(it.first) })
    }

    @Test
    fun askingNamesTheSteerersParkThenTheSteerersQuestionAsLinesAfterWhatAsksTheOwner() {
        val steererQuestion = question(asked = NOW - 300, id = "Q1", steererHeld = true)
        val ownerQuestion = question(asked = NOW - 120, id = "Q2")
        val parkLine = Asking.Steerer(
            "Its steerer holds this park, and answers it or escalates it to you: budget exhausted",
        )
        val cases = listOf(
            detail(epic = epic(outcome = "built"), parked = "budget exhausted", steererParked = true) to parkLine,
            detail(epic = epic(outcome = "built"), questions = listOf(steererQuestion)) to
                Asking.Steerer("Its steerer holds this question, and answers it or escalates it to you: Which branch?"),
            detail(
                epic = epic(outcome = "built"),
                questions = listOf(steererQuestion),
                parked = "budget exhausted",
                steererParked = true,
            ) to parkLine,
            detail(epic = epic(outcome = "built"), questions = listOf(steererQuestion, ownerQuestion)) to
                Asking.Question(ownerQuestion),
            detail(
                epic = epic(outcome = "built"),
                questions = listOf(steererQuestion),
                steps = listOf(step(limited = true)),
            ) to Asking.Steerer("Its steerer holds this question, and answers it or escalates it to you: Which branch?"),
        )

        assertEquals(cases.map { it.second }, cases.map { askingOf(it.first) })
    }

    @Test
    fun aRunningStepsRowNamesItsStateWhatItDoesAndItsSince() {
        val d = detail(
            epic = epic(outcome = "built"),
            steps = listOf(
                step(
                    id = "s-limit",
                    task = "example-task-a",
                    limited = true,
                    limitKind = "weekly",
                    limitResets = NOW + 3600,
                    stageStarted = NOW - 90,
                ),
                step(id = "s-held", task = "", agent = "example-agent", held = true, reason = "needs an answer"),
                step(id = "s-work", task = "example-task-c", stageStarted = NOW - 50),
                step(id = "s-both", task = "example-task-d", held = true, limited = true),
                step(id = "s-ended", task = "example-task-e", end = "done"),
            ),
        )

        assertEquals(
            listOf(
                StepLine(
                    id = "s-limit",
                    glyph = StepGlyph.LIMITED,
                    label = "example-task-a",
                    task = "example-task-a",
                    doing = "waits on the weekly limit · resets ${clock(NOW + 3600)}",
                    since = NOW - 90,
                ),
                StepLine(
                    id = "s-held",
                    glyph = StepGlyph.HELD,
                    label = "example-agent",
                    task = "",
                    doing = "held · needs an answer",
                    since = NOW - 600,
                ),
                StepLine(
                    id = "s-work",
                    glyph = StepGlyph.WORKING,
                    label = "example-task-c",
                    task = "example-task-c",
                    doing = "Example doing.",
                    since = NOW - 50,
                ),
                StepLine(
                    id = "s-both",
                    glyph = StepGlyph.HELD,
                    label = "example-task-d",
                    task = "example-task-d",
                    doing = "held",
                    since = NOW - 600,
                ),
            ),
            stepLinesOf(d),
        )
    }

    @Test
    fun aReleasingEpicsNowNamesItsRunningStepOfHowManyAndElseOnlyThatItReleases() {
        val cases = listOf(
            detail(
                epic = epic(outcome = "releasing"),
                releaseProof = listOf(
                    releaseStep(1L, "passed"),
                    releaseStep(2L, "running"),
                    releaseStep(3L, "waiting"),
                ),
            ) to "Releasing: step 2 of 3 is running.",
            detail(
                epic = epic(outcome = "releasing"),
                releaseProof = listOf(releaseStep(1L, "passed"), releaseStep(2L, "waiting")),
            ) to "Releasing.",
            detail(epic = epic(outcome = "releasing")) to "Releasing.",
        )

        assertEquals(cases.map { it.second }, cases.map { nowSentenceOf(it.first) })
    }

    @Test
    fun aStoppedReleaseNowNamesItsStepAndSaysWhetherItWaitsOnTheOwner() {
        val cases = listOf(
            detail(
                epic = epic(outcome = "stopped"),
                releaseProof = listOf(releaseStep(1L, "passed"), releaseStep(2L, "failed", undo = "none")),
            ) to "The release stopped at step 2, and that step has no undo. It waits on you.",
            detail(
                epic = epic(outcome = "stopped"),
                releaseProof = listOf(releaseStep(1L, "passed"), releaseStep(2L, "failed", undo = "git revert")),
            ) to "The release stopped at step 2.",
            detail(
                epic = epic(outcome = "stopped"),
                releaseProof = listOf(
                    releaseStep(1L, "passed"),
                    releaseStep(2L, "waiting"),
                    releaseStep(3L, "failed", undo = "none"),
                ),
            ) to "The release stopped at step 2.",
            detail(epic = epic(outcome = "stopped")) to "The release stopped.",
        )

        assertEquals(cases.map { it.second }, cases.map { nowSentenceOf(it.first) })
    }

    @Test
    fun theBlockerPillNamesAStoppedStepWithNoUndoAndAnEndedWithGapsEpicsTwoWaysOn() {
        val cases = listOf(
            detail(
                epic = epic(outcome = "stopped"),
                releaseProof = listOf(releaseStep(1L, "passed"), releaseStep(2L, "failed", undo = "none")),
            ) to Pill("step 2 failed and cannot be undone", WordTone.ATTENTION),
            detail(
                epic = epic(outcome = "stopped"),
                releaseProof = listOf(releaseStep(1L, "passed"), releaseStep(2L, "failed", undo = "git revert")),
            ) to null,
            detail(epic = epic(outcome = "ended_with_gaps")) to
                Pill("release it anyway, or close it", WordTone.ATTENTION),
        )

        assertEquals(cases.map { it.second }, cases.map { blockerPillOf(it.first, NOW) })
    }

    @Test
    fun anOpenQuestionOrAnOwnersParkStillWinsOverTheReleaseWords() {
        val releaseFailed = listOf(releaseStep(1L, "failed", undo = "none"))
        val questioned = detail(
            epic = epic(outcome = "stopped"),
            releaseProof = releaseFailed,
            questions = listOf(question(asked = NOW - 120)),
        )
        val parked = detail(epic = epic(outcome = "ended_with_gaps"), parked = "budget")

        assertEquals("It waits on your answer.", nowSentenceOf(questioned))
        assertEquals(Pill("asks you a question · 2 min", WordTone.ATTENTION), blockerPillOf(questioned, NOW))
        assertEquals("Parked. Nothing runs until you answer.", nowSentenceOf(parked))
        assertEquals(Pill("asks you what to do", WordTone.ATTENTION), blockerPillOf(parked, NOW))
        assertEquals(
            Pill("example-task held", WordTone.ATTENTION),
            blockerPillOf(
                detail(
                    epic = epic(outcome = "stopped"),
                    releaseProof = releaseFailed,
                    steps = listOf(step(held = true)),
                ),
                NOW,
            ),
        )
    }

    private fun clock(time: Long): String =
        CLOCK_TIME.format(Instant.ofEpochSecond(time).atZone(ZoneId.systemDefault()))

    private fun releaseStep(position: Long, state: String, undo: String = "") = EpicReleaseProofRow(
        position = position,
        step = "example-release-step",
        target = "",
        undo = undo,
        state = state,
        evidence = "",
        reason = "",
        attempt = 0L,
        at = 0L,
    )

    private fun ahead(name: String) = Stage(name, StageState.AHEAD, WordTone.QUIET, "not reached")

    private fun epic(outcome: String = "", phaseAt: Long = 0L) = EpicRow(
        id = "example-repo/example-epic",
        repository = "example-repo",
        slug = "example-epic",
        outcome = outcome,
        changed = NOW,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        phaseAt = phaseAt,
    )

    // A build task at the place serve computed for it, done once integrated and open before.
    private fun buildTask(id: String, place: String) = EpicBuildTaskRow(
        id = id,
        title = "",
        status = if (place == "integrated") "done" else "open",
        stage = "",
        red = null,
        green = null,
        place = place,
        tone = when (place) {
            "integrated" -> "ok"
            "planned" -> "plan"
            else -> "run"
        },
    )

    private fun contract(accepted: Long = NOW - 3600, care: String = "serious", title: String = "Example epic title") =
        EpicContractRow(
            title = title,
            approved = "",
            intent = "",
            failures = emptyList(),
            care = care,
            additions = emptyList(),
            premises = emptyList(),
            exclusions = emptyList(),
            approach = "",
            alternatives = emptyList(),
            releaseSteps = emptyList(),
            releaseThen = "",
            checks = "",
            base = "",
            accepted = accepted,
        )

    private fun requirement(id: String) =
        EpicRequirementRow(id = id, text = "Example requirement.", evidence = "", kind = "check")

    private fun question(asked: Long, id: String = "Q1", steererHeld: Boolean = false) =
        EpicQuestionRow(id = id, text = "Which branch?", turn = 1L, asked = asked, steererHeld = steererHeld)

    private fun historyEntry() = EpicHistoryRow(time = NOW, cursor = "example-cursor", kind = "")

    private fun step(
        id: String = "example-step-1",
        task: String = "example-task",
        agent: String = "example-agent",
        end: String = "",
        held: Boolean = false,
        limited: Boolean = false,
        limitKind: String = "",
        limitResets: Long = 0L,
        stageStarted: Long = 0L,
        reason: String = "",
    ) = StepRow(
        id = id,
        task = task,
        agent = agent,
        end = end,
        status = "working",
        held = held,
        started = NOW - 600,
        ended = 0L,
        doing = "Example doing.",
        checks = emptyList(),
        reason = reason,
        stageStarted = stageStarted,
        limited = limited,
        limitKind = limitKind,
        limitResets = limitResets,
    )

    private fun detail(
        epic: EpicRow = epic(),
        tasks: List<EpicTaskRow> = emptyList(),
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
        questions: List<EpicQuestionRow> = emptyList(),
        requirements: List<EpicRequirementRow> = emptyList(),
        summary: String = "",
        contract: EpicContractRow? = null,
        paused: Boolean = false,
        parked: String = "",
        plannerParked: Boolean = false,
        steererParked: Boolean = false,
        releaseProof: List<EpicReleaseProofRow> = emptyList(),
    ) = EpicDetail(
        epic = epic,
        tasks = tasks,
        steps = steps,
        requirements = requirements,
        buildTasks = buildTasks,
        summary = summary,
        contract = contract,
        questions = questions,
        paused = paused,
        parked = parked,
        plannerParked = plannerParked,
        steererParked = steererParked,
        releaseProof = releaseProof,
    )

    private companion object {
        const val NOW = 1_800_000_000L
    }
}
