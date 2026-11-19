package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicReleaseProofRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

private fun epic(id: String, slug: String, steering: String = "", live: Boolean = true) = EpicRow(
    id = id,
    repository = "repo-a",
    slug = slug,
    outcome = "",
    changed = 0L,
    live = live,
    runningSteps = emptyList(),
    endedSteps = 0,
    problem = "",
    steering = steering,
)

private fun detail(
    epic: EpicRow,
    questions: List<EpicQuestionRow> = emptyList(),
    parked: String = "",
    releaseProof: List<EpicReleaseProofRow> = emptyList(),
    steps: List<StepRow> = emptyList(),
) = EpicDetail(
    epic = epic,
    tasks = emptyList(),
    slices = emptyList(),
    steps = steps,
    questions = questions,
    parked = parked,
    releaseProof = releaseProof,
)

private fun question(id: String, text: String) = EpicQuestionRow(id = id, text = text, turn = 0L, asked = 0L)

private fun failedRelease(position: Long, step: String, reason: String) = EpicReleaseProofRow(
    position = position,
    step = step,
    target = "",
    undo = "",
    state = "failed",
    evidence = "",
    reason = reason,
    attempt = 0L,
    at = 0L,
)

private fun heldStep(task: String) = StepRow(
    id = "S1",
    task = task,
    agent = "",
    end = "",
    status = "",
    held = true,
    started = 0L,
    ended = 0L,
    doing = "",
    checks = emptyList(),
    reason = "",
)

class BandLinesTest {
    private val rota = epic("repo-a/rota", "rota", steering = "conv-1")
    private val orbit = epic("repo-a/orbit", "orbit", steering = "conv-1")

    @Test
    fun steeredEpicsKeepsListingOrderAndOnlyLiveEpicsWhoseSteeringIsTheId() {
        val listed = listOf(
            orbit,
            epic("repo-a/other", "other", steering = "conv-2"),
            epic("repo-a/dead", "dead", steering = "conv-1", live = false),
            epic("repo-a/free", "free"),
            rota,
        )
        assertEquals(listOf(orbit, rota), steeredEpics("conv-1", listed))
    }

    @Test
    fun steeredEpicsNeverKeepAnotherIdsEpic() {
        val listed = listOf(epic("repo-a/other", "other", steering = "conv-2"))
        assertEquals(emptyList<EpicRow>(), steeredEpics("conv-1", listed))
    }

    @Test
    fun steeredEpicsWithAnEmptyIdKeepsNone() {
        val listed = listOf(epic("repo-a/free", "free", steering = ""), rota)
        assertEquals(emptyList<EpicRow>(), steeredEpics("", listed))
    }

    @Test
    fun askLinesGivesAQuestionAnswerWithItsTextAfterTheSlug() {
        val lines = askLines(
            listOf(rota),
            mapOf(rota.id to detail(rota, questions = listOf(question("q1", "Bump the tz database?")))),
        )
        assertEquals(listOf(AskLine(rota.id, "rota", "asks: Bump the tz database?", AskWay.ANSWER)), lines)
    }

    @Test
    fun askLinesGivesAParkDecideEvenWithAQuestion() {
        val lines = askLines(
            listOf(rota),
            mapOf(rota.id to detail(rota, questions = listOf(question("q1", "Bump?")), parked = "R5 is already met")),
        )
        assertEquals(listOf(AskLine(rota.id, "rota", "is parked: R5 is already met", AskWay.DECIDE)), lines)
    }

    @Test
    fun askLinesGivesAFailedReleaseStepOpenWithItsTextLessTheSlug() {
        val lines = askLines(
            listOf(rota),
            mapOf(rota.id to detail(rota, releaseProof = listOf(failedRelease(2L, "CI green on the tag", "timeout")))),
        )
        assertEquals(
            listOf(AskLine(rota.id, "rota", "release step 2 (CI green on the tag) failed: timeout", AskWay.OPEN)),
            lines,
        )
    }

    @Test
    fun askLinesGivesAnotherBlockerOpenWithItsTextLessTheSlug() {
        val lines = askLines(listOf(rota), mapOf(rota.id to detail(rota, steps = listOf(heldStep("Build the image")))))
        assertEquals(listOf(AskLine(rota.id, "rota", "Build the image held", AskWay.OPEN)), lines)
    }

    @Test
    fun askLinesGivesNoLineWithNoNeeds() {
        assertTrue(askLines(listOf(rota), mapOf(rota.id to detail(rota))).isEmpty())
    }
}
