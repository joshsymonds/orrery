package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import io.github.joshsymonds.orrery.api.EpicClosureRow
import io.github.joshsymonds.orrery.api.EpicHandoverRow
import io.github.joshsymonds.orrery.api.EpicLedgerFindingRow
import io.github.joshsymonds.orrery.api.EpicReleaseRunRow
import io.github.joshsymonds.orrery.api.EpicReviewVerdictRow
import io.github.joshsymonds.orrery.api.HandoverReleaseStepRow
import io.github.joshsymonds.orrery.api.toEpicHandoverRow
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicClosure
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicLedgerFinding
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseRun
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReviewVerdict
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The window is tall so every row is composed: the handover is a plain column, but its rows must all be in the tree.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h3000dp")
class EpicHandoverTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val EPIC_SLUG = "example-epic"
        const val CHANGED = 4_102_444_800L
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun review(
        position: Long,
        reviewer: String,
        verdict: String,
        step: String = "",
        reason: String = "",
    ): EpicReviewVerdict =
        EpicReviewVerdict.newBuilder()
            .setPosition(position)
            .setReviewer(reviewer)
            .setStep(step)
            .setVerdict(verdict)
            .setReason(reason)
            .build()

    private fun closure(round: Long, step: String, result: String, reason: String = ""): EpicClosure =
        EpicClosure.newBuilder()
            .setRound(round)
            .setStep(step)
            .setResult(result)
            .setReason(reason)
            .build()

    private fun finding(
        id: String,
        position: Long,
        reviewer: String,
        claim: String,
        citation: String = "",
        location: String = "",
        evidence: String = "",
        step: String = "",
        result: String = "",
        unverified: Boolean = false,
        reason: String = "",
        closures: List<EpicClosure> = emptyList(),
    ): EpicLedgerFinding =
        EpicLedgerFinding.newBuilder()
            .setId(id)
            .setPosition(position)
            .setReviewer(reviewer)
            .setClaim(claim)
            .setCitation(citation)
            .setLocation(location)
            .setEvidence(evidence)
            .setStep(step)
            .setResult(result)
            .setUnverified(unverified)
            .setReason(reason)
            .addAllClosures(closures)
            .build()

    private fun releaseRun(
        part: String,
        position: Long,
        attempt: Long,
        step: String,
        end: String,
        passed: Boolean,
        at: Long,
        start: String = "",
        head: String = "",
        commits: List<String> = emptyList(),
        evidence: String = "",
        reason: String = "",
    ): EpicReleaseRun =
        EpicReleaseRun.newBuilder()
            .setPart(part)
            .setPosition(position)
            .setAttempt(attempt)
            .setStep(step)
            .setStart(start)
            .setHead(head)
            .setEnd(end)
            .addAllCommits(commits)
            .setEvidence(evidence)
            .setPassed(passed)
            .setReason(reason)
            .setAt(at)
            .build()

    private fun releaseStep(step: String, target: String, undo: String): EpicReleaseStep =
        EpicReleaseStep.newBuilder().setStep(step).setTarget(target).setUndo(undo).build()

    private fun body(
        outcome: String,
        contract: EpicContract? = null,
        reviews: List<EpicReviewVerdict> = emptyList(),
        ledger: List<EpicLedgerFinding> = emptyList(),
        releases: List<EpicReleaseRun> = emptyList(),
    ): EpicsGetResponse {
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug(EPIC_SLUG)
            .setOutcome(outcome)
            .setChanged(CHANGED)
            .setLive(false)
        val builder = EpicsGetResponse.newBuilder()
            .setEpic(epic)
            .addAllReviews(reviews)
            .addAllLedger(ledger)
            .addAllReleases(releases)
        if (contract != null) builder.setContract(contract)
        return builder.build()
    }

    /** An epic that ended with a gap: two verdicts, a confirmed finding closed in two rounds, and an unverified one. */
    private fun gapsBody(): EpicsGetResponse = body(
        outcome = "ended_with_gaps",
        reviews = listOf(
            review(1L, "example-reviewer", "gap", step = "example-review-step", reason = "the claim lacks a test"),
            review(2L, "example-second", "holds"),
        ),
        ledger = listOf(
            finding(
                id = "example-reviewer-1",
                position = 1L,
                reviewer = "example-reviewer",
                claim = "the epic dropped a requirement",
                citation = "example.md:4",
                location = "internal/example.go:12",
                evidence = "example evidence",
                step = "example-verify",
                result = "confirmed",
                reason = "the requirement is absent",
                closures = listOf(
                    closure(1L, "example-close", "open", reason = "still absent"),
                    closure(2L, "example-close", "closed"),
                ),
            ),
            finding(
                id = "example-second-1",
                position = 2L,
                reviewer = "example-second",
                claim = "the test is tautological",
                citation = "example.md:9",
                unverified = true,
                reason = "no verifier ran",
            ),
        ),
    )

    /** A released epic: two contract steps, a failed then passed attempt on the first, and candidate and judge runs. */
    private fun releasedBody(): EpicsGetResponse = body(
        outcome = "released",
        contract = EpicContract.newBuilder()
            .addAllReleaseSteps(
                listOf(
                    releaseStep("example-tag", "example-tag-v1", "delete the tag"),
                    releaseStep("example-publish", "example-registry", "unpublish"),
                ),
            )
            .build(),
        releases = listOf(
            releaseRun(
                part = "release",
                position = 1L,
                attempt = 1L,
                step = "example-tag",
                end = "done",
                passed = false,
                at = 100L,
                start = "1a2b3c",
                head = "4d5e6f",
                reason = "tag exists",
            ),
            releaseRun(
                part = "release",
                position = 1L,
                attempt = 2L,
                step = "example-tag",
                end = "done",
                passed = true,
                at = 200L,
                start = "1a2b3c",
                head = "7a8b9c",
                commits = listOf("1111aaa", "2222bbb"),
                evidence = "tag v1 pushed",
            ),
            releaseRun(
                part = "release",
                position = 2L,
                attempt = 1L,
                step = "example-publish",
                end = "done",
                passed = true,
                at = 300L,
                start = "7a8b9c",
                head = "7a8b9c",
                evidence = "published 1.0",
            ),
            releaseRun(
                part = "candidate",
                position = 1L,
                attempt = 1L,
                step = "example-candidate",
                end = "done",
                passed = true,
                at = 50L,
            ),
            releaseRun(
                part = "judge",
                position = 1L,
                attempt = 1L,
                step = "example-judge",
                end = "done",
                passed = false,
                at = 400L,
                evidence = "judge transcript",
                reason = "the judge found a gap",
            ),
        ),
    )

    private fun gapsRow(): EpicHandoverRow = EpicHandoverRow(
        phase = "ended_with_gaps",
        reviews = listOf(
            EpicReviewVerdictRow(1L, "example-reviewer", "example-review-step", "gap", "the claim lacks a test"),
            EpicReviewVerdictRow(2L, "example-second", "", "holds", ""),
        ),
        ledger = listOf(
            EpicLedgerFindingRow(
                id = "example-reviewer-1",
                position = 1L,
                reviewer = "example-reviewer",
                claim = "the epic dropped a requirement",
                citation = "example.md:4",
                location = "internal/example.go:12",
                evidence = "example evidence",
                step = "example-verify",
                result = "confirmed",
                unverified = false,
                reason = "the requirement is absent",
                closures = listOf(
                    EpicClosureRow(1L, "example-close", "open", "still absent"),
                    EpicClosureRow(2L, "example-close", "closed", ""),
                ),
            ),
            EpicLedgerFindingRow(
                id = "example-second-1",
                position = 2L,
                reviewer = "example-second",
                claim = "the test is tautological",
                citation = "example.md:9",
                location = "",
                evidence = "",
                step = "",
                result = "",
                unverified = true,
                reason = "no verifier ran",
                closures = emptyList(),
            ),
        ),
        releases = emptyList(),
        releaseSteps = emptyList(),
    )

    private fun releasedRow(): EpicHandoverRow = EpicHandoverRow(
        phase = "released",
        reviews = emptyList(),
        ledger = emptyList(),
        releases = listOf(
            EpicReleaseRunRow(
                part = "release",
                position = 1L,
                attempt = 1L,
                step = "example-tag",
                start = "1a2b3c",
                head = "4d5e6f",
                end = "done",
                commits = emptyList(),
                evidence = "",
                passed = false,
                reason = "tag exists",
                at = 100L,
            ),
            EpicReleaseRunRow(
                part = "release",
                position = 1L,
                attempt = 2L,
                step = "example-tag",
                start = "1a2b3c",
                head = "7a8b9c",
                end = "done",
                commits = listOf("1111aaa", "2222bbb"),
                evidence = "tag v1 pushed",
                passed = true,
                reason = "",
                at = 200L,
            ),
            EpicReleaseRunRow(
                part = "release",
                position = 2L,
                attempt = 1L,
                step = "example-publish",
                start = "7a8b9c",
                head = "7a8b9c",
                end = "done",
                commits = emptyList(),
                evidence = "published 1.0",
                passed = true,
                reason = "",
                at = 300L,
            ),
            EpicReleaseRunRow(
                part = "candidate",
                position = 1L,
                attempt = 1L,
                step = "example-candidate",
                start = "",
                head = "",
                end = "done",
                commits = emptyList(),
                evidence = "",
                passed = true,
                reason = "",
                at = 50L,
            ),
            EpicReleaseRunRow(
                part = "judge",
                position = 1L,
                attempt = 1L,
                step = "example-judge",
                start = "",
                head = "",
                end = "done",
                commits = emptyList(),
                evidence = "judge transcript",
                passed = false,
                reason = "the judge found a gap",
                at = 400L,
            ),
        ),
        releaseSteps = listOf(
            HandoverReleaseStepRow("example-tag", "example-tag-v1", "delete the tag"),
            HandoverReleaseStepRow("example-publish", "example-registry", "unpublish"),
        ),
    )

    @Test
    fun theGapsEpicMapsEveryFieldInOrder() {
        assertEquals(gapsRow(), gapsBody().toEpicHandoverRow())
    }

    @Test
    fun theReleasedEpicMapsEveryFieldInOrder() {
        assertEquals(releasedRow(), releasedBody().toEpicHandoverRow())
    }

    /** Every text under [tag], in the order the tree holds them. */
    private fun textsUnder(tag: String): List<String> =
        composeRule.onAllNodes(
            hasAnyAncestor(hasTestTag(tag)) and SemanticsMatcher.keyIsDefined(SemanticsProperties.Text),
            useUnmergedTree = true,
        ).fetchSemanticsNodes().map { node -> node.config[SemanticsProperties.Text].joinToString("") { it.text } }

    @Test
    fun theGapsEpicShowsItsPhaseVerdictsAndLedgerUnderTheirTags() {
        composeRule.setContent { EpicHandover(gapsBody().toEpicHandoverRow()) }

        assertEquals(
            listOf(
                "phase ended_with_gaps",
                "REVIEW",
                "example-reviewer",
                "gap",
                "the claim lacks a test",
                "step example-review-step",
                "example-second",
                "holds",
                "LEDGER",
                "example-reviewer-1",
                "example-reviewer",
                "confirmed",
                "the epic dropped a requirement",
                "internal/example.go:12",
                "the requirement is absent",
                "round 1 · open · still absent",
                "round 2 · closed",
                "example-second-1",
                "example-second",
                "unverified",
                "the test is tautological",
                "no verifier ran",
            ),
            textsUnder("handover"),
        )
        assertEquals(
            listOf("example-reviewer", "gap", "the claim lacks a test", "step example-review-step"),
            textsUnder("verdict:0"),
        )
        assertEquals(listOf("example-second", "holds"), textsUnder("verdict:1"))
        assertEquals(
            listOf(
                "example-reviewer-1",
                "example-reviewer",
                "confirmed",
                "the epic dropped a requirement",
                "internal/example.go:12",
                "the requirement is absent",
                "round 1 · open · still absent",
                "round 2 · closed",
            ),
            textsUnder("finding:0"),
        )
        assertEquals(
            listOf("example-second-1", "example-second", "unverified", "the test is tautological", "no verifier ran"),
            textsUnder("finding:1"),
        )
    }

    @Test
    fun theReleasedEpicShowsItsStepsAndRunsUnderTheirTags() {
        composeRule.setContent { EpicHandover(releasedBody().toEpicHandoverRow()) }

        assertEquals(
            listOf(
                "phase released",
                "RELEASE",
                "example-tag",
                "example-tag-v1",
                "undo delete the tag",
                "attempt 1",
                "done",
                "failed",
                "head 4d5e6f",
                "tag exists",
                "attempt 2",
                "done",
                "passed",
                "head 7a8b9c",
                "commit 1111aaa",
                "commit 2222bbb",
                "tag v1 pushed",
                "example-publish",
                "example-registry",
                "undo unpublish",
                "attempt 1",
                "done",
                "passed",
                "head 7a8b9c",
                "published 1.0",
                "attempt 1",
                "done",
                "passed",
                "attempt 1",
                "done",
                "failed",
                "the judge found a gap",
                "judge transcript",
            ),
            textsUnder("handover"),
        )
        assertEquals(listOf("example-tag", "example-tag-v1", "undo delete the tag"), textsUnder("release-step:1"))
        assertEquals(
            listOf("attempt 1", "done", "failed", "head 4d5e6f", "tag exists"),
            textsUnder("release-run:release:0"),
        )
        assertEquals(
            listOf("attempt 2", "done", "passed", "head 7a8b9c", "commit 1111aaa", "commit 2222bbb", "tag v1 pushed"),
            textsUnder("release-run:release:1"),
        )
        assertEquals(
            listOf("example-publish", "example-registry", "undo unpublish"),
            textsUnder("release-step:2"),
        )
        assertEquals(
            listOf("attempt 1", "done", "passed", "head 7a8b9c", "published 1.0"),
            textsUnder("release-run:release:2"),
        )
        assertEquals(listOf("attempt 1", "done", "passed"), textsUnder("release-run:candidate:3"))
        assertEquals(
            listOf("attempt 1", "done", "failed", "the judge found a gap", "judge transcript"),
            textsUnder("release-run:judge:4"),
        )
    }

    @Test
    fun anEpicWithNoReviewsLedgerOrReleasesDrawsNothing() {
        composeRule.setContent { EpicHandover(body(outcome = "in progress").toEpicHandoverRow()) }

        assertEquals(
            0,
            composeRule.onAllNodes(hasTestTag("handover"), useUnmergedTree = true).fetchSemanticsNodes().size,
        )
        assertEquals(
            0,
            composeRule.onAllNodes(SemanticsMatcher.keyIsDefined(SemanticsProperties.Text), useUnmergedTree = true)
                .fetchSemanticsNodes().size,
        )
    }
}
