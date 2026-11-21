package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.EpicClosure
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicLedgerFinding
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseProof
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseRun
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReport
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReviewVerdict

/**
 * One step of an orreryd epic's contract release, at [position] in the contract's release steps, counted from 1. [step]
 * is what the step does, [target] where it sends what leaves the machine, and [undo] how it is undone. [state] is
 * waiting, running, passed or failed. The run that decided the step gave [evidence] and [reason], and its [attempt] and
 * [at] (unix seconds) are its own; the evidence, reason, attempt and at are empty or zero when no run has decided it.
 */
data class EpicReleaseProofRow(
    val position: Long,
    val step: String,
    val target: String,
    val undo: String,
    val state: String,
    val evidence: String,
    val reason: String,
    val attempt: Long,
    val at: Long,
)

internal fun EpicReleaseProof.toEpicReleaseProofRow(): EpicReleaseProofRow =
    EpicReleaseProofRow(
        position = position,
        step = step,
        target = target,
        undo = undo,
        state = state,
        evidence = evidence,
        reason = reason,
        attempt = attempt,
        at = at,
    )

/**
 * One epic-stage reviewer's verdict on an orreryd epic, at [position] among the reviewers, counted from 1. [reviewer]
 * is the reviewer's name and [step] the step that ran the review. [verdict] is holds, fails or gap, and [reason] why
 * the review gave it.
 */
data class EpicReviewVerdictRow(
    val position: Long,
    val reviewer: String,
    val step: String,
    val verdict: String,
    val reason: String,
)

/**
 * One round of closing an orreryd epic's ledger finding, counted from [round] at 1. [step] ran the closing check, and
 * [result] is closed, open or undetermined, with [reason] why the check gave it.
 */
data class EpicClosureRow(
    val round: Long,
    val step: String,
    val result: String,
    val reason: String,
)

/**
 * One finding of an orreryd epic's end-of-epic review ledger, at [position] in the ledger, counted from 1. [id] is
 * `<reviewer>-<k>`, and [reviewer] raised it with [claim], [citation], [location] and [evidence]. [step] verified it
 * with [result], unless [unverified] is set, and [reason] says why the verification gave that result or why the
 * finding was left unverified. [closures] are its rounds in order.
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
 * One run of an orreryd epic's release, by [part] (candidate, release, judge or named), at [position] within its part
 * and [attempt], both counted from 1. [step] is the release step the run is for. The run started from [start] and
 * recorded [head], and its [end] is the end of its step, empty when it has none. [commits] are the commits it made in
 * order. [passed] is whether it passed, for the reason [reason] and the evidence [evidence]; [at] is when it was
 * recorded, in unix seconds.
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
 * What an ended orreryd epic shipped of its contract's Requirements. [outcome] is the ending phase the report reports,
 * [shipped] and [notShipped] are Requirement ids in contract order, and [why] is why the epic shipped what it did.
 */
data class EpicReportRow(
    val outcome: String,
    val shipped: List<String>,
    val notShipped: List<String>,
    val why: String,
)

internal fun EpicReviewVerdict.toEpicReviewVerdictRow(): EpicReviewVerdictRow =
    EpicReviewVerdictRow(
        position = position,
        reviewer = reviewer,
        step = step,
        verdict = verdict,
        reason = reason,
    )

internal fun EpicClosure.toEpicClosureRow(): EpicClosureRow =
    EpicClosureRow(
        round = round,
        step = step,
        result = result,
        reason = reason,
    )

internal fun EpicLedgerFinding.toEpicLedgerFindingRow(): EpicLedgerFindingRow =
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

internal fun EpicReleaseRun.toEpicReleaseRunRow(): EpicReleaseRunRow =
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

internal fun EpicReport.toEpicReportRow(): EpicReportRow =
    EpicReportRow(
        outcome = outcome,
        shipped = shippedList.toList(),
        notShipped = notShippedList.toList(),
        why = why,
    )
