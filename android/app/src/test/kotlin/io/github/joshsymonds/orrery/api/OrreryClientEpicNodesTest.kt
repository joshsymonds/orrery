package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicNode
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicNodeCheck
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifact
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceRun
import org.junit.Assert.assertEquals
import org.junit.Test

class OrreryClientEpicNodesTest {
    private val epicProto = Epic.newBuilder()
        .setId("example-repo/example-epic")
        .setRepository("example-repo")
        .setSlug("example-epic")
        .setSource("orreryd")
        .build()

    private val screenshotProto = EvidenceArtifact.newBuilder()
        .setPath("example/screenshot.png")
        .setState("kept")
        .setSize(2048L)
        .setMediaType("image/png")
        .build()

    private val redRunProto = EvidenceRun.newBuilder()
        .setId("example-run-1")
        .setPhase("red")
        .setSource("captured")
        .setState("ended")
        .setExit(3)
        .setTimedOut(true)
        .setVerdict("fails")
        .setPassed(true)
        .setRevision("example-revision-2")
        .setCreated(4102444100L)
        .setEnded(4102444200L)
        .setLogBytes(4096L)
        .setStderrBytes(1024L)
        .addArtifacts(screenshotProto)
        .build()

    private val greenRunProto = EvidenceRun.newBuilder()
        .setId("example-run-2")
        .setPhase("green")
        .setSource("supplied")
        .setState("judging")
        .setExit(0)
        .setNotCaptured("lost at a restart")
        .setJudgeReason("example judge reason")
        .setPassed(false)
        .setRevision("example-revision-2")
        .setCreated(4102444300L)
        .build()

    private val redRunRow = EvidenceRunRow(
        id = "example-run-1",
        phase = "red",
        source = "captured",
        state = "ended",
        exit = 3,
        timedOut = true,
        notCaptured = "",
        verdict = "fails",
        judgeReason = "",
        passed = true,
        revision = "example-revision-2",
        created = 4102444100L,
        ended = 4102444200L,
        logBytes = 4096L,
        stderrBytes = 1024L,
        artifacts = listOf(
            EvidenceArtifactRow(path = "example/screenshot.png", state = "kept", size = 2048L, mediaType = "image/png"),
        ),
    )

    private val greenRunRow = EvidenceRunRow(
        id = "example-run-2",
        phase = "green",
        source = "supplied",
        state = "judging",
        exit = 0,
        timedOut = false,
        notCaptured = "lost at a restart",
        verdict = "",
        judgeReason = "example judge reason",
        passed = false,
        revision = "example-revision-2",
        created = 4102444300L,
        ended = 0L,
        logBytes = 0L,
        stderrBytes = 0L,
        artifacts = emptyList(),
    )

    private val planProto = EpicNode.newBuilder()
        .setPath("example-plan")
        .setNode("plan-node")
        .setKind("plan")
        .setState("ended")
        .setOutcome("complete")
        .setOutput("example plan output")
        .setRevision("example-revision-1")
        .addTurns(1L)
        .addTurns(2L)
        .addTasks(10L)
        .addTasks(11L)
        .build()

    private val planRow = EpicNodeRow(
        path = "example-plan",
        node = "plan-node",
        kind = "plan",
        state = "ended",
        outcome = "complete",
        round = 0L,
        loopRound = 0L,
        attempt = 0L,
        output = "example plan output",
        revision = "example-revision-1",
        turns = listOf(1L, 2L),
        tasks = listOf(10L, 11L),
        runs = emptyList(),
        check = null,
        step = "",
    )

    private val buildProto = EpicNode.newBuilder()
        .setPath("example-build")
        .setNode("build-node")
        .setKind("build")
        .setState("ended")
        .setOutcome("passed")
        .setRound(1L)
        .setAttempt(1L)
        .addTasks(12L)
        .build()

    private val buildRow = EpicNodeRow(
        path = "example-build",
        node = "build-node",
        kind = "build",
        state = "ended",
        outcome = "passed",
        round = 1L,
        loopRound = 0L,
        attempt = 1L,
        output = "",
        revision = "",
        turns = emptyList(),
        tasks = listOf(12L),
        runs = emptyList(),
        check = null,
        step = "",
    )

    private val gateProto = EpicNode.newBuilder()
        .setPath("example-gate")
        .setNode("gate-node")
        .setKind("gate")
        .setState("ended")
        .setOutcome("failed")
        .setRound(2L)
        .setAttempt(1L)
        .setOutput("example gate output")
        .setRevision("example-revision-1")
        .setCheck(
            EpicNodeCheck.newBuilder()
                .setState("ended")
                .setExit(1)
                .setTimedOut(true)
                .setStdout("example stdout tail")
                .setStderr("example stderr tail"),
        )
        .build()

