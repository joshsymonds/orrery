package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicClosure
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicLedgerFinding
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseRun
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReport
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReviewVerdict
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsCloseRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsCloseResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsReleaseRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsReleaseResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientEpicEndingTest {
    @Test
    fun epicMapsReviewsLedgerWithClosuresReleasesReportAndSupersededBy() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(Epic.newBuilder().setId("example-repo/example-epic").build())
                    .addReviews(
                        EpicReviewVerdict.newBuilder()
                            .setPosition(4)
                            .setReviewer("example-reviewer")
                            .setStep("example-step-9")
                            .setVerdict("gap")
                            .setReason("Example review reason."),
                    )
                    .addLedger(
                        EpicLedgerFinding.newBuilder()
                            .setId("example-reviewer-1")
                            .setPosition(2)
                            .setReviewer("example-reviewer")
                            .setClaim("Example claim.")
                            .setCitation("Example citation.")
                            .setLocation("Example location.")
                            .setEvidence("Example evidence.")
                            .setStep("")
                            .setResult("")
                            .setUnverified(true)
                            .setReason("Example verify reason.")
                            .addClosures(
                                EpicClosure.newBuilder()
                                    .setRound(3)
                                    .setStep("example-step-12")
                                    .setResult("closed")
                                    .setReason("Example closure reason."),
                            ),
                    )
                    .addReleases(
                        EpicReleaseRun.newBuilder()
                            .setPart("release")
                            .setPosition(2)
                            .setAttempt(3)
                            .setStep("Example release step.")
                            .setStart("example-start")
                            .setHead("example-head")
                            .setEnd("done")
                            .addCommits("example-commit-1")
                            .setEvidence("Example run evidence.")
                            .setPassed(true)
                            .setReason("Example run reason.")
                            .setAt(4102444800L),
                    )
                    .setReport(
                        EpicReport.newBuilder()
                            .setOutcome("released")
                            .addShipped("R1")
                            .addNotShipped("R2")
                            .setWhy("Example why."),
                    )
                    .setSupersededBy("example-repo/example-successor")
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")!!

            assertEquals(
                listOf(
                    EpicReviewVerdictRow(
                        position = 4L,
                        reviewer = "example-reviewer",
                        step = "example-step-9",
                        verdict = "gap",
                        reason = "Example review reason.",
                    ),
                ),
                detail.reviews,
            )
            assertEquals(
                listOf(
                    EpicLedgerFindingRow(
                        id = "example-reviewer-1",
                        position = 2L,
                        reviewer = "example-reviewer",
                        claim = "Example claim.",
                        citation = "Example citation.",
                        location = "Example location.",
                        evidence = "Example evidence.",
                        step = "",
                        result = "",
                        unverified = true,
                        reason = "Example verify reason.",
                        closures = listOf(
                            EpicClosureRow(
                                round = 3L,
                                step = "example-step-12",
                                result = "closed",
                                reason = "Example closure reason.",
                            ),
                        ),
                    ),
                ),
                detail.ledger,
            )
            assertEquals(
                listOf(
                    EpicReleaseRunRow(
                        part = "release",
                        position = 2L,
                        attempt = 3L,
                        step = "Example release step.",
                        start = "example-start",
                        head = "example-head",
                        end = "done",
                        commits = listOf("example-commit-1"),
                        evidence = "Example run evidence.",
                        passed = true,
                        reason = "Example run reason.",
                        at = 4102444800L,
                    ),
                ),
                detail.releases,
            )
            assertEquals(
                EpicReportRow(
                    outcome = "released",
                    shipped = listOf("R1"),
                    notShipped = listOf("R2"),
                    why = "Example why.",
                ),
                detail.report,
            )
            assertEquals("example-repo/example-successor", detail.supersededBy)
        }
    }

    @Test
    fun epicHasNoReportWhenTheGetResponseHasNone() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(Epic.newBuilder().setId("example-repo/example-epic").build())
                    .build(),
            )

            val detail = OrreryClient(server.baseUrl).epic("example-repo/example-epic")!!

            assertNull(detail.report)
        }
    }

    @Test
    fun releasePostsTheEpicIdAndTheUndoNoneNamedAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_RELEASE, EpicsReleaseResponse.getDefaultInstance())

            assertEquals(
                StepCallResult.Landed,
                OrreryClient(server.baseUrl).releaseEpic("example-repo/example-epic", listOf(2)),
            )

            val request = server.requests.single()
            assertConnectRequest(EPICS_RELEASE, request)
            val body = EpicsReleaseRequest.parseFrom(request.body)
            assertEquals("example-repo/example-epic", body.id)
            assertEquals(listOf(2), body.undoNoneList)
        }
    }

    @Test
    fun releaseWithNoUndoNoneNamedPostsAnEmptyList() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_RELEASE, EpicsReleaseResponse.getDefaultInstance())

            assertEquals(
                StepCallResult.Landed,
                OrreryClient(server.baseUrl).releaseEpic("example-repo/example-epic", emptyList()),
            )

            val request = server.requests.single()
            assertConnectRequest(EPICS_RELEASE, request)
            val body = EpicsReleaseRequest.parseFrom(request.body)
            assertEquals("example-repo/example-epic", body.id)
            assertEquals(emptyList<Int>(), body.undoNoneList)
        }
    }

    @Test
    fun closePostsTheEpicIdKindAndByAndLands() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_CLOSE, EpicsCloseResponse.getDefaultInstance())

            assertEquals(
                StepCallResult.Landed,
                OrreryClient(server.baseUrl).closeEpic(
                    "example-repo/example-epic",
                    "superseded",
                    "example-repo/example-successor",
                ),
            )

            val request = server.requests.single()
            assertConnectRequest(EPICS_CLOSE, request)
            val body = EpicsCloseRequest.parseFrom(request.body)
            assertEquals("example-repo/example-epic", body.id)
            assertEquals("superseded", body.kind)
            assertEquals("example-repo/example-successor", body.by)
        }
    }

    @Test
    fun aRefusedReleaseOrCloseAnswersRefusedWithTheServerMessage() {
        FakeOrreryServer().use { server ->
            server.fail(EPICS_RELEASE, Code.FAILED_PRECONDITION, "example release refusal")
            server.fail(EPICS_CLOSE, Code.FAILED_PRECONDITION, "example close refusal")
            val client = OrreryClient(server.baseUrl)

            assertEquals(
                StepCallResult.Refused("example release refusal"),
                client.releaseEpic("example-repo/example-epic", listOf(2)),
            )
            assertEquals(
                StepCallResult.Refused("example close refusal"),
                client.closeEpic("example-repo/example-epic", "shipped", ""),
            )
        }
    }

    @Test
    fun droppedReleaseIsNoAnswerAndSentOnce() {
        assertDroppedWriteIsNoAnswerAndSentOnce(EPICS_RELEASE) {
            it.releaseEpic("example-repo/example-epic", listOf(2))
        }
    }

    @Test
    fun droppedCloseIsNoAnswerAndSentOnce() {
        assertDroppedWriteIsNoAnswerAndSentOnce(EPICS_CLOSE) {
            it.closeEpic("example-repo/example-epic", "abandoned", "")
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
        const val EPICS_RELEASE = "/orrery.v1.Epics/Release"
        const val EPICS_CLOSE = "/orrery.v1.Epics/Close"
    }
}
