package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAddition
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAlternative
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicBuildTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicFailure
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicPremise
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicSlice
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepCheck
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifact
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceRun
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsHoldRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsHoldResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientEpicsTest {
    private val runningStepProto = EpicStep.newBuilder()
        .setId("example-step-1")
        .setTask("example-task")
        .setAgent("example-agent")
        .setEnd("")
        .setStatus("working")
        .setHeld(true)
        .setStarted(4102444700L)
        .setDoing("Example doing.")
        .addChecks(EpicStepCheck.newBuilder().setRound(1).setName("example-check").setOk(true))
        .addChecks(EpicStepCheck.newBuilder().setRound(2).setName("other-check").setOk(false))
        .setReason("")
        .build()

    private val runningStepRow = StepRow(
        id = "example-step-1",
        task = "example-task",
        agent = "example-agent",
        end = "",
        status = "working",
        held = true,
        started = 4102444700L,
        ended = 0L,
        doing = "Example doing.",
        checks = listOf(
            StepCheckRow(round = 1, name = "example-check", ok = true, stdout = "", stderr = ""),
            StepCheckRow(round = 2, name = "other-check", ok = false, stdout = "", stderr = ""),
        ),
        reason = "",
    )

    private val epicProto = Epic.newBuilder()
        .setId("example-repo/example-epic")
        .setRepository("example-repo")
        .setSlug("example-epic")
        .setOutcome("in progress")
        .setChanged(4102444800L)
        .setLive(true)
        .addRunningSteps(runningStepProto)
        .setEndedSteps(3)
        .setProblem("efforts/1/state.json: does not parse")
        .build()

    private val epicRow = EpicRow(
        id = "example-repo/example-epic",
        repository = "example-repo",
        slug = "example-epic",
        outcome = "in progress",
        changed = 4102444800L,
        live = true,
        runningSteps = listOf(runningStepRow),
        endedSteps = 3,
        problem = "efforts/1/state.json: does not parse",
    )

    private val exampleContractProto = EpicContract.newBuilder()
        .setTitle("Example epic title")
        .setApproved("Example approval sentence.")
        .setIntent("Example intent.")
        .addFailures(
            EpicFailure.newBuilder()
                .setId("F1")
                .setText("Example failure one.")
                .setRating("serious")
                .setResponse("Example response one."),
        )
        .addFailures(
            EpicFailure.newBuilder()
                .setId("F2")
                .setText("Example failure two.")
                .setRating("limited")
                .setResponse("Example response two."),
        )
        .setCare("serious")
        .addAdditions(
            EpicAddition.newBuilder()
                .setLocation("example location")
                .setWhat("Example addition.")
                .setWhy("Example why.")
                .setCost("Example cost."),
        )
        .addPremises(
            EpicPremise.newBuilder()
                .setId("P1")
                .setText("Example premise.")
                .setIfWrong("Example consequence."),
        )
        .addExclusions("Example exclusion one.")
        .addExclusions("Example exclusion two.")
        .setApproach("Example approach.")
        .addAlternatives(
            EpicAlternative.newBuilder()
                .setAlternative("Example alternative.")
                .setWhyNot("Example reason not.")
                .setReconsider("Example reconsider."),
        )
        .addReleaseSteps(
            EpicReleaseStep.newBuilder()
                .setStep("Example release step one.")
                .setTarget("example target one")
                .setUndo("Example undo one."),
        )
        .addReleaseSteps(
            EpicReleaseStep.newBuilder()
                .setStep("Example release step two.")
                .setTarget("example target two")
                .setUndo("Example undo two."),
        )
        .setReleaseThen("Example then line.")
        .setChecks("example checks")
        .setBase(BUILD_REVISION)
        .setAccepted(4102444600L)
        .build()

    private val exampleContractRow = EpicContractRow(
        title = "Example epic title",
        approved = "Example approval sentence.",
        intent = "Example intent.",
        failures = listOf(
            EpicFailureRow(
                id = "F1",
                text = "Example failure one.",
                rating = "serious",
                response = "Example response one.",
            ),
            EpicFailureRow(
                id = "F2",
                text = "Example failure two.",
                rating = "limited",
                response = "Example response two.",
            ),
        ),
        care = "serious",
        additions = listOf(
            EpicAdditionRow(
                location = "example location",
                what = "Example addition.",
                why = "Example why.",
                cost = "Example cost.",
            ),
        ),
        premises = listOf(EpicPremiseRow(id = "P1", text = "Example premise.", ifWrong = "Example consequence.")),
        exclusions = listOf("Example exclusion one.", "Example exclusion two."),
        approach = "Example approach.",
        alternatives = listOf(
            EpicAlternativeRow(
                alternative = "Example alternative.",
                whyNot = "Example reason not.",
                reconsider = "Example reconsider.",
            ),
        ),
        releaseSteps = listOf(
            EpicReleaseStepRow(
                step = "Example release step one.",
                target = "example target one",
                undo = "Example undo one.",
            ),
            EpicReleaseStepRow(
                step = "Example release step two.",
                target = "example target two",
                undo = "Example undo two.",
            ),
        ),
        releaseThen = "Example then line.",
        checks = "example checks",
        base = BUILD_REVISION,
        accepted = 4102444600L,
    )

    @Test
    fun epicsMapsEveryFieldIncludingRunningStepsWithHeldAndChecks() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_LIST,
                EpicsListResponse.newBuilder().addEpics(epicProto.toBuilder().setWaiting(true)).build(),
            )

            val epics = OrreryClient(server.baseUrl).epics()

            assertEquals(listOf(epicRow.copy(waiting = true)), epics)
            assertConnectRequest(EPICS_LIST, server.requests.single())
        }
    }

    @Test
    fun epicSendsTheIdAndMapsTasksAndStepsAsSent() {
        FakeOrreryServer().use { server ->
            val endedStepProto = EpicStep.newBuilder()
                .setId("example-step-2")
                .setTask("")
                .setAgent("other-agent")
                .setEnd("checks_failed")
                .setStatus("ended")
                .setHeld(false)
                .setStarted(4102444000L)
                .setEnded(4102444600L)
                .setDoing("")
                .addChecks(
                    EpicStepCheck.newBuilder()
                        .setRound(1)
                        .setName("example-check")
                        .setOk(false)
                        .setStdout("example stdout tail")
                        .setStderr("example stderr tail"),
                )
                .setReason("example reason")
                .build()
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto)
                    .addTasks(
                        EpicTask.newBuilder()
                            .setEffort(1)
                            .setSlug("example-task")
                            .setSubject("Example subject")
                            .setStatus("DONE?  as written "),
                    )
                    .addSteps(runningStepProto)
                    .addSteps(endedStepProto)
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals(
                EpicDetail(
                    epic = epicRow,
                    tasks = listOf(
                        EpicTaskRow(
                            effort = 1,
                            slug = "example-task",
                            subject = "Example subject",
                            status = "DONE?  as written ",
                        ),
                    ),
                    slices = emptyList(),
                    steps = listOf(
                        runningStepRow,
                        StepRow(
                            id = "example-step-2",
                            task = "",
                            agent = "other-agent",
                            end = "checks_failed",
                            status = "ended",
                            held = false,
                            started = 4102444000L,
                            ended = 4102444600L,
                            doing = "",
                            checks = listOf(
                                StepCheckRow(
                                    round = 1,
                                    name = "example-check",
                                    ok = false,
                                    stdout = "example stdout tail",
                                    stderr = "example stderr tail",
                                ),
                            ),
                            reason = "example reason",
                        ),
                    ),
                ),
                detail,
            )
            val request = server.requests.single { it.rawPath == EPICS_GET }
            assertConnectRequest(EPICS_GET, request)
            assertEquals("example-repo/example-epic", EpicsGetRequest.parseFrom(request.body).id)
        }
    }

    @Test
    fun epicMapsEverySliceFieldInServeOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto)
                    .addSlices(
                        EpicSlice.newBuilder()
                            .setTask("example-task-a")
                            .setHue("dispatched")
                            .setRedTest(true)
                            .setWorking(true)
                            .setTestsPass(true)
                            .setChecks(true)
                            .setGateAccepted(false)
                            .setAt(4)
                            .setHeld(false)
                            .setErrorAt(0),
                    )
                    .addSlices(
                        EpicSlice.newBuilder()
                            .setTask("example-task-b")
                            .setHue("gap")
                            .setWorking(true)
                            .setTestsPass(true)
                            .setAt(0)
                            .setHeld(true)
                            .setErrorAt(3),
                    )
                    .addSlices(
                        EpicSlice.newBuilder()
                            .setTask("example-task-c")
                            .setHue("done")
                            .setRedTest(false)
                            .setWorking(true)
                            .setTestsPass(false)
                            .setChecks(true)
                            .setGateAccepted(true)
                            .setAt(0)
                            .setHeld(false)
                            .setErrorAt(4),
                    )
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals(
                listOf(
                    EpicSliceRow(
                        task = "example-task-a",
                        hue = "dispatched",
                        redTest = true,
                        working = true,
                        testsPass = true,
                        checks = true,
                        gateAccepted = false,
                        at = 4,
                        held = false,
                        errorAt = 0,
                    ),
                    EpicSliceRow(
                        task = "example-task-b",
                        hue = "gap",
                        redTest = false,
                        working = true,
                        testsPass = true,
                        checks = false,
                        gateAccepted = false,
                        at = 0,
                        held = true,
                        errorAt = 3,
                    ),
                    EpicSliceRow(
                        task = "example-task-c",
                        hue = "done",
                        redTest = false,
                        working = true,
                        testsPass = false,
                        checks = true,
                        gateAccepted = true,
                        at = 0,
                        held = false,
                        errorAt = 4,
                    ),
                ),
                detail?.slices,
            )
        }
    }

    @Test
    fun epicMapsEveryRequirementFieldInContractOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto.toBuilder().setSource("orreryd"))
                    .addRequirements(
                        EpicRequirement.newBuilder()
                            .setId("R1")
                            .setText("Example requirement one.")
                            .setEvidence("example check one")
                            .setKind("check"),
                    )
                    .addRequirements(
                        EpicRequirement.newBuilder()
                            .setId("R2")
                            .setText("Example requirement two.")
                            .setEvidence("example review two")
                            .setKind("review"),
                    )
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals(
                listOf(
                    EpicRequirementRow(
                        id = "R1",
                        text = "Example requirement one.",
                        evidence = "example check one",
                        kind = "check",
                    ),
                    EpicRequirementRow(
                        id = "R2",
                        text = "Example requirement two.",
                        evidence = "example review two",
                        kind = "review",
                    ),
                ),
                detail?.requirements,
            )
        }
    }

    @Test
    fun epicMapsEveryBuildTaskInPlanOrderWithItsRedAndGreenRunsAndTheirKeptArtifacts() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto.toBuilder().setSource("orreryd"))
                    .addBuildTasks(
                        EpicBuildTask.newBuilder()
                            .setId("T1")
                            .setTitle("Example build task one.")
                            .setStatus("done")
                            .setStage("green")
                            .setRed(buildRunProto("example-run-1", "red", "example/red-screen.png"))
                            .setGreen(buildRunProto("example-run-2", "green", "example/green-screen.png")),
                    )
                    .addBuildTasks(
                        EpicBuildTask.newBuilder()
                            .setId("T2")
                            .setTitle("Example build task two.")
                            .setStatus("open")
                            .setStage("implement")
                            .setRed(buildRunProto("example-run-3", "red", "example/second-red-screen.png"))
                            .setGreen(buildRunProto("example-run-4", "green", "example/second-green-screen.png")),
                    )
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals(
                listOf(
                    EpicBuildTaskRow(
                        id = "T1",
                        title = "Example build task one.",
                        status = "done",
                        stage = "green",
                        red = buildRunRow("example-run-1", "red", "example/red-screen.png"),
                        green = buildRunRow("example-run-2", "green", "example/green-screen.png"),
                    ),
                    EpicBuildTaskRow(
                        id = "T2",
                        title = "Example build task two.",
                        status = "open",
                        stage = "implement",
                        red = buildRunRow("example-run-3", "red", "example/second-red-screen.png"),
                        green = buildRunRow("example-run-4", "green", "example/second-green-screen.png"),
                    ),
                ),
                detail?.buildTasks,
            )
        }
    }

    @Test
    fun aBuildTaskThatNamesNoRunMapsNoRuns() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto.toBuilder().setSource("orreryd"))
                    .addBuildTasks(
                        EpicBuildTask.newBuilder()
                            .setId("T1")
                            .setTitle("Example build task one.")
                            .setStatus("open"),
                    )
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals(
                listOf(
                    EpicBuildTaskRow(
                        id = "T1",
                        title = "Example build task one.",
                        status = "open",
                        stage = "",
                        red = null,
                        green = null,
                    ),
                ),
                detail?.buildTasks,
            )
        }
    }

    @Test
    fun recordEpicMapsNoRequirementsAndItsSource() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto.toBuilder().setSource("record"))
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals(emptyList<EpicRequirementRow>(), detail?.requirements)
            assertEquals("record", detail?.epic?.source)
            assertNull(detail?.contract)
        }
    }

    @Test
    fun epicsAndEpicMapEachEpicsSource() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_LIST,
                EpicsListResponse.newBuilder()
                    .addEpics(epicProto.toBuilder().setSource("orreryd"))
                    .addEpics(
                        epicProto.toBuilder()
                            .setId("example-repo/example-record")
                            .setSlug("example-record")
                            .setSource("record"),
                    )
                    .build(),
            )
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto.toBuilder().setSource("orreryd"))
                    .build(),
            )
            val client = OrreryClient(server.baseUrl)

            assertEquals(
                listOf("orreryd", "record"),
                client.epics().map { it.source },
            )
            assertEquals("orreryd", client.epic("example-repo/example-epic")?.epic?.source)
        }
    }

    @Test
    fun epicMapsSummaryContractQuestionsPauseAndParkWithEveryContractSectionInOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto.toBuilder().setSource("orreryd"))
                    .setSummary("Example summary paragraph.")
                    .addQuestions(
                        EpicQuestion.newBuilder()
                            .setId("Q1")
                            .setText("Example question one?")
                            .setTurn(2L)
                            .setAsked(4102444700L),
                    )
                    .addQuestions(
                        EpicQuestion.newBuilder()
                            .setId("Q2")
                            .setText("Example question two?")
                            .setTurn(3L)
                            .setAsked(4102444800L),
                    )
                    .setPaused(true)
                    .setParked("example park reason")
                    .setContract(exampleContractProto)
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals("Example summary paragraph.", detail?.summary)
            assertEquals(
                listOf(
                    EpicQuestionRow(id = "Q1", text = "Example question one?", turn = 2L, asked = 4102444700L),
                    EpicQuestionRow(id = "Q2", text = "Example question two?", turn = 3L, asked = 4102444800L),
                ),
                detail?.questions,
            )
            assertEquals(true, detail?.paused)
            assertEquals("example park reason", detail?.parked)
            assertEquals(exampleContractRow, detail?.contract)
        }
    }

    @Test
    fun epicMapsEveryStepStageLimitReadOnlyRoleAndSummary() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto)
                    .addSteps(
                        EpicStep.newBuilder()
                            .setId("example-step-3")
                            .setEnd("")
                            .setStatus("working")
                            .setStarted(4102444700L)
                            .setStage("gate")
                            .setStageStarted(4102444750L)
                            .setLimited(true)
                            .setLimitKind("weekly")
                            .setReadOnly(true)
                            .setRole("reviewer")
                            .setSummary("Example step summary."),
                    )
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals(
                StepRow(
                    id = "example-step-3",
                    task = "",
                    agent = "",
                    end = "",
                    status = "working",
                    held = false,
                    started = 4102444700L,
                    ended = 0L,
                    doing = "",
                    checks = emptyList(),
                    reason = "",
                    stage = "gate",
                    stageStarted = 4102444750L,
                    limited = true,
                    limitKind = "weekly",
                    readOnly = true,
                    role = "reviewer",
                    summary = "Example step summary.",
                ),
                detail?.steps?.single(),
            )
        }
    }

    @Test
    fun epicMapsEveryTaskAndBuildTaskRequirementInOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto.toBuilder().setSource("orreryd"))
                    .addTasks(
                        EpicTask.newBuilder()
                            .setEffort(1)
                            .setSlug("example-task")
                            .setSubject("Example subject")
                            .setStatus("DONE")
                            .addRequirements("R1")
                            .addRequirements("R3"),
                    )
                    .addBuildTasks(
                        EpicBuildTask.newBuilder()
                            .setId("T1")
                            .setTitle("Example build task one.")
                            .setStatus("open")
                            .addRequirements("R2")
                            .addRequirements("R4"),
                    )
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals(listOf("R1", "R3"), detail?.tasks?.single()?.requirements)
            assertEquals(listOf("R2", "R4"), detail?.buildTasks?.single()?.requirements)
        }
    }

    @Test
    fun epicNotFoundIsNull() {
        FakeOrreryServer().use { server ->
            server.fail(EPICS_GET, Code.NOT_FOUND, "not_found")

            assertNull(OrreryClient(server.baseUrl).epic("example-repo/missing"))
        }
    }

    @Test
    fun holdStepSendsTheIdAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(STEPS_HOLD, StepsHoldResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).holdStep("example-step-1")

            assertEquals(StepCallResult.Landed, result)
            val request = server.requests.single()
            assertConnectRequest(STEPS_HOLD, request)
            assertEquals("example-step-1", StepsHoldRequest.parseFrom(request.body).id)
        }
    }

    @Test
    fun nudgeStepSendsTheIdAndTextAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(STEPS_NUDGE, StepsNudgeResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).nudgeStep("example-step-1", "example nudge text")

            assertEquals(StepCallResult.Landed, result)
            val request = server.requests.single()
            assertConnectRequest(STEPS_NUDGE, request)
            val sent = StepsNudgeRequest.parseFrom(request.body)
            assertEquals("example-step-1", sent.id)
            assertEquals("example nudge text", sent.text)
        }
    }

    @Test
    fun stopStepSendsTheIdAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(STEPS_STOP, StepsStopResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).stopStep("example-step-1")

            assertEquals(StepCallResult.Landed, result)
            val request = server.requests.single()
            assertConnectRequest(STEPS_STOP, request)
            assertEquals("example-step-1", StepsStopRequest.parseFrom(request.body).id)
        }
    }

    @Test
    fun failedPreconditionOnHoldNudgeAndStopIsRefusedWithTheServerMessage() {
        FakeOrreryServer().use { server ->
            server.fail(STEPS_HOLD, Code.FAILED_PRECONDITION, "example-step-1 is not running")
            server.fail(STEPS_NUDGE, Code.FAILED_PRECONDITION, "example-step-1 is held")
            server.fail(STEPS_STOP, Code.FAILED_PRECONDITION, "example-step-1 has ended")
            val client = OrreryClient(server.baseUrl)

            assertEquals(
                StepCallResult.Refused("example-step-1 is not running"),
                client.holdStep("example-step-1"),
            )
            assertEquals(
                StepCallResult.Refused("example-step-1 is held"),
                client.nudgeStep("example-step-1", "example nudge text"),
            )
            assertEquals(
                StepCallResult.Refused("example-step-1 has ended"),
                client.stopStep("example-step-1"),
            )
        }
    }

    @Test
    fun droppedHoldIsNoAnswerAndSentOnce() {
        assertDroppedWriteIsNoAnswerAndSentOnce(STEPS_HOLD) { it.holdStep("example-step-1") }
    }

    @Test
    fun droppedNudgeIsNoAnswerAndSentOnce() {
        assertDroppedWriteIsNoAnswerAndSentOnce(STEPS_NUDGE) { it.nudgeStep("example-step-1", "example nudge text") }
    }

    @Test
    fun droppedStopIsNoAnswerAndSentOnce() {
        assertDroppedWriteIsNoAnswerAndSentOnce(STEPS_STOP) { it.stopStep("example-step-1") }
    }

    @Test
    fun everyCallSendsTheClientHeader() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
            server.respond(EPICS_GET, EpicsGetResponse.getDefaultInstance())
            server.respond(STEPS_HOLD, StepsHoldResponse.getDefaultInstance())
            server.respond(STEPS_NUDGE, StepsNudgeResponse.getDefaultInstance())
            server.respond(STEPS_STOP, StepsStopResponse.getDefaultInstance())
            val client = OrreryClient(server.baseUrl)

            client.epics()
            client.epic("example-repo/example-epic")
            client.holdStep("example-step-1")
            client.nudgeStep("example-step-1", "example nudge text")
            client.stopStep("example-step-1")

            assertEquals(5, server.requests.size)
            server.requests.forEach { request -> assertConnectRequest(request.rawPath, request) }
        }
    }

    /**
     * Runs [call] after a read has left a keep-alive connection in the pool, so the write travels on
     * that pooled connection, the one a retry would resend on. The server reads the write and closes
     * the connection unanswered.
     */
    private fun assertDroppedWriteIsNoAnswerAndSentOnce(procedure: String, call: (OrreryClient) -> Unit) {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
            server.drop(procedure)
            val client = OrreryClient(server.baseUrl)
            client.epics()

            assertThrows(NoAnswerException::class.java) {
                call(client)
            }

            assertEquals(1, server.requests.count { it.rawPath == procedure })
        }
    }

    /** A passing red or green run of a build task, with one kept image artifact at [artifact]. */
    private fun buildRunProto(id: String, phase: String, artifact: String): EvidenceRun =
        EvidenceRun.newBuilder()
            .setId(id)
            .setPhase(phase)
            .setSource("captured")
            .setState("ended")
            .setExit(0)
            .setTimedOut(false)
            .setNotCaptured("")
            .setVerdict("holds")
            .setJudgeReason("")
            .setPassed(true)
            .setRevision(BUILD_REVISION)
            .setCreated(4102444700L)
            .setEnded(4102444800L)
            .setLogBytes(12L)
            .setStderrBytes(0L)
            .addArtifacts(
                EvidenceArtifact.newBuilder()
                    .setPath(artifact)
                    .setState("kept")
                    .setSize(68L)
                    .setMediaType("image/png"),
            )
            .build()

    private fun buildRunRow(id: String, phase: String, artifact: String): EvidenceRunRow =
        EvidenceRunRow(
            id = id,
            phase = phase,
            source = "captured",
            state = "ended",
            exit = 0,
            timedOut = false,
            notCaptured = "",
            verdict = "holds",
            judgeReason = "",
            passed = true,
            revision = BUILD_REVISION,
            created = 4102444700L,
            ended = 4102444800L,
            logBytes = 12L,
            stderrBytes = 0L,
            artifacts = listOf(
                EvidenceArtifactRow(path = artifact, state = "kept", size = 68L, mediaType = "image/png"),
            ),
        )

    private fun assertConnectRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val STEPS_HOLD = "/orrery.v1.Steps/Hold"
        const val STEPS_NUDGE = "/orrery.v1.Steps/Nudge"
        const val STEPS_STOP = "/orrery.v1.Steps/Stop"
        const val BUILD_REVISION = "0123456789abcdef0123456789abcdef01234567"
    }
}
