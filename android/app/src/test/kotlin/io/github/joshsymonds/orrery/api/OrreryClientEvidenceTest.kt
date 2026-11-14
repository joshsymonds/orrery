package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import com.google.protobuf.ByteString
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifact
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifactRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifactResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceRun
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class OrreryClientEvidenceTest {
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

    private val reportProto = EvidenceArtifact.newBuilder()
        .setPath("example/report.json")
        .setState("over_cap")
        .setSize(9_000_000L)
        .setMediaType("application/json")
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
        .setRevision("example-revision-1")
        .setCreated(4102444100L)
        .setEnded(4102444200L)
        .setLogBytes(4096L)
        .addArtifacts(screenshotProto)
        .addArtifacts(reportProto)
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

    private val requirementProto = EpicRequirement.newBuilder()
        .setId("R1")
        .setText("Example requirement one.")
        .setEvidence("example check one")
        .setKind("check")
        .setConclusion("example conclusion one")
        .addRuns(redRunProto)
        .addRuns(greenRunProto)
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
        revision = "example-revision-1",
        created = 4102444100L,
        ended = 4102444200L,
        logBytes = 4096L,
        artifacts = listOf(
            EvidenceArtifactRow(path = "example/screenshot.png", state = "kept", size = 2048L, mediaType = "image/png"),
            EvidenceArtifactRow(
                path = "example/report.json",
                state = "over_cap",
                size = 9_000_000L,
                mediaType = "application/json",
            ),
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
        artifacts = emptyList(),
    )

    @Test
    fun epicMapsEveryRequirementConclusionAndRunInOrderWithItsArtifacts() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto)
                    .addRequirements(requirementProto)
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
                        conclusion = "example conclusion one",
                        runs = listOf(redRunRow, greenRunRow),
                    ),
                ),
                detail?.requirements,
            )
        }
    }

    @Test
    fun artifactReturnsTheBytesAndMediaTypeAndSendsTheRunAndName() {
        FakeOrreryServer().use { server ->
            val bytes = byteArrayOf(0x89.toByte(), 0x50, 0x4E, 0x47)
            server.respond(
                EVIDENCE_ARTIFACT,
                EvidenceArtifactResponse.newBuilder()
                    .setData(ByteString.copyFrom(bytes))
                    .setMediaType("image/png")
                    .build(),
            )

            val file = requireNotNull(OrreryClient(server.baseUrl).artifact("example-run-1", "example/screenshot.png"))

            assertArrayEquals(bytes, file.data)
            assertEquals("image/png", file.mediaType)
            val request = server.requests.single()
            assertConnectRequest(EVIDENCE_ARTIFACT, request)
            val sent = EvidenceArtifactRequest.parseFrom(request.body)
            assertEquals("example-run-1", sent.run)
            assertEquals("example/screenshot.png", sent.name)
        }
    }

    @Test
    fun logIsFetchedWithAnEmptyName() {
        FakeOrreryServer().use { server ->
            server.respond(
                EVIDENCE_ARTIFACT,
                EvidenceArtifactResponse.newBuilder()
                    .setData(ByteString.copyFromUtf8("example log line\n"))
                    .setMediaType("text/plain")
                    .build(),
            )

            val file = requireNotNull(OrreryClient(server.baseUrl).artifact("example-run-1", ""))

            assertEquals("example log line\n", String(file.data, Charsets.UTF_8))
            assertEquals("text/plain", file.mediaType)
            val sent = EvidenceArtifactRequest.parseFrom(server.requests.single().body)
            assertEquals("example-run-1", sent.run)
            assertEquals("", sent.name)
        }
    }

    @Test
    fun artifactNotFoundIsNull() {
        FakeOrreryServer().use { server ->
            server.fail(EVIDENCE_ARTIFACT, Code.NOT_FOUND, "not_found")

            assertNull(OrreryClient(server.baseUrl).artifact("example-run-1", "example/missing.png"))
        }
    }

    private fun assertConnectRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EVIDENCE_ARTIFACT = "/orrery.v1.Evidence/Artifact"
    }
}
