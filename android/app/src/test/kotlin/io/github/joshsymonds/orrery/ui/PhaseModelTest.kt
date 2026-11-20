package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EpicTaskRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepRow
import org.junit.Assert.assertEquals
import org.junit.Test

// The cases of web/src/phases.test.ts, over the phone's epic rows.
class PhaseModelTest {
    // A labelled step of task T1 that runs in stage turn from second 100, unless the arguments say otherwise.
    private fun step(
        task: String = "T1",
        end: String = "",
        stage: String = "turn",
        started: Long = 100,
        ended: Long = 0,
        stageStarted: Long = 0,
        held: Boolean = false,
        limited: Boolean = false,
        readOnly: Boolean = false,
        doing: String = "",
        summary: String = "",
    ): StepRow = StepRow(
        id = "step",
        task = task,
        agent = "builder",
        end = end,
        status = "",
        held = held,
        started = started,
        ended = ended,
        doing = doing,
        checks = emptyList(),
        reason = "",
        stage = stage,
        stageStarted = stageStarted,
        limited = limited,
        readOnly = readOnly,
        summary = summary,
    )

    private fun buildTask(id: String, status: String, stage: String, requirements: List<String>) = EpicBuildTaskRow(
        id = id,
        title = "Parser",
        status = status,
        stage = stage,
        red = null,
        green = null,
        requirements = requirements,
    )

    private fun detail(
        tasks: List<EpicTaskRow> = emptyList(),
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
        requirements: List<EpicRequirementRow> = emptyList(),
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
        tasks = tasks,
        slices = emptyList(),
        steps = steps,
        requirements = requirements,
        buildTasks = buildTasks,
    )

    // A detail with the build task T1 at the given status and stage, requiring R1, and the given steps.
    private fun planOf(status: String, stage: String, steps: List<StepRow>): EpicDetail =
        detail(buildTasks = listOf(buildTask("T1", status, stage, listOf("R1"))), steps = steps)

    // The one wedge of a detail that has one task.
    private fun wedgeOf(epic: EpicDetail): Wedge {
        val wedges = wedgesOf(epic)
        assertEquals(1, wedges.size)
        return wedges[0]
    }

    // The state of each phase slice in phase order, as words.
    private fun states(wedge: Wedge): String = wedge.slices.joinToString(" ") { it.state.name.lowercase() }

    // Wedges for build tasks T1, T2 and so on, each naming its requirements at its status.
    private fun wedgesNaming(vararg tasks: Pair<List<String>, String>): List<Wedge> = wedgesOf(
        detail(
            buildTasks = tasks.mapIndexed { index, (requirements, status) ->
                buildTask("T${index + 1}", status, "", requirements)
            },
        ),
    )

