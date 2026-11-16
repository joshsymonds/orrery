package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.EpicClosure
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicLedgerFinding
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseRun
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReviewVerdict
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse

/**
 * One reviewer's verdict on an epic: [position] is its place among the epic-stage reviewers, counted from 1. [verdict]
 * is holds, fails or gap, [reason] says why, and [step] is the step that ran the review.
 */
data class EpicReviewVerdictRow(
    val position: Long,
    val reviewer: String,
    val step: String,
    val verdict: String,
    val reason: String,
)

/**
 * One round of closing a ledger finding: [step] ran the closing check, which gave [result] (closed, open or
 * undetermined) for [reason].
 */
data class EpicClosureRow(
    val round: Long,
    val step: String,
    val result: String,
    val reason: String,
)

/**
 * One finding of an epic's end-of-epic review ledger, at [position] in the ledger, counted from 1. [claim], [citation],
 * [location] and [evidence] are what the reviewer gave. [step] verified it and gave [result] (confirmed, dropped or
 * undetermined) for [reason]; [unverified] says no verifier ran, and [reason] then says why. [closures] are its rounds
 * in order.
 */
data class EpicLedgerFindingRow(
    val id: String,
    val position: Long,
    val reviewer: String,
    val claim: String,
    val citation: String,
    val location: String,
    val evidence: String,
    val step: String,
    val result: String,
    val unverified: Boolean,
    val reason: String,
    val closures: List<EpicClosureRow>,
)

/**
 * One run of an epic's release. [part] is candidate, release, judge or named; [position] and [attempt] count from 1
 * and [step] is the release step it ran for. [start] and [head] are the commits it began and ended on, [commits] the
 * commits it made in order, and [end] its step's end, empty when it has none. [evidence] is what it gave for its
 * result, [passed] says whether it passed and [reason] why. [at] is when it was recorded, in unix seconds.
 */
data class EpicReleaseRunRow(
    val part: String,
    val position: Long,
    val attempt: Long,
    val step: String,
    val start: String,
    val head: String,
    val end: String,
    val commits: List<String>,
    val evidence: String,
    val passed: Boolean,
    val reason: String,
    val at: Long,
)

/**
 * An epic's handover: its [phase], which is its outcome, its reviewers' [reviews] in position order, its ledger
 * [ledger] in ledger order, its release [releases] in the order the server lists them, and the contract's
 * [releaseSteps] in order.
 */
data class EpicHandoverRow(
    val phase: String = "",
    val reviews: List<EpicReviewVerdictRow> = emptyList(),
    val ledger: List<EpicLedgerFindingRow> = emptyList(),
    val releases: List<EpicReleaseRunRow> = emptyList(),
    val releaseSteps: List<EpicReleaseStepRow> = emptyList(),
)

/**
 * The handover of this epic response. A record epic has no contract, reviews, ledger or releases, so its lists are
 * empty.
 */
fun EpicsGetResponse.toEpicHandoverRow(): EpicHandoverRow =
    EpicHandoverRow(
        phase = epic.outcome,
        reviews = reviewsList.map { it.toEpicReviewVerdictRow() },
        ledger = ledgerList.map { it.toEpicLedgerFindingRow() },
        releases = releasesList.map { it.toEpicReleaseRunRow() },
        releaseSteps = contract.releaseStepsList.map { it.toEpicReleaseStepRow() },
    )

private fun EpicReviewVerdict.toEpicReviewVerdictRow(): EpicReviewVerdictRow =
    EpicReviewVerdictRow(
        position = position,
        reviewer = reviewer,
        step = step,
        verdict = verdict,
        reason = reason,
    )

private fun EpicLedgerFinding.toEpicLedgerFindingRow(): EpicLedgerFindingRow =
    EpicLedgerFindingRow(
        id = id,
        position = position,
        reviewer = reviewer,
        claim = claim,
        citation = citation,
        location = location,
        evidence = evidence,
        step = step,
        result = result,
        unverified = unverified,
        reason = reason,
        closures = closuresList.map { it.toEpicClosureRow() },
    )

private fun EpicClosure.toEpicClosureRow(): EpicClosureRow =
    EpicClosureRow(
        round = round,
        step = step,
        result = result,
        reason = reason,
    )

private fun EpicReleaseRun.toEpicReleaseRunRow(): EpicReleaseRunRow =
    EpicReleaseRunRow(
        part = part,
        position = position,
        attempt = attempt,
        step = step,
        start = start,
        head = head,
        end = end,
        commits = commitsList.toList(),
        evidence = evidence,
        passed = passed,
        reason = reason,
        at = at,
    )