    private val gateRow = EpicNodeRow(
        path = "example-gate",
        node = "gate-node",
        kind = "gate",
        state = "ended",
        outcome = "failed",
        round = 2L,
        loopRound = 0L,
        attempt = 1L,
        output = "example gate output",
        revision = "example-revision-1",
        turns = emptyList(),
        tasks = emptyList(),
        runs = emptyList(),
        check = EpicNodeCheckRow(
            state = "ended",
            exit = 1,
            timedOut = true,
            notRun = "",
            stdout = "example stdout tail",
            stderr = "example stderr tail",
        ),
        step = "",
    )

    private val commandProto = EpicNode.newBuilder()
        .setPath("example-command")
        .setNode("command-node")
        .setKind("command")
        .setState("ended")
        .setOutcome("failed")
        .setAttempt(2L)
        .setCheck(EpicNodeCheck.newBuilder().setState("ended").setNotRun("example not run"))
        .build()

    private val commandRow = EpicNodeRow(
        path = "example-command",
        node = "command-node",
        kind = "command",
        state = "ended",
        outcome = "failed",
        round = 0L,
        loopRound = 0L,
        attempt = 2L,
        output = "",
        revision = "",
        turns = emptyList(),
        tasks = emptyList(),
        runs = emptyList(),
        check = EpicNodeCheckRow(
            state = "ended",
            exit = 0,
            timedOut = false,
            notRun = "example not run",
            stdout = "",
            stderr = "",
        ),
        step = "",
    )

    private val evidenceProto = EpicNode.newBuilder()
        .setPath("example-evidence")
        .setNode("evidence-node")
        .setKind("evidence")
        .setState("parked")
        .setRound(1L)
        .setRevision("example-revision-2")
        .addRuns(redRunProto)
        .addRuns(greenRunProto)
        .build()

    private val evidenceRow = EpicNodeRow(
        path = "example-evidence",
        node = "evidence-node",
        kind = "evidence",
        state = "parked",
        outcome = "",
        round = 1L,
        loopRound = 0L,
        attempt = 0L,
        output = "",
        revision = "example-revision-2",
        turns = emptyList(),
        tasks = emptyList(),
        runs = listOf(redRunRow, greenRunRow),
        check = null,
        step = "",
    )

    private val ownedProto = EpicNode.newBuilder()
        .setPath("example-owned")
        .setNode("owned-node")
        .setKind("owned")
        .setState("ended")
        .setOutcome("complete")
        .setOutput("example owned output")
        .build()

    private val ownedRow = EpicNodeRow(
        path = "example-owned",
        node = "owned-node",
        kind = "owned",
        state = "ended",
        outcome = "complete",
        round = 0L,
        loopRound = 0L,
        attempt = 0L,
        output = "example owned output",
        revision = "",
        turns = emptyList(),
        tasks = emptyList(),
        runs = emptyList(),
        check = null,
        step = "",
    )

    private val loopProto = EpicNode.newBuilder()
        .setPath("example-loop")
        .setNode("loop-node")
        .setKind("loop")
        .setState("running")
        .setLoopRound(3L)
        .build()

    private val loopRow = EpicNodeRow(
        path = "example-loop",
        node = "loop-node",
        kind = "loop",
        state = "running",
        outcome = "",
        round = 0L,
        loopRound = 3L,
        attempt = 0L,
        output = "",
        revision = "",
        turns = emptyList(),
        tasks = emptyList(),
        runs = emptyList(),
        check = null,
        step = "",
    )

    private val agentProto = EpicNode.newBuilder()
        .setPath("example-agent")
        .setNode("agent-node")
        .setKind("agent")
        .setState("running")
        .setAttempt(1L)
        .setStep("example-step-3")
        .build()

    private val agentRow = EpicNodeRow(
        path = "example-agent",
        node = "agent-node",
        kind = "agent",
        state = "running",
        outcome = "",
        round = 0L,
        loopRound = 0L,
        attempt = 1L,
        output = "",
        revision = "",
        turns = emptyList(),
        tasks = emptyList(),
        runs = emptyList(),
        check = null,
        step = "example-step-3",
    )

    @Test
    fun epicMapsEveryNodeFieldInResponseOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto)
                    .addNodes(planProto)
                    .addNodes(buildProto)
                    .addNodes(gateProto)
                    .addNodes(commandProto)
                    .addNodes(evidenceProto)
                    .addNodes(ownedProto)
                    .addNodes(loopProto)
                    .addNodes(agentProto)
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals(
                listOf(planRow, buildRow, gateRow, commandRow, evidenceRow, ownedRow, loopRow, agentRow),
                detail?.nodes,
            )
        }
    }

    @Test
    fun responseWithoutNodesGivesAnEmptyList() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder().setEpic(epicProto).build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")

            assertEquals(emptyList<EpicNodeRow>(), detail?.nodes)
        }
    }

    private companion object {
        const val EPICS_GET = "/orrery.v1.Epics/Get"
    }
}