    private fun run(phase: String, passed: Boolean) = EvidenceRunRow(
        id = "run-$phase",
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

    @Test
    fun aTaskWhoseStatusIsDoneOrIntegratedIsIntegratedAndOkWhateverItsSteps() {
        for (status in listOf("done", "integrated", "DONE")) {
            val wedge = wedgeOf(planOf(status, "", listOf(step(end = ""))))
            assertEquals(status, WedgePlace.INTEGRATED, wedge.place)
            assertEquals(status, WedgeTone.OK, wedge.tone)
            assertEquals(status, "passed passed passed passed", states(wedge))
        }
    }

    @Test
    fun aRunningStepInStageTurnPlacesItsTaskInImplementingRunning() {
        val wedge = wedgeOf(planOf("open", "implement", listOf(step(stage = "turn"))))
        assertEquals(WedgePlace.IMPLEMENTING, wedge.place)
        assertEquals(WedgeTone.RUN, wedge.tone)
        assertEquals("now ahead ahead ahead", states(wedge))
    }

    @Test
    fun aRunningStepInStageChecksGateOrJudgePlacesItsTaskInCheckingRunningWithTheSliceNow() {
        for (stage in listOf("checks", "gate", "judge")) {
            val wedge = wedgeOf(planOf("open", "implement", listOf(step(stage = stage))))
            assertEquals(stage, WedgePlace.CHECKING, wedge.place)
            assertEquals(stage, WedgeTone.RUN, wedge.tone)
            assertEquals(stage, "passed now ahead ahead", states(wedge))
        }
    }

    @Test
    fun aRunningReadOnlyStepPlacesItsTaskInReviewingRunningWithTheSliceNow() {
        for (stage in listOf("turn", "checks")) {
            val wedge = wedgeOf(planOf("open", "implement", listOf(step(stage = stage, readOnly = true))))
            assertEquals(stage, WedgePlace.REVIEWING, wedge.place)
            assertEquals(stage, WedgeTone.RUN, wedge.tone)
            assertEquals(stage, "passed passed now ahead", states(wedge))
        }
    }

    @Test
    fun aHeldRunningStepPlacesItsTaskAsMineAndALimitedOneAsLimitedAndHeldWinsOverLimited() {
        val held = wedgeOf(planOf("open", "implement", listOf(step(held = true))))
        assertEquals(WedgePlace.IMPLEMENTING, held.place)
        assertEquals(WedgeTone.MINE, held.tone)

        val limited = wedgeOf(planOf("open", "implement", listOf(step(limited = true))))
        assertEquals(WedgePlace.IMPLEMENTING, limited.place)
        assertEquals(WedgeTone.LIMITED, limited.tone)

        val heldAndLimited = wedgeOf(planOf("open", "implement", listOf(step(held = true, limited = true))))
        assertEquals(WedgeTone.MINE, heldAndLimited.tone)
    }

    @Test
    fun aLatestStepThatEndedDonePlacesItsTaskInIntegratingAccepted() {
        val wedge = wedgeOf(planOf("open", "green", listOf(step(end = "done", ended = 160))))
        assertEquals(WedgePlace.INTEGRATING, wedge.place)
        assertEquals(WedgeTone.ACC, wedge.tone)
        assertEquals("passed passed passed now", states(wedge))
    }

    @Test
    fun aLatestStepThatEndedOtherwiseIsBadInThePhaseItsEndAndReadOnlyFlagName() {
        val cases = listOf(
            Triple(step(end = "checks_failed", ended = 130), WedgePlace.CHECKING, "passed now ahead ahead"),
            Triple(step(end = "goal_unmet", ended = 130), WedgePlace.CHECKING, "passed now ahead ahead"),
            Triple(step(end = "failed", ended = 130), WedgePlace.IMPLEMENTING, "now ahead ahead ahead"),
            Triple(step(end = "returned", ended = 130, readOnly = true), WedgePlace.REVIEWING, "passed passed now ahead"),
        )
        for ((latest, place, slices) in cases) {
            val wedge = wedgeOf(planOf("open", "implement", listOf(latest)))
            assertEquals(latest.end, place, wedge.place)
            assertEquals(latest.end, WedgeTone.BAD, wedge.tone)
            assertEquals(latest.end, slices, states(wedge))
        }
    }

    @Test
    fun aTaskWithNoStepPlacesByItsBuildStageRunning() {
        val cases = listOf(
            Triple("red", WedgePlace.IMPLEMENTING, "now ahead ahead ahead"),
            Triple("implement", WedgePlace.IMPLEMENTING, "now ahead ahead ahead"),
            Triple("green", WedgePlace.CHECKING, "passed now ahead ahead"),
            Triple("review", WedgePlace.REVIEWING, "passed passed now ahead"),
            Triple("verify", WedgePlace.REVIEWING, "passed passed now ahead"),
        )
        for ((stage, place, slices) in cases) {
            val wedge = wedgeOf(planOf("open", stage, emptyList()))
            assertEquals(stage, place, wedge.place)
            assertEquals(stage, WedgeTone.RUN, wedge.tone)
            assertEquals(stage, slices, states(wedge))
        }
    }

    @Test
    fun aTaskWithNoStepAndNoBuildStagePlacesByItsStatusDispatchedGatedOrGap() {
        val dispatched = wedgeOf(planOf("dispatched", "", emptyList()))
        assertEquals(WedgePlace.IMPLEMENTING, dispatched.place)
        assertEquals(WedgeTone.RUN, dispatched.tone)
        assertEquals("now ahead ahead ahead", states(dispatched))

        val gated = wedgeOf(planOf("gated", "", emptyList()))
        assertEquals(WedgePlace.INTEGRATING, gated.place)
        assertEquals(WedgeTone.ACC, gated.tone)
        assertEquals("passed passed passed now", states(gated))

        val gap = wedgeOf(planOf("gap", "", emptyList()))
        assertEquals(WedgePlace.IMPLEMENTING, gap.place)
        assertEquals(WedgeTone.BAD, gap.tone)
        assertEquals("now ahead ahead ahead", states(gap))
    }

    @Test
    fun aTaskWithNoStepNoBuildStageAndNoDispatchIsPlannedAtTheRim() {
        for (status in listOf("open", "pending")) {
            val wedge = wedgeOf(planOf(status, "", emptyList()))
            assertEquals(status, WedgePlace.PLANNED, wedge.place)
            assertEquals(status, WedgeTone.PLAN, wedge.tone)
            assertEquals(status, "ahead ahead ahead ahead", states(wedge))
        }
    }

    @Test
    fun aSlicesSummaryIsItsPhasesLatestStepsSummaryDoingWhileItRunsAndEmptyWithNoStep() {
        val wedge = wedgeOf(
            planOf(
                "open",
                "implement",
                listOf(
                    step(stage = "turn", end = "stopped", started = 40, ended = 90, summary = "started the parser"),
                    step(stage = "turn", end = "done", started = 100, ended = 160, summary = "wrote the parser"),
                    step(stage = "checks", started = 170, stageStarted = 175, doing = "running checks"),
                ),
            ),
        )
        assertEquals(WedgePlace.CHECKING, wedge.place)
        assertEquals("wrote the parser", wedge.slices[0].summary)
        assertEquals("running checks", wedge.slices[1].summary)
        assertEquals("", wedge.slices[2].summary)
        assertEquals("", wedge.slices[3].summary)
    }

    @Test
    fun aNowSlicesSinceIsItsRunningStepsStageStartElseItsStartAndZeroForOtherSlices() {
        val running = wedgeOf(
            planOf(
                "open",
                "implement",
                listOf(
                    step(end = "done", started = 100, ended = 160),
                    step(stage = "checks", started = 170, stageStarted = 175),
                ),
            ),
        )
        assertEquals(175L, running.slices[1].since)
        assertEquals(0L, running.slices[0].since)
        assertEquals(0L, running.slices[2].since)

        val ended = wedgeOf(planOf("open", "implement", listOf(step(end = "failed", started = 300, ended = 330))))
        assertEquals(300L, ended.slices[0].since)

        val none = wedgeOf(planOf("open", "red", emptyList()))
        assertEquals(0L, none.slices[0].since)
    }

    @Test
    fun aPassedSlicesSecondsAreTheSumOfItsEndedStepsDurationsAndZeroForEveryOtherSlice() {
        val wedge = wedgeOf(
            planOf(
                "open",
                "implement",
                listOf(
                    step(end = "returned", started = 100, ended = 130),
                    step(end = "returned", started = 140, ended = 200),
                    step(readOnly = true, end = "done", started = 210, ended = 240),
                ),
            ),
        )
        assertEquals(WedgePlace.INTEGRATING, wedge.place)
        assertEquals(90L, wedge.slices[0].seconds)
        assertEquals(0L, wedge.slices[1].seconds)
        assertEquals(30L, wedge.slices[2].seconds)
        assertEquals(0L, wedge.slices[3].seconds)

        val failed = wedgeOf(planOf("open", "implement", listOf(step(stage = "checks", end = "checks_failed", started = 300, ended = 330))))
        assertEquals(WedgePlace.CHECKING, failed.place)
        assertEquals(0L, failed.slices[1].seconds)
    }

    @Test
    fun wedgesComeFromTheBuildTasksWhenThereAreAnyAndFromTheRecordsTasksWhenThereAreNone() {
        val planned = wedgesOf(
            detail(
                buildTasks = listOf(buildTask("T1", "open", "", listOf("R1"))),
                tasks = listOf(EpicTaskRow(effort = 1, slug = "old", subject = "Old", status = "done", requirements = emptyList())),
            ),
        )
        assertEquals(1, planned.size)
        assertEquals("T1", planned[0].task)
        assertEquals("Parser", planned[0].title)
        assertEquals(listOf("R1"), planned[0].requirements)

        val record = wedgesOf(
            detail(
                tasks = listOf(
                    EpicTaskRow(effort = 1, slug = "first", subject = "First task", status = "done", requirements = listOf("R2", "R3")),
                    EpicTaskRow(effort = 1, slug = "second", subject = "Second", status = "pending", requirements = emptyList()),
                ),
            ),
        )
        assertEquals(2, record.size)
        assertEquals("first", record[0].task)
        assertEquals("First task", record[0].title)
        assertEquals(listOf("R2", "R3"), record[0].requirements)
        assertEquals(WedgePlace.INTEGRATED, record[0].place)
        assertEquals(WedgePlace.PLANNED, record[1].place)
    }

    @Test
    fun aTasksStepsAreThoseLabelledWithItsId() {
        val wedge = wedgeOf(planOf("open", "", listOf(step(task = "T2"))))
        assertEquals(WedgePlace.PLANNED, wedge.place)
        assertEquals(WedgeTone.PLAN, wedge.tone)
    }

    @Test
    fun aWedgesRequirementsSplitItsSpanAndAPartContinuesThePreviousArcOfItsRequirement() {
        val arcs = requirementArcs(
            wedgesNaming(listOf("R1", "R2") to "open", listOf("R2") to "open", listOf("R3") to "open"),
            emptyList(),
        )
        assertEquals(3, arcs.size)
        assertEquals("R1", arcs[0].id)
        assertEquals(0.0, arcs[0].start, 0.0)
        assertEquals(0.5, arcs[0].end, 0.0)
        assertEquals("R2", arcs[1].id)
        assertEquals(0.5, arcs[1].start, 0.0)
        assertEquals(2.0, arcs[1].end, 0.0)
        assertEquals("R3", arcs[2].id)
        assertEquals(2.0, arcs[2].start, 0.0)
        assertEquals(3.0, arcs[2].end, 0.0)
    }

    @Test
    fun anArcDoesNotContinueAcrossAWedgeWithNoRequirement() {
        val arcs = requirementArcs(
            wedgesNaming(listOf("R1") to "open", emptyList<String>() to "open", listOf("R1") to "open"),
            emptyList(),
        )
        assertEquals(2, arcs.size)
        assertEquals(0.0, arcs[0].start, 0.0)
        assertEquals(1.0, arcs[0].end, 0.0)
        assertEquals(2.0, arcs[1].start, 0.0)
        assertEquals(3.0, arcs[1].end, 0.0)
    }

    @Test
    fun anArcIsProvenWhenItsRequirementHasAGreenRunThatPassed() {
        val requirements = listOf(
            EpicRequirementRow(id = "R1", text = "", evidence = "", kind = "check", runs = listOf(run("red", true), run("green", true))),
            EpicRequirementRow(id = "R2", text = "", evidence = "", kind = "check", runs = listOf(run("red", true), run("green", false))),
            EpicRequirementRow(id = "R3", text = "", evidence = "", kind = "check", runs = listOf(run("red", true))),
        )
        val arcs = requirementArcs(
            wedgesNaming(listOf("R1") to "open", listOf("R2") to "open", listOf("R3") to "open"),
            requirements,
        )
        assertEquals("R1:true R2:false R3:false", arcs.joinToString(" ") { "${it.id}:${it.proven}" })
    }

    @Test
    fun anArcWithNoMatchingRequirementIsProvenWhenEveryWedgeNamingItIsIntegrated() {
        val allIntegrated = requirementArcs(
            wedgesNaming(listOf("R9") to "done", listOf("R9") to "done"),
            emptyList(),
        )
        assertEquals(1, allIntegrated.size)
        assertEquals(true, allIntegrated[0].proven)

        val oneOpen = requirementArcs(
            wedgesNaming(listOf("R9") to "done", listOf("R9") to "open"),
            emptyList(),
        )
        assertEquals(1, oneOpen.size)
        assertEquals(false, oneOpen[0].proven)
    }

    @Test
    fun durationTextGivesSecondsUnderAMinuteMinutesUnderAnHourAndHoursWithTheMinutesLeftOver() {
        assertEquals("0 s", durationText(0))
        assertEquals("59 s", durationText(59))
        assertEquals("1 min", durationText(60))
        assertEquals("1 min", durationText(119))
        assertEquals("59 min", durationText(3599))
        assertEquals("1 h", durationText(3600))
        assertEquals("1 h 1 min", durationText(3660))
        assertEquals("2 h 2 min", durationText(7320))
    }
}
