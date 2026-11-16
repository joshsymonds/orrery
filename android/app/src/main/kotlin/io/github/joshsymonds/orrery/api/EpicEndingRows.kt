package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseProof
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReport

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

/**
 * What an ended orreryd epic reports: the [outcome] it ended with, the Requirement ids it [shipped] and did not ship
 * ([notShipped]), in contract order, and [why] it shipped what it did and left the rest out.
 */
data class EpicReportRow(
    val outcome: String,
    val shipped: List<String>,
    val notShipped: List<String>,
    val why: String,
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

internal fun EpicReport.toEpicReportRow(): EpicReportRow =
    EpicReportRow(
        outcome = outcome,
        shipped = shippedList.toList(),
        notShipped = notShippedList.toList(),
        why = why,
    )
