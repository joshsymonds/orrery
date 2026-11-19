package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EpicClosureRow
import io.github.joshsymonds.orrery.api.EpicHandoverRow
import io.github.joshsymonds.orrery.api.EpicLedgerFindingRow
import io.github.joshsymonds.orrery.api.EpicReleaseRunRow
import io.github.joshsymonds.orrery.api.EpicReleaseStepRow
import io.github.joshsymonds.orrery.api.EpicReviewVerdictRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

/** The part of a release run that runs a contract release step; its position is the step it ran. */
private const val RELEASE_PART = "release"

/**
 * An epic's handover: its phase, its reviewers' verdicts, its ledger findings with their reasons and closures, and its
 * release steps with the runs each holds. An epic with no reviews, ledger or releases draws nothing.
 */
@Composable
internal fun EpicHandover(handover: EpicHandoverRow) {
    if (handover.reviews.isEmpty() && handover.ledger.isEmpty() && handover.releases.isEmpty()) return
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("handover")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four + OrreryTokens.Space.two),
    ) {
        Text("phase ${handover.phase}".inLabelCase(), style = OrreryTokens.Type.label)
        if (handover.reviews.isNotEmpty()) {
            Text("REVIEW".inLabelCase(), style = OrreryTokens.Type.label)
            handover.reviews.forEachIndexed { index, verdict -> ReviewVerdict(index, verdict) }
        }
        if (handover.ledger.isNotEmpty()) {
            Text("LEDGER".inLabelCase(), style = OrreryTokens.Type.label)
            handover.ledger.forEachIndexed { index, finding -> LedgerFinding(index, finding) }
        }
        if (handover.releases.isNotEmpty()) {
            Text("RELEASE".inLabelCase(), style = OrreryTokens.Type.label)
            ReleaseRuns(handover.releaseSteps, handover.releases)
        }
    }
}

/** One reviewer's verdict under its tag [index], with its reason and the step that ran it when set. */
@Composable
private fun ReviewVerdict(index: Int, verdict: EpicReviewVerdictRow) {
    Column(modifier = Modifier.fillMaxWidth().testTag("verdict:$index")) {
        Text(verdict.reviewer, style = OrreryTokens.Type.body)
        Text(verdict.verdict, style = OrreryTokens.Type.small)
        if (verdict.reason.isNotEmpty()) {
            Text(verdict.reason, style = OrreryTokens.Type.small)
        }
        if (verdict.step.isNotEmpty()) {
            Text("step ${verdict.step}", style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
        }
    }
}

/**
 * One ledger finding under its tag [index]. An unverified finding shows that in place of its result. Its closures
 * follow, one line each.
 */
@Composable
private fun LedgerFinding(index: Int, finding: EpicLedgerFindingRow) {
    Column(modifier = Modifier.fillMaxWidth().testTag("finding:$index")) {
        Text(finding.id, style = OrreryTokens.Type.body)
        Text(finding.reviewer, style = OrreryTokens.Type.small)
        Text(
            if (finding.unverified) "unverified" else finding.result,
            style = OrreryTokens.Type.small,
        )
        Text(finding.claim, style = OrreryTokens.Type.small)
        if (finding.location.isNotEmpty()) {
            Text(finding.location, style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
        }
        if (finding.reason.isNotEmpty()) {
            Text(finding.reason, style = OrreryTokens.Type.small)
        }
        finding.closures.forEach { Text(closureLine(it), style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold)) }
    }
}

/** A closure's round, result, and reason when set, separated by middle dots. */
private fun closureLine(closure: EpicClosureRow): String {
    val line = "round ${closure.round} · ${closure.result}"
    return if (closure.reason.isEmpty()) line else "$line · ${closure.reason}"
}

/**
 * The release steps, each followed by the release runs that ran it, then every run that ran no contract step:
 * candidate, judge and named runs, and any release run whose position is no step's. Each run is tagged by its part
 * and its index in [releases].
 */
@Composable
private fun ReleaseRuns(steps: List<EpicReleaseStepRow>, releases: List<EpicReleaseRunRow>) {
    steps.forEachIndexed { stepIndex, step ->
        ReleaseStep(stepIndex + 1, step)
        releases.forEachIndexed { runIndex, run ->
            if (run.part == RELEASE_PART && run.position == stepIndex + 1L) ReleaseRun(runIndex, run)
        }
    }
    releases.forEachIndexed { runIndex, run ->
        if (run.part != RELEASE_PART || run.position !in 1L..steps.size.toLong()) ReleaseRun(runIndex, run)
    }
}

/** A contract release step under its tag, numbered [number] from 1. */
@Composable
private fun ReleaseStep(number: Int, step: EpicReleaseStepRow) {
    Column(modifier = Modifier.fillMaxWidth().testTag("release-step:$number")) {
        Text(step.step, style = OrreryTokens.Type.body)
        Text(step.target, style = OrreryTokens.Type.small)
        Text("undo ${step.undo}", style = OrreryTokens.Type.small)
    }
}

/**
 * One release run under its tag [index]: its attempt, its end when set, whether it passed, its head, its commits, and
 * its reason and evidence when set. The evidence is monospace.
 */
@Composable
private fun ReleaseRun(index: Int, run: EpicReleaseRunRow) {
    Column(modifier = Modifier.fillMaxWidth().testTag("release-run:${run.part}:$index")) {
        Text("attempt ${run.attempt}", style = OrreryTokens.Type.small)
        if (run.end.isNotEmpty()) {
            Text(run.end, style = OrreryTokens.Type.small)
        }
        Text(if (run.passed) "passed" else "failed", style = OrreryTokens.Type.small)
        if (run.head.isNotEmpty()) {
            Text("head ${run.head}", style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
        }
        run.commits.forEach { Text("commit $it", style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold)) }
        if (run.reason.isNotEmpty()) {
            Text(run.reason, style = OrreryTokens.Type.small)
        }
        if (run.evidence.isNotEmpty()) {
            Text(run.evidence, fontFamily = OrreryTokens.Type.small.fontFamily, style = OrreryTokens.Type.small)
        }
    }
}
