package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicClosureRow
import io.github.joshsymonds.orrery.api.EpicHandoverRow
import io.github.joshsymonds.orrery.api.EpicLedgerFindingRow
import io.github.joshsymonds.orrery.api.EpicReleaseRunRow
import io.github.joshsymonds.orrery.api.EpicReviewVerdictRow
import io.github.joshsymonds.orrery.api.HandoverReleaseStepRow

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
            .padding(horizontal = 16.dp, vertical = 6.dp),
    ) {
        Text("phase ${handover.phase}", style = MaterialTheme.typography.titleSmall)
        if (handover.reviews.isNotEmpty()) {
            Text("REVIEW", style = MaterialTheme.typography.labelLarge)
            handover.reviews.forEachIndexed { index, verdict -> ReviewVerdict(index, verdict) }
        }
        if (handover.ledger.isNotEmpty()) {
            Text("LEDGER", style = MaterialTheme.typography.labelLarge)
            handover.ledger.forEachIndexed { index, finding -> LedgerFinding(index, finding) }
        }
        if (handover.releases.isNotEmpty()) {
            Text("RELEASE", style = MaterialTheme.typography.labelLarge)
            ReleaseRuns(handover.releaseSteps, handover.releases)
        }
    }
}

/** One reviewer's verdict under its tag [index], with its reason and the step that ran it when set. */
@Composable
private fun ReviewVerdict(index: Int, verdict: EpicReviewVerdictRow) {
    Column(modifier = Modifier.fillMaxWidth().testTag("verdict:$index")) {
        Text(verdict.reviewer, style = MaterialTheme.typography.bodyMedium)
        Text(verdict.verdict, style = MaterialTheme.typography.labelMedium)
        if (verdict.reason.isNotEmpty()) {
            Text(verdict.reason, style = MaterialTheme.typography.bodySmall)
        }
        if (verdict.step.isNotEmpty()) {
            Text("step ${verdict.step}", style = MaterialTheme.typography.labelSmall)
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
        Text(finding.id, style = MaterialTheme.typography.bodyMedium)
        Text(finding.reviewer, style = MaterialTheme.typography.labelMedium)
        Text(
            if (finding.unverified) "unverified" else finding.result,
            style = MaterialTheme.typography.labelMedium,
        )
        Text(finding.claim, style = MaterialTheme.typography.bodySmall)
        if (finding.location.isNotEmpty()) {
            Text(finding.location, style = MaterialTheme.typography.labelSmall)
        }
        if (finding.reason.isNotEmpty()) {
            Text(finding.reason, style = MaterialTheme.typography.bodySmall)
        }
        finding.closures.forEach { Text(closureLine(it), style = MaterialTheme.typography.labelSmall) }
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
private fun ReleaseRuns(steps: List<HandoverReleaseStepRow>, releases: List<EpicReleaseRunRow>) {
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
private fun ReleaseStep(number: Int, step: HandoverReleaseStepRow) {
    Column(modifier = Modifier.fillMaxWidth().testTag("release-step:$number")) {
        Text(step.step, style = MaterialTheme.typography.bodyMedium)
        Text(step.target, style = MaterialTheme.typography.labelMedium)
        Text("undo ${step.undo}", style = MaterialTheme.typography.bodySmall)
    }
}

/**
 * One release run under its tag [index]: its attempt, its end when set, whether it passed, its head, its commits, and
 * its reason and evidence when set. The evidence is monospace.
 */
@Composable
private fun ReleaseRun(index: Int, run: EpicReleaseRunRow) {
    Column(modifier = Modifier.fillMaxWidth().testTag("release-run:${run.part}:$index")) {
        Text("attempt ${run.attempt}", style = MaterialTheme.typography.labelMedium)
        if (run.end.isNotEmpty()) {
            Text(run.end, style = MaterialTheme.typography.labelMedium)
        }
        Text(if (run.passed) "passed" else "failed", style = MaterialTheme.typography.labelMedium)
        if (run.head.isNotEmpty()) {
            Text("head ${run.head}", style = MaterialTheme.typography.labelSmall)
        }
        run.commits.forEach { Text("commit $it", style = MaterialTheme.typography.labelSmall) }
        if (run.reason.isNotEmpty()) {
            Text(run.reason, style = MaterialTheme.typography.bodySmall)
        }
        if (run.evidence.isNotEmpty()) {
            Text(run.evidence, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
        }
    }
}
