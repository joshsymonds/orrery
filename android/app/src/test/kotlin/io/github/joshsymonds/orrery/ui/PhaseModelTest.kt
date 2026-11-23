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

    // An open build task at the place and tone serve computed for it.
    private fun buildTask(id: String, place: String, tone: String, requirements: List<String>) = EpicBuildTaskRow(
        id = id,
        title = "Parser",
        status = "open",
        stage = "",
        red = null,
        green = null,
        requirements = requirements,
        place = place,
        tone = tone,
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
        ),
        tasks = tasks,
        steps = steps,
        requirements = requirements,
        buildTasks = buildTasks,
    )

    // A detail with the build task T1 at the given place and tone, requiring R1, and the given steps.
    private fun planOf(place: String, tone: String, steps: List<StepRow>): EpicDetail =
        detail(buildTasks = listOf(buildTask("T1", place, tone, listOf("R1"))), steps = steps)

    // The one wedge of a detail that has one task.
    private fun wedgeOf(epic: EpicDetail): Wedge {
        val wedges = wedgesOf(epic)
        assertEquals(1, wedges.size)
        return wedges[0]
    }

    // The state of each phase slice in phase order, as words.
    private fun states(wedge: Wedge): String = wedge.slices.joinToString(" ") { it.state.name.lowercase() }

    // Wedges for build tasks T1, T2 and so on, each naming its requirements at its place, in the tone run.
    private fun wedgesNaming(vararg tasks: Pair<List<String>, String>): List<Wedge> = wedgesOf(
        detail(
            buildTasks = tasks.mapIndexed { index, (requirements, place) ->
                buildTask("T${index + 1}", place, "run", requirements)
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
    fun eachPlaceServeNamesPutsTheWedgeInItsPhaseWithTheSlicesBeforeItPassedAndAfterItAhead() {
        val cases = listOf(
            Triple("planned", WedgePlace.PLANNED, "ahead ahead ahead ahead"),
            Triple("red", WedgePlace.IMPLEMENTING, "now ahead ahead ahead"),
            Triple("implement", WedgePlace.IMPLEMENTING, "now ahead ahead ahead"),
            Triple("green", WedgePlace.CHECKING, "passed now ahead ahead"),
            Triple("review", WedgePlace.REVIEWING, "passed passed now ahead"),
            Triple("integrate", WedgePlace.INTEGRATING, "passed passed passed now"),
            Triple("integrated", WedgePlace.INTEGRATED, "passed passed passed passed"),
        )
        for ((place, wedgePlace, slices) in cases) {
            val wedge = wedgeOf(planOf(place, "run", emptyList()))
            assertEquals(place, wedgePlace, wedge.place)
            assertEquals(place, slices, states(wedge))
        }
    }

    @Test
    fun eachToneServeNamesIsTheWedgesTone() {
        val cases = listOf(
            "plan" to WedgeTone.PLAN,
            "run" to WedgeTone.RUN,
            "mine" to WedgeTone.MINE,
            "limited" to WedgeTone.LIMITED,
            "bad" to WedgeTone.BAD,
            "acc" to WedgeTone.ACC,
            "ok" to WedgeTone.OK,
        )
        for ((tone, wedgeTone) in cases) {
            assertEquals(tone, wedgeTone, wedgeOf(planOf("implement", tone, emptyList())).tone)
        }
    }

    @Test
    fun aPlaceOrToneServeDoesNotNameReadsAsPlannedAndPlan() {
        for (place in listOf("", "orbiting")) {
            val wedge = wedgeOf(planOf(place, "run", emptyList()))
            assertEquals(place, WedgePlace.PLANNED, wedge.place)
            assertEquals(place, "ahead ahead ahead ahead", states(wedge))
        }
        for (tone in listOf("", "glowing")) {
            assertEquals(tone, WedgeTone.PLAN, wedgeOf(planOf("green", tone, emptyList())).tone)
        }
    }

    @Test
    fun aRetryingTaskWhoseLatestStepEndedGoalUnmetWithNothingRunningIsTheRunningWedgeServeNames() {
        val wedge = wedgeOf(
            planOf(
                "implement",
                "run",
                listOf(step(stage = "turn", end = "goal_unmet", started = 100, ended = 160)),
            ),
        )
        assertEquals(WedgePlace.IMPLEMENTING, wedge.place)
        assertEquals(WedgeTone.RUN, wedge.tone)
        assertEquals("now ahead ahead ahead", states(wedge))
    }

    @Test
    fun aWedgeReadsItsPlaceAndToneFromServeWhateverItsStepsAndStatusSay() {
        val build = buildTask("T1", "integrate", "acc", listOf("R1")).copy(status = "done", stage = "review")
        val wedge = wedgeOf(detail(buildTasks = listOf(build), steps = listOf(step(stage = "checks", held = true))))
        assertEquals(WedgePlace.INTEGRATING, wedge.place)
        assertEquals(WedgeTone.ACC, wedge.tone)
        assertEquals("passed passed passed now", states(wedge))
    }

    @Test
    fun aSlicesSummaryIsItsPhasesLatestStepsSummaryDoingWhileItRunsAndEmptyWithNoStep() {
        val wedge = wedgeOf(
            planOf(
                "green",
                "run",
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
                "green",
                "run",
                listOf(
                    step(end = "done", started = 100, ended = 160),
                    step(stage = "checks", started = 170, stageStarted = 175),
                ),
            ),
        )
        assertEquals(175L, running.slices[1].since)
        assertEquals(0L, running.slices[0].since)
        assertEquals(0L, running.slices[2].since)

        val ended = wedgeOf(planOf("implement", "run", listOf(step(end = "failed", started = 300, ended = 330))))
        assertEquals(300L, ended.slices[0].since)

        val none = wedgeOf(planOf("red", "run", emptyList()))
        assertEquals(0L, none.slices[0].since)
    }

    @Test
    fun aPassedSlicesSecondsAreTheSumOfItsEndedStepsDurationsAndZeroForEveryOtherSlice() {
        val wedge = wedgeOf(
            planOf(
                "integrate",
                "acc",
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

        val failed = wedgeOf(planOf("green", "run", listOf(step(stage = "checks", end = "checks_failed", started = 300, ended = 330))))
        assertEquals(WedgePlace.CHECKING, failed.place)
        assertEquals(0L, failed.slices[1].seconds)
    }

    @Test
    fun wedgesComeFromTheBuildTasksWhenThereAreAnyAndFromTheRecordsTasksWhenThereAreNone() {
        val planned = wedgesOf(
            detail(
                buildTasks = listOf(buildTask("T1", "planned", "plan", listOf("R1"))),
                tasks = listOf(
                    EpicTaskRow(effort = 1, slug = "old", subject = "Old", status = "done", place = "integrated", tone = "ok"),
                ),
            ),
        )
        assertEquals(1, planned.size)
        assertEquals("T1", planned[0].task)
        assertEquals("Parser", planned[0].title)
        assertEquals(listOf("R1"), planned[0].requirements)

        val record = wedgesOf(
            detail(
                tasks = listOf(
                    EpicTaskRow(
                        effort = 1,
                        slug = "first",
                        subject = "First task",
                        status = "done",
                        requirements = listOf("R2", "R3"),
                        place = "integrated",
                        tone = "ok",
                    ),
                    EpicTaskRow(effort = 1, slug = "second", subject = "Second", status = "pending", place = "planned", tone = "plan"),
                ),
            ),
        )
        assertEquals(2, record.size)
        assertEquals("first", record[0].task)
        assertEquals("First task", record[0].title)
        assertEquals(listOf("R2", "R3"), record[0].requirements)
        assertEquals(WedgePlace.INTEGRATED, record[0].place)
        assertEquals(WedgeTone.OK, record[0].tone)
        assertEquals(WedgePlace.PLANNED, record[1].place)
        assertEquals(WedgeTone.PLAN, record[1].tone)
    }

    @Test
    fun aWedgesSlicesReadOnlyTheStepsLabelledWithItsId() {
        val wedge = wedgeOf(planOf("implement", "run", listOf(step(task = "T2", doing = "another task's work", stageStarted = 150))))
        assertEquals("", wedge.slices[0].summary)
        assertEquals(0L, wedge.slices[0].since)
    }

    @Test
    fun aWedgesRequirementsSplitItsSpanAndAPartContinuesThePreviousArcOfItsRequirement() {
        val arcs = requirementArcs(
            wedgesNaming(listOf("R1", "R2") to "implement", listOf("R2") to "implement", listOf("R3") to "implement"),
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
            wedgesNaming(listOf("R1") to "implement", emptyList<String>() to "implement", listOf("R1") to "implement"),
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
            wedgesNaming(listOf("R1") to "implement", listOf("R2") to "implement", listOf("R3") to "implement"),
            requirements,
        )
        assertEquals("R1:true R2:false R3:false", arcs.joinToString(" ") { "${it.id}:${it.proven}" })
    }

    @Test
    fun anArcWithNoMatchingRequirementIsProvenWhenEveryWedgeNamingItIsIntegrated() {
        val allIntegrated = requirementArcs(
            wedgesNaming(listOf("R9") to "integrated", listOf("R9") to "integrated"),
            emptyList(),
        )
        assertEquals(1, allIntegrated.size)
        assertEquals(true, allIntegrated[0].proven)

        val oneOpen = requirementArcs(
            wedgesNaming(listOf("R9") to "integrated", listOf("R9") to "integrate"),
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
