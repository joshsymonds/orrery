package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicReleaseProofRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow
import org.junit.Assert.assertEquals
import org.junit.Test

private const val EPIC_ID = "repo-a/alpha"

private val alpha = EpicRow(
    id = EPIC_ID,
    repository = "repo-a",
    slug = "alpha",
    outcome = "",
    changed = 0L,
    live = true,
    runningSteps = emptyList(),
    endedSteps = 0,
    problem = "",
)

private fun detail(
    questions: List<EpicQuestionRow> = emptyList(),
    parked: String = "",
    releaseProof: List<EpicReleaseProofRow> = emptyList(),
    steps: List<StepRow> = emptyList(),
) = EpicDetail(
    epic = alpha,
    tasks = emptyList(),
    steps = steps,
    questions = questions,
    parked = parked,
    releaseProof = releaseProof,
)

private fun needsOf(detail: EpicDetail): List<Need> = epicNeedsOf(listOf(alpha), mapOf(EPIC_ID to detail))

private fun question(id: String, text: String) = EpicQuestionRow(id = id, text = text, turn = 0L, asked = 0L)

private fun release(position: Long, step: String, state: String, reason: String = "") = EpicReleaseProofRow(
    position = position,
    step = step,
    target = "",
    undo = "",
    state = state,
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

private fun limitedStep(task: String, kind: String) = StepRow(
    id = "S2",
    task = task,
    agent = "",
    end = "",
    status = "",
    held = false,
    started = 0L,
    ended = 0L,
    doing = "",
    checks = emptyList(),
    reason = "",
    limited = true,
    limitKind = kind,
)

class NeedsModelTest {
    @Test
    fun anOpenQuestionReadsItsSlugAskingAndCarriesTheEpicAndQuestionIds() {
        val needs = needsOf(detail(questions = listOf(question("Q1", "Use Postgres?"))))
        assertEquals(listOf(Need(NeedKind.QUESTION, "alpha asks: Use Postgres?", EPIC_ID, "Q1")), needs)
    }

    @Test
    fun twoOpenQuestionsGiveTwoNeedsInOrder() {
        val needs = needsOf(
            detail(questions = listOf(question("Q1", "Use Postgres?"), question("Q2", "Ship on Friday?"))),
        )
        assertEquals(
            listOf(
                Need(NeedKind.QUESTION, "alpha asks: Use Postgres?", EPIC_ID, "Q1"),
                Need(NeedKind.QUESTION, "alpha asks: Ship on Friday?", EPIC_ID, "Q2"),
            ),
            needs,
        )
    }

    @Test
    fun aFailedStepTwoWithAParkGivesTheReleaseNeedAndNoParkNeed() {
        val needs = needsOf(
            detail(
                parked = "budget",
                releaseProof = listOf(
                    release(1L, "tag", "passed"),
                    release(2L, "publish", "failed", "registry 503"),
                    release(3L, "announce", "waiting"),
                ),
            ),
        )
        assertEquals(
            listOf(Need(NeedKind.RELEASE, "alpha: release step 2 (publish) failed: registry 503", EPIC_ID, "")),
            needs,
        )
    }

    @Test
    fun aFailedReleaseStepGivesOneReleaseNeedForTheFirstFailure() {
        val needs = needsOf(
            detail(
                releaseProof = listOf(
                    release(1L, "tag", "failed", "tag exists"),
                    release(2L, "publish", "failed", "registry 503"),
                ),
            ),
        )
        assertEquals(listOf("alpha: release step 1 (tag) failed: tag exists"), needs.map { it.text })
    }

    @Test
    fun releaseStepsThatHaveNotFailedGiveNoReleaseNeed() {
        val needs = needsOf(
            detail(
                releaseProof = listOf(
                    release(1L, "tag", "passed"),
                    release(2L, "publish", "running"),
                    release(3L, "announce", "waiting"),
                ),
            ),
        )
        assertEquals(emptyList<Need>(), needs)
    }

    @Test
    fun aParkedEpicReadsItsParkAsAParkNeed() {
        val needs = needsOf(detail(parked = "budget"))
        assertEquals(listOf(Need(NeedKind.PARK, "alpha is parked: budget", EPIC_ID, "")), needs)
    }

    @Test
    fun aHeldStepGivesItsSlugAndTaskHeldAsAnOtherNeed() {
        val needs = needsOf(detail(steps = listOf(heldStep("web-line"))))
        assertEquals(listOf(Need(NeedKind.OTHER, "alpha: web-line held", EPIC_ID, "")), needs)
    }

    @Test
    fun aLimitedStepReadsItsOwnTextAsAnOtherNeed() {
        val needs = needsOf(detail(steps = listOf(limitedStep("build", "usage"))))
        assertEquals(listOf(Need(NeedKind.OTHER, "alpha: build waits on a usage limit", EPIC_ID, "")), needs)
    }

    @Test
    fun anOpenQuestionBesideAHeldStepGivesBothNeeds() {
        val needs = needsOf(
            detail(
                questions = listOf(question("Q1", "Use Postgres?")),
                steps = listOf(heldStep("web-line")),
            ),
        )
        assertEquals(
            listOf(
                Need(NeedKind.QUESTION, "alpha asks: Use Postgres?", EPIC_ID, "Q1"),
                Need(NeedKind.OTHER, "alpha: web-line held", EPIC_ID, ""),
            ),
            needs,
        )
    }

    @Test
    fun anEpicWithNoBlockerGivesNoNeed() {
        assertEquals(emptyList<Need>(), needsOf(detail()))
    }

    @Test
    fun anEpicThatIsNotLiveOrHasNoDetailGivesNoNeed() {
        val ended = alpha.copy(live = false, outcome = "released")
        val questioned = detail(questions = listOf(question("Q1", "Use Postgres?")))
        assertEquals(emptyList<Need>(), epicNeedsOf(listOf(ended), mapOf(EPIC_ID to questioned)))
        assertEquals(emptyList<Need>(), epicNeedsOf(listOf(alpha), emptyMap()))
    }
}
