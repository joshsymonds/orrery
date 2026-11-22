package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicClosureRow
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicLedgerFindingRow
import io.github.joshsymonds.orrery.api.EpicReleaseProofRow
import io.github.joshsymonds.orrery.api.EpicReleaseRunRow
import io.github.joshsymonds.orrery.api.EpicReportRow
import io.github.joshsymonds.orrery.api.EpicReviewVerdictRow
import io.github.joshsymonds.orrery.api.EpicRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class EpicEndingWordsTest {
    @Test
    fun aReviewingEpicReachesReviewWithNoVerdictYetAndNoFindings() {
        val view = reviewOf(detail(outcome = "reviewing"))

        assertEquals(
            ReviewView(words = "no verdict yet · no findings", reviewers = emptyList(), findings = emptyList()),
            view,
        )
    }

    @Test
    fun aCorrectingEpicReachesReviewWithNoVerdictYet() {
        assertEquals("no verdict yet · no findings", reviewOf(detail(outcome = "correcting"))?.words)
    }

    @Test
    fun aClosedEpicWithNoVerdictAndNoLedgerHasNoReview() {
        assertNull(reviewOf(detail(outcome = "closed_abandoned")))
    }

    @Test
    fun aCleanEpicWithNoVerdictAndNoLedgerHasNoReview() {
        assertNull(reviewOf(detail(outcome = "clean")))
    }

    @Test
    fun anAcceptedOrBuiltEpicIsNeverReviewedEvenWithAVerdict() {
        assertNull(reviewOf(detail(outcome = "accepted", reviews = listOf(verdict(1L, "example-reviewer", "holds")))))
        assertNull(reviewOf(detail(outcome = "built", ledger = listOf(finding("example-reviewer-1")))))
    }

    @Test
    fun aClosedEpicWithAVerdictStillReachesReview() {
        assertEquals(
            "1 reviewer · no findings",
            reviewOf(
                detail(outcome = "closed_superseded", reviews = listOf(verdict(1L, "example-reviewer", "holds"))),
            )?.words,
        )
    }

    @Test
    fun aRecordEpicHasNoReview() {
        assertNull(reviewOf(detail(source = "", outcome = "reviewing")))
    }

    @Test
    fun theReviewCountWordsPluralizeReviewersAndFindings() {
        val view = reviewOf(
            detail(
                outcome = "reviewing",
                reviews = listOf(
                    verdict(1L, "example-reviewer-a", "holds"),
                    verdict(2L, "example-reviewer-b", "fails"),
                ),
                ledger = listOf(finding("example-reviewer-a-1")),
            ),
        )

        assertEquals("2 reviewers · 1 finding", view?.words)
        val twoFindings = detail(
            outcome = "reviewing",
            reviews = listOf(verdict(1L, "example-reviewer", "holds")),
            ledger = listOf(finding("example-reviewer-1"), finding("example-reviewer-2")),
        )
        assertEquals("1 reviewer · 2 findings", reviewOf(twoFindings)?.words)
    }

    @Test
    fun eachReviewerVerdictHasItsChipWordAndTone() {
        val view = reviewOf(
            detail(
                outcome = "reviewing",
                reviews = listOf(
                    verdict(1L, "example-reviewer-a", "holds", reason = "Example reason."),
                    verdict(2L, "example-reviewer-b", "fails"),
                    verdict(3L, "example-reviewer-c", "gap"),
                    verdict(4L, "example-reviewer-d", "maybe"),
                ),
            ),
        )

        assertEquals(
            listOf(
                ReviewerLine("example-reviewer-a", "Example reason.", "holds", ChipTone.Ok),
                ReviewerLine("example-reviewer-b", "", "fails", ChipTone.Bad),
                ReviewerLine("example-reviewer-c", "", "gap", ChipTone.Attention),
                ReviewerLine("example-reviewer-d", "", "maybe", ChipTone.Neutral),
            ),
            view?.reviewers,
        )
    }

    @Test
    fun eachFindingResultHasItsChipWordAndTone() {
        val view = reviewOf(
            detail(
                outcome = "reviewing",
                ledger = listOf(
                    finding("example-reviewer-1", result = "confirmed"),
                    finding("example-reviewer-2", result = "dropped"),
                    finding("example-reviewer-3", result = "undetermined"),
                    finding("example-reviewer-4", result = "", unverified = true),
                    finding("example-reviewer-5", result = ""),
                    finding("example-reviewer-6", result = "partly"),
                ),
            ),
        )

        assertEquals(
            listOf(
                "confirmed" to ChipTone.Bad,
                "dropped" to ChipTone.Neutral,
                "undetermined" to ChipTone.Attention,
                "left unverified" to ChipTone.Attention,
                "being verified" to ChipTone.Live,
                "partly" to ChipTone.Neutral,
            ),
            view?.findings?.map { it.result to it.tone },
        )
    }

    @Test
    fun aFindingLineCarriesItsClaimLocationReasonAndClosures() {
        val view = reviewOf(
            detail(
                outcome = "reviewing",
                ledger = listOf(
                    finding(
                        "example-reviewer-1",
                        result = "confirmed",
                        reason = "Example reason.",
                        location = "example.kt:12",
                        closures = listOf(closure(1L, "closed"), closure(2L, "open", reason = "Still open.")),
                    ),
                ),
            ),
        )

        assertEquals(
            listOf(
                FindingLine(
                    id = "example-reviewer-1",
                    what = "Example claim.",
                    where = "example.kt:12",
                    result = "confirmed",
                    tone = ChipTone.Bad,
                    reason = "Example reason.",
                    closures = listOf(
                        ClosureLine("round 1 · closed", ChipTone.Ok),
                        ClosureLine("round 2 · open · Still open.", ChipTone.Attention),
                    ),
                ),
            ),
            view?.findings,
        )
    }

    @Test
    fun aClosureOtherThanClosedIsAttentionAndItsUndeterminedReasonIsOmittedWhenEmpty() {
        val view = reviewOf(
            detail(
                outcome = "reviewing",
                ledger = listOf(finding("example-reviewer-1", closures = listOf(closure(3L, "undetermined")))),
            ),
        )

        assertEquals(
            listOf(ClosureLine("round 3 · undetermined", ChipTone.Attention)),
            view?.findings?.single()?.closures,
        )
    }

    @Test
    fun isUndoNoneIsNoneTrimmedAndIgnoringCase() {
        assertTrue(isUndoNone("none"))
        assertTrue(isUndoNone("  NONE "))
        assertFalse(isUndoNone("none once someone has fetched the tag"))
        assertFalse(isUndoNone("rollback"))
        assertFalse(isUndoNone(""))
    }

    @Test
    fun aReleaseEpicReachesReleaseWithItsRowsAndThen() {
        val view = releaseOf(
            detail(
                outcome = "released",
                releaseProof = listOf(proof(1L, "passed", target = "example-target", evidence = "Example evidence.")),
                releaseThen = "Roll it out.",
            ),
        )

        assertEquals(
            ReleaseView(
                words = "1 of 1 steps passed",
                rows = listOf(
                    ReleaseLine(
                        position = 1L,
                        step = "Example step 1",
                        target = "example-target",
                        undo = "rollback",
                        state = "passed",
                        tone = ReleaseTone.Ok,
                        reason = "",
                        evidence = "Example evidence.",
                        runs = emptyList(),
                    ),
                ),
                then = "Then: Roll it out.",
                offer = 0L,
            ),
            view,
        )
    }

    @Test
    fun aReleaseWithNoThenHasAnEmptyThen() {
        assertEquals("", releaseOf(detail(outcome = "released", releaseProof = listOf(proof(1L, "passed"))))?.then)
    }

    @Test
    fun aStepWithNoUndoSaysNotGiven() {
        assertEquals(
            "not given",
            releaseOf(detail(outcome = "released", releaseProof = listOf(proof(1L, "passed", undo = ""))))
                ?.rows?.single()?.undo,
        )
    }

    @Test
    fun eachReleaseStateHasItsCellWordAndTone() {
        val view = releaseOf(
            detail(
                outcome = "releasing",
                releaseProof = listOf(
                    proof(1L, "waiting"),
                    proof(2L, "running"),
                    proof(3L, "passed"),
                    proof(4L, "failed"),
                    proof(5L, "unknown"),
                ),
            ),
        )

        assertEquals(
            listOf(
                "waiting" to ReleaseTone.None,
                "running" to ReleaseTone.Live,
                "passed" to ReleaseTone.Ok,
                "failed" to ReleaseTone.Bad,
                "unknown" to ReleaseTone.None,
            ),
            view?.rows?.map { it.state to it.tone },
        )
    }

    @Test
    fun aFailedStepCarriesItsReasonAndAPassedStepDoesNot() {
        val view = releaseOf(
            detail(
                outcome = "stopped",
                releaseProof = listOf(
                    proof(1L, "failed", reason = "Example failure."),
                    proof(2L, "passed", reason = "Ignored."),
                ),
            ),
        )

        assertEquals(listOf("Example failure.", ""), view?.rows?.map { it.reason })
    }

    @Test
    fun theReleasingWordsNameTheRunningStepThenTheFirstUnpassedThenTheCount() {
        assertEquals(
            "step 2 of 3",
            releaseOf(
                detail(
                    outcome = "releasing",
                    releaseProof = listOf(proof(1L, "passed"), proof(2L, "running"), proof(3L, "waiting")),
                ),
            )?.words,
        )
        assertEquals(
            "step 2 of 3",
            releaseOf(
                detail(
                    outcome = "releasing",
                    releaseProof = listOf(proof(1L, "passed"), proof(2L, "waiting"), proof(3L, "waiting")),
                ),
            )?.words,
        )
        assertEquals(
            "step 3 of 3",
            releaseOf(
                detail(
                    outcome = "releasing",
                    releaseProof = listOf(proof(1L, "passed"), proof(2L, "passed"), proof(3L, "passed")),
                ),
            )?.words,
        )
    }

    @Test
    fun aStoppedReleaseWordsNameTheFirstUnpassedStep() {
        assertEquals(
            "stopped at step 2 of 3",
            releaseOf(
                detail(
                    outcome = "stopped",
                    releaseProof = listOf(proof(1L, "passed"), proof(2L, "failed"), proof(3L, "waiting")),
                ),
            )?.words,
        )
    }

    @Test
    fun otherReleasePhasesCountTheStepsPassed() {
        assertEquals(
            "2 of 3 steps passed",
            releaseOf(
                detail(
                    outcome = "released",
                    releaseProof = listOf(proof(1L, "passed"), proof(2L, "passed"), proof(3L, "waiting")),
                ),
            )?.words,
        )
    }

    @Test
    fun eachStepRunsItsReleaseAndNamedRunsAtItsPositionInOrder() {
        val view = releaseOf(
            detail(
                outcome = "stopped",
                releaseProof = listOf(proof(1L, "failed"), proof(2L, "passed")),
                releases = listOf(
                    run(1L, passed = true),
                    run(1L, passed = false, attempt = 2L, reason = "Example failure."),
                    run(2L, passed = true, part = "judge"),
                    run(2L, passed = true, part = "candidate"),
                    run(2L, passed = true, part = "named"),
                ),
            ),
        )

        assertEquals(
            listOf(
                listOf("attempt 1 · passed", "attempt 2 · failed · Example failure."),
                listOf("attempt 1 · passed"),
            ),
            view?.rows?.map { it.runs },
        )
    }

    @Test
    fun theOfferIsTheFirstUnpassedFailedStepWithNoUndoWhileStopped() {
        assertEquals(
            2L,
            releaseOf(
                detail(
                    outcome = "stopped",
                    releaseProof = listOf(proof(1L, "passed"), proof(2L, "failed", undo = " None ")),
                ),
            )?.offer,
        )
    }

    @Test
    fun aFailedStepWithAnUndoIsNotOffered() {
        assertEquals(
            0L,
            releaseOf(detail(outcome = "stopped", releaseProof = listOf(proof(1L, "failed", undo = "rollback"))))
                ?.offer,
        )
    }

    @Test
    fun aFailedStepWhoseUndoIsNoneOnceFetchedIsNotOffered() {
        assertEquals(
            0L,
            releaseOf(
                detail(
                    outcome = "stopped",
                    releaseProof = listOf(proof(1L, "failed", undo = "none once someone has fetched the tag")),
                ),
            )?.offer,
        )
    }

    @Test
    fun noStepIsOfferedWhenAnEarlierUnpassedStepHasAnUndo() {
        assertEquals(
            0L,
            releaseOf(
                detail(
                    outcome = "stopped",
                    releaseProof = listOf(proof(1L, "failed", undo = "rollback"), proof(2L, "failed", undo = "none")),
                ),
            )?.offer,
        )
    }

    @Test
    fun aWaitingStepIsNotOfferedEvenWithNoUndo() {
        assertEquals(
            0L,
            releaseOf(
                detail(
                    outcome = "stopped",
                    releaseProof = listOf(proof(1L, "passed"), proof(2L, "waiting", undo = "none")),
                ),
            )?.offer,
        )
    }

    @Test
    fun onlyTheFirstUnpassedStepIsOfferedWhileStopped() {
        assertEquals(
            2L,
            releaseOf(
                detail(
                    outcome = "stopped",
                    releaseProof = listOf(
                        proof(1L, "passed"),
                        proof(2L, "failed", undo = "none"),
                        proof(3L, "failed", undo = "none"),
                    ),
                ),
            )?.offer,
        )
    }

    @Test
    fun noStepIsOfferedOutsideStoppedEvenWithAFailedNoneStep() {
        for (phase in listOf("releasing", "released", "done")) {
            val view = releaseOf(detail(outcome = phase, releaseProof = listOf(proof(1L, "failed", undo = "none"))))
            assertEquals(0L, view?.offer)
        }
    }

    @Test
    fun aReleaseWithNoStepsHasNoRelease() {
        assertNull(releaseOf(detail(outcome = "releasing")))
    }

    @Test
    fun aWaitingReleaseBeforeItsPhaseHasNoRelease() {
        assertNull(releaseOf(detail(outcome = "accepted", releaseProof = listOf(proof(1L, "waiting")))))
    }

    @Test
    fun aStepThatHasLeftWaitingReachesReleaseBeforeItsPhase() {
        assertEquals(
            "0 of 1 steps passed",
            releaseOf(detail(outcome = "reviewing", releaseProof = listOf(proof(1L, "running"))))?.words,
        )
    }

    @Test
    fun aReleaseWithRunsReachesReleaseBeforeItsPhase() {
        assertEquals(
            "0 of 1 steps passed",
            releaseOf(
                detail(
                    outcome = "reviewing",
                    releaseProof = listOf(proof(1L, "waiting")),
                    releases = listOf(run(1L, true)),
                ),
            )?.words,
        )
    }

    @Test
    fun aReportSentenceNamesItsOutcome() {
        val sentences = listOf(
            "released" to "Released.",
            "done" to "Done.",
            "met_at_base" to "Already met at its base.",
            "ended_with_gaps" to "Ended with gaps. It did not release.",
            "closed_superseded" to "Closed as superseded.",
            "closed_shipped" to "Closed as shipped outside orrery.",
            "closed_abandoned" to "Closed as abandoned.",
            "other_outcome" to "Ended: other_outcome.",
        )

        for ((outcome, sentence) in sentences) {
            assertEquals(outcome, sentence, reportOf(detail(outcome = "done", report = report(outcome)))?.sentence)
        }
    }

    @Test
    fun aSetReportListsWhatShippedAndWhatDidNotAndItsWhy() {
        val view = reportOf(
            detail(
                outcome = "released",
                report = report(
                    "released",
                    shipped = listOf("REQ-1", "REQ-2"),
                    notShipped = listOf("REQ-3"),
                    why = "Example why.",
                ),
            ),
        )

        assertEquals(
            ReportView(
                sentence = "Released.",
                why = "Example why.",
                shipped = "REQ-1, REQ-2",
                notShipped = "REQ-3",
                replacedBy = "",
                gaps = false,
            ),
            view,
        )
    }

    @Test
    fun aSetReportThatShippedNothingSaysNone() {
        assertEquals("none", reportOf(detail(outcome = "ended_with_gaps", report = report("ended_with_gaps")))?.shipped)
    }

    @Test
    fun anEndedWithGapsReportIsGaps() {
        assertTrue(reportOf(detail(outcome = "ended_with_gaps", report = report("ended_with_gaps")))?.gaps == true)
        assertFalse(reportOf(detail(outcome = "released", report = report("released")))?.gaps == true)
    }

    @Test
    fun aClosedEpicWithNoReportIsReportedFromItsPhase() {
        assertEquals(
            ReportView(
                sentence = "Closed as abandoned.",
                why = "",
                shipped = "",
                notShipped = "",
                replacedBy = "",
                gaps = false,
            ),
            reportOf(detail(outcome = "closed_abandoned")),
        )
    }

    @Test
    fun anUnfinishedEpicWithNoReportHasNoReport() {
        assertNull(reportOf(detail(outcome = "reviewing")))
    }

    @Test
    fun theReplacedByIsTheSlugOfTheEpicItNamed() {
        assertEquals(
            "example-other",
            reportOf(detail(outcome = "closed_superseded", supersededBy = "example-repo/example-other"))?.replacedBy,
        )
    }

    @Test
    fun replacementsAreTheListedEpicsOnThisPathOtherThanThisEpicInListedOrder() {
        val listed = listOf(
            listedEpic("example-repo/example-epic", "example-epic", "/example/repo"),
            listedEpic("example-repo/example-other", "example-other", "/example/repo"),
            listedEpic("other-repo/example-elsewhere", "example-elsewhere", "/example/elsewhere"),
            listedEpic("example-repo/example-third", "example-third", "/example/repo"),
        )

        assertEquals(
            listOf(
                Replacement("example-repo/example-other", "example-other"),
                Replacement("example-repo/example-third", "example-third"),
            ),
            replacementsOf(detail(outcome = "reviewing"), listed),
        )
    }

    @Test
    fun anEpicWithNoPathHasNoReplacements() {
        val listed = listOf(listedEpic("example-repo/example-other", "example-other", ""))

        assertEquals(emptyList<Replacement>(), replacementsOf(detail(outcome = "reviewing", path = ""), listed))
    }

    @Test
    fun everyReviewPhaseWithAVerdictReachesReview() {
        val phases = listOf(
            "reviewing", "correcting", "clean", "releasing", "stopped", "released", "done", "ended_with_gaps",
            "closed_superseded", "closed_shipped", "closed_abandoned",
        )
        for (phase in phases) {
            val view = reviewOf(detail(outcome = phase, reviews = listOf(verdict(1L, "example-reviewer", "holds"))))
            assertEquals(phase, "1 reviewer · no findings", view?.words)
        }
        assertNull(reviewOf(detail(outcome = "met_at_base", reviews = listOf(verdict(1L, "example-reviewer", "holds")))))
    }

    @Test
    fun everyReleasePhaseReachesReleaseWithWaitingStepsAndNoRuns() {
        for (phase in listOf("clean", "releasing", "stopped", "released", "done")) {
            val view = releaseOf(detail(outcome = phase, releaseProof = listOf(proof(1L, "waiting"))))
            assertEquals(phase, 1, view?.rows?.size)
        }
        assertNull(releaseOf(detail(outcome = "ended_with_gaps", releaseProof = listOf(proof(1L, "waiting")))))
    }

    @Test
    fun aRecordEpicHasNoReleaseAndNoReport() {
        assertNull(releaseOf(detail(source = "record", outcome = "releasing", releaseProof = listOf(proof(1L, "running")))))
        assertNull(reportOf(detail(source = "record", outcome = "released", report = report("released"))))
    }

    @Test
    fun aReportSetOnAnEpicThatIsNotClosedReachesTheReport() {
        assertEquals("Released.", reportOf(detail(outcome = "released", report = report("released")))?.sentence)
    }

    private fun listedEpic(id: String, slug: String, path: String) = EpicRow(
        id = id,
        repository = "example-repo",
        slug = slug,
        outcome = "reviewing",
        changed = NOW,
        live = false,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        source = "orreryd",
        path = path,
    )

    private fun verdict(position: Long, reviewer: String, verdict: String, reason: String = "") =
        EpicReviewVerdictRow(
            position = position,
            reviewer = reviewer,
            step = "example-step",
            verdict = verdict,
            reason = reason,
        )

    private fun finding(
        id: String,
        result: String = "",
        unverified: Boolean = false,
        reason: String = "",
        location: String = "",
        closures: List<EpicClosureRow> = emptyList(),
    ) = EpicLedgerFindingRow(
        id = id,
        position = 1L,
        reviewer = "example-reviewer",
        claim = "Example claim.",
        citation = "",
        location = location,
        evidence = "",
        step = "example-step",
        result = result,
        unverified = unverified,
        reason = reason,
        closures = closures,
    )

    private fun closure(round: Long, result: String, reason: String = "") =
        EpicClosureRow(round = round, step = "example-step", result = result, reason = reason)

    private fun proof(
        position: Long,
        state: String,
        undo: String = "rollback",
        target: String = "",
        reason: String = "",
        evidence: String = "",
    ) = EpicReleaseProofRow(
        position = position,
        step = "Example step $position",
        target = target,
        undo = undo,
        state = state,
        evidence = evidence,
        reason = reason,
        attempt = 0L,
        at = 0L,
    )

    private fun run(
        position: Long,
        passed: Boolean,
        attempt: Long = 1L,
        part: String = "release",
        reason: String = "",
    ) = EpicReleaseRunRow(
        part = part,
        position = position,
        attempt = attempt,
        step = "Example step",
        start = "",
        head = "",
        end = "",
        commits = emptyList(),
        evidence = "",
        passed = passed,
        reason = reason,
        at = 0L,
    )

    private fun report(
        outcome: String,
        shipped: List<String> = emptyList(),
        notShipped: List<String> = emptyList(),
        why: String = "",
    ) = EpicReportRow(outcome = outcome, shipped = shipped, notShipped = notShipped, why = why)

    private fun detail(
        source: String = "orreryd",
        outcome: String,
        path: String = "/example/repo",
        reviews: List<EpicReviewVerdictRow> = emptyList(),
        ledger: List<EpicLedgerFindingRow> = emptyList(),
        releaseProof: List<EpicReleaseProofRow> = emptyList(),
        releases: List<EpicReleaseRunRow> = emptyList(),
        releaseThen: String = "",
        report: EpicReportRow? = null,
        supersededBy: String = "",
    ) = EpicDetail(
        epic = EpicRow(
            id = "example-repo/example-epic",
            repository = "example-repo",
            slug = "example-epic",
            outcome = outcome,
            changed = NOW,
            live = true,
            runningSteps = emptyList(),
            endedSteps = 0,
            problem = "",
            source = source,
            path = path,
        ),
        tasks = emptyList(),
        steps = emptyList(),
        contract = EpicContractRow(
            title = "Example epic title",
            approved = "",
            intent = "",
            failures = emptyList(),
            care = "serious",
            additions = emptyList(),
            premises = emptyList(),
            exclusions = emptyList(),
            approach = "",
            alternatives = emptyList(),
            releaseSteps = emptyList(),
            releaseThen = releaseThen,
            checks = "",
            base = "",
            accepted = 0L,
        ),
        reviews = reviews,
        ledger = ledger,
        releaseProof = releaseProof,
        releases = releases,
        report = report,
        supersededBy = supersededBy,
    )

    private companion object {
        const val NOW = 1_800_000_000L
    }
}
