package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepCheck
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
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
            StepCheckRow(round = 1, name = "example-check", ok = true),
            StepCheckRow(round = 2, name = "other-check", ok = false),
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

    @Test
    fun epicsMapsEveryFieldIncludingRunningStepsWithHeldAndChecks() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_LIST,
                EpicsListResponse.newBuilder().addEpics(epicProto).build(),
            )

            val epics = OrreryClient(server.baseUrl).epics()

            assertEquals(listOf(epicRow), epics)
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
                .addChecks(EpicStepCheck.newBuilder().setRound(1).setName("example-check").setOk(false))
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
                            checks = listOf(StepCheckRow(round = 1, name = "example-check", ok = false)),
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
    }
}
