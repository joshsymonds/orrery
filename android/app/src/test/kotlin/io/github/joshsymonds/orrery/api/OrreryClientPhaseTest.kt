package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicBuildTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepCheck
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepVerdict
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTaskFinding
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTaskReview
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import org.junit.Assert.assertEquals
import org.junit.Test

class OrreryClientPhaseTest {
    @Test
    fun epicKeepsEachCheckRoundsDetailAndTails() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(Epic.newBuilder().setId(EPIC_ID))
                    .addSteps(
                        EpicStep.newBuilder()
                            .setId("example-step-1")
                            .addChecks(
                                EpicStepCheck.newBuilder()
                                    .setRound(1)
                                    .setName("example-check")
                                    .setOk(false)
                                    .setDetail("example detail one")
                                    .setTailStdout("example tail stdout one")
                                    .setTailStderr("example tail stderr one"),
                            )
                            .addChecks(
                                EpicStepCheck.newBuilder()
                                    .setRound(2)
                                    .setName("example-check")
                                    .setOk(true)
                                    .setDetail("example detail two")
                                    .setTailStdout("example tail stdout two")
                                    .setTailStderr("example tail stderr two"),
                            ),
                    )
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic(EPIC_ID)

            assertEquals(
                stepRow(
                    checks = listOf(
                        StepCheckRow(
                            round = 1,
                            name = "example-check",
                            ok = false,
                            stdout = "",
                            stderr = "",
                            detail = "example detail one",
                            tailStdout = "example tail stdout one",
                            tailStderr = "example tail stderr one",
                        ),
                        StepCheckRow(
                            round = 2,
                            name = "example-check",
                            ok = true,
                            stdout = "",
                            stderr = "",
                            detail = "example detail two",
                            tailStdout = "example tail stdout two",
                            tailStderr = "example tail stderr two",
                        ),
                    ),
                ),
                detail?.steps?.single(),
            )
        }
    }

    @Test
    fun epicKeepsEachStepsGateNameAndVerdictsInOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(Epic.newBuilder().setId(EPIC_ID))
                    .addSteps(
                        EpicStep.newBuilder()
                            .setId("example-step-1")
                            .setGateName("example-gate")
                            .addVerdicts(
                                EpicStepVerdict.newBuilder()
                                    .setRound(1)
                                    .setVerdict("unmet")
                                    .setReason("example reason one"),
                            )
                            .addVerdicts(
                                EpicStepVerdict.newBuilder()
                                    .setRound(2)
                                    .setVerdict("met")
                                    .setReason("example reason two"),
                            ),
                    )
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic(EPIC_ID)

            assertEquals(
                stepRow(
                    gateName = "example-gate",
                    verdicts = listOf(
                        StepVerdictRow(round = 1, verdict = "unmet", reason = "example reason one"),
                        StepVerdictRow(round = 2, verdict = "met", reason = "example reason two"),
                    ),
                ),
                detail?.steps?.single(),
            )
        }
    }

    @Test
    fun epicKeepsEachBuildTasksReviewsAndTheirFindingsInOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(Epic.newBuilder().setId(EPIC_ID))
                    .addBuildTasks(
                        EpicBuildTask.newBuilder()
                            .setId("T1")
                            .setTitle("Example build task one.")
                            .setStatus("open")
                            .setStage("review")
                            .addReviews(
                                EpicTaskReview.newBuilder()
                                    .setAttempt(1L)
                                    .setGate("example-review-gate")
                                    .setStep("example-review-step-1")
                                    .setVerdict("fails")
                                    .addFindings(
                                        EpicTaskFinding.newBuilder()
                                            .setId("F1")
                                            .setClaim("example claim one")
                                            .setLocation("example/file-one.kt:10")
                                            .setStep("example-verify-step-1")
                                            .setResult("confirmed")
                                            .setReason("example reason one"),
                                    )
                                    .addFindings(
                                        EpicTaskFinding.newBuilder()
                                            .setId("F2")
                                            .setClaim("example claim two")
                                            .setLocation("example/file-two.kt:20")
                                            .setStep("example-verify-step-2")
                                            .setResult("dropped")
                                            .setReason("example reason two"),
                                    ),
                            )
                            .addReviews(
                                EpicTaskReview.newBuilder()
                                    .setAttempt(2L)
                                    .setGate("example-review-gate")
                                    .setStep("example-review-step-3")
                                    .setVerdict("holds")
                                    .addFindings(
                                        EpicTaskFinding.newBuilder()
                                            .setId("F3")
                                            .setClaim("example claim three")
                                            .setLocation("example/file-three.kt:30")
                                            .setStep("example-verify-step-4")
                                            .setResult("undecided")
                                            .setReason("example reason three"),
                                    ),
                            )
                            .addReviews(
                                EpicTaskReview.newBuilder()
                                    .setAttempt(2L)
                                    .setGate("example-other-gate")
                                    .setStep("example-review-step-5")
                                    .setVerdict("undetermined")
                                    .setReason("example undetermined reason"),
                            ),
                    )
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic(EPIC_ID)

            assertEquals(
                EpicBuildTaskRow(
                    id = "T1",
                    title = "Example build task one.",
                    status = "open",
                    stage = "review",
                    red = null,
                    green = null,
                    reviews = listOf(
                        TaskReviewRow(
                            attempt = 1L,
                            gate = "example-review-gate",
                            step = "example-review-step-1",
                            verdict = "fails",
                            findings = listOf(
                                TaskFindingRow(
                                    id = "F1",
                                    claim = "example claim one",
                                    location = "example/file-one.kt:10",
                                    step = "example-verify-step-1",
                                    result = "confirmed",
                                    reason = "example reason one",
                                ),
                                TaskFindingRow(
                                    id = "F2",
                                    claim = "example claim two",
                                    location = "example/file-two.kt:20",
                                    step = "example-verify-step-2",
                                    result = "dropped",
                                    reason = "example reason two",
                                ),
                            ),
                        ),
                        TaskReviewRow(
                            attempt = 2L,
                            gate = "example-review-gate",
                            step = "example-review-step-3",
                            verdict = "holds",
                            findings = listOf(
                                TaskFindingRow(
                                    id = "F3",
                                    claim = "example claim three",
                                    location = "example/file-three.kt:30",
                                    step = "example-verify-step-4",
                                    result = "undecided",
                                    reason = "example reason three",
                                ),
                            ),
                        ),
                        TaskReviewRow(
                            attempt = 2L,
                            gate = "example-other-gate",
                            step = "example-review-step-5",
                            verdict = "undetermined",
                            findings = emptyList(),
                            reason = "example undetermined reason",
                        ),
                    ),
                ),
                detail?.buildTasks?.single(),
            )
        }
    }

    @Test
    fun epicMapsEmptyPhaseFieldsWhenAStepAndBuildTaskCarryNone() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(Epic.newBuilder().setId(EPIC_ID))
                    .addSteps(EpicStep.newBuilder().setId("example-step-1"))
                    .addBuildTasks(EpicBuildTask.newBuilder().setId("T1"))
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic(EPIC_ID)

            assertEquals(stepRow(), detail?.steps?.single())
            assertEquals(
                EpicBuildTaskRow(
                    id = "T1",
                    title = "",
                    status = "",
                    stage = "",
                    red = null,
                    green = null,
                    reviews = emptyList(),
                ),
                detail?.buildTasks?.single(),
            )
        }
    }

    private fun stepRow(
        checks: List<StepCheckRow> = emptyList(),
        gateName: String = "",
        verdicts: List<StepVerdictRow> = emptyList(),
    ) = StepRow(
        id = "example-step-1",
        task = "",
        agent = "",
        end = "",
        status = "",
        held = false,
        started = 0L,
        ended = 0L,
        doing = "",
        checks = checks,
        reason = "",
        gateName = gateName,
        verdicts = verdicts,
    )

    private companion object {
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPIC_ID = "example-repo/example-epic"
    }
}
