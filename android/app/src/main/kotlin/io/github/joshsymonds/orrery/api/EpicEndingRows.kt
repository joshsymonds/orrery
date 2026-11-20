package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseProof

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
