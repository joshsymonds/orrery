package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicClosureRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicLedgerFindingRow
import io.github.joshsymonds.orrery.api.EpicReleaseProofRow
import io.github.joshsymonds.orrery.api.EpicReleaseRunRow
import io.github.joshsymonds.orrery.api.EpicReviewVerdictRow
import io.github.joshsymonds.orrery.api.EpicRow

/**
 * A release step's state cell: none while it waits or its state is unknown, live while it runs, ok when it passed, bad
 * when it failed.
 */
enum class ReleaseTone { None, Live, Ok, Bad }

/** One reviewer's verdict line: the [reviewer], its [reason], its [verdict] as served, and its chip [tone]. */
data class ReviewerLine(val reviewer: String, val reason: String, val verdict: String, val tone: ChipTone)

/** One closure round of a finding: its [text] and its chip [tone]. */
data class ClosureLine(val text: String, val tone: ChipTone)

/**
 * One ledger finding: its [id], its [what] (the claim) and [where] (the location), its [result] word and [tone], its
 * [reason], and its [closures] in round order.
 */
data class FindingLine(
    val id: String,
    val what: String,
    val where: String,
    val result: String,
    val tone: ChipTone,
    val reason: String,
    val closures: List<ClosureLine>,
)

/** The Review section: its count [words], its reviewer lines and its finding lines. */
data class ReviewView(val words: String, val reviewers: List<ReviewerLine>, val findings: List<FindingLine>)

/**
 * One release step row: its [position], [step], [target], [undo], state word and [tone], its [reason] when it failed,
 * its [evidence], and its [runs] in order.
 */
data class ReleaseLine(
    val position: Long,
    val step: String,
    val target: String,
    val undo: String,
    val state: String,
    val tone: ReleaseTone,
    val reason: String,
    val evidence: String,
    val runs: List<String>,
)

/**
 * The Release section: its count [words], its step [rows], its [then] line, and the [offer]ed step's position, 0 when
 * no step is offered.
 */
data class ReleaseView(val words: String, val rows: List<ReleaseLine>, val then: String, val offer: Long)

/**
 * The Report section: its outcome [sentence], its [why], its [shipped] and [notShipped] ids, the slug of the epic it
 * was [replacedBy], and whether it [gaps] (ended with gaps).
 */
data class ReportView(
    val sentence: String,
    val why: String,
    val shipped: String,
    val notShipped: String,
    val replacedBy: String,
    val gaps: Boolean,
)

/** An epic a superseded close may name: its [id] and its [slug]. */
data class Replacement(val id: String, val slug: String)

// The phases Review is drawn in. Accepted and built are never reviewed on the page.
private val REVIEW_PHASES = setOf(
    "reviewing",
    "correcting",
    "clean",
    "releasing",
    "stopped",
    "released",
    "done",
    "ended_with_gaps",
    "closed_superseded",
    "closed_shipped",
    "closed_abandoned",
)

// The phases Release is drawn in, without a release run or a step that has left waiting.
private val RELEASE_PHASES = setOf("clean", "releasing", "stopped", "released", "done")

/** Whether an undo text is none: trimmed, ignoring case, the store's rule. */
fun isUndoNone(undo: String): Boolean = undo.trim().equals("none", ignoreCase = true)

/** The Review section of an orreryd epic in a review phase, or null when the page does not reach it. */
fun reviewOf(detail: EpicDetail): ReviewView? {
    val phase = detail.epic.outcome
    if (phase !in REVIEW_PHASES) {
        return null
    }
    val reviewing = phase == "reviewing" || phase == "correcting"
    if (!reviewing && detail.reviews.isEmpty() && detail.ledger.isEmpty()) {
        return null
    }
    val reviewers = if (detail.reviews.isEmpty()) {
        "no verdict yet"
    } else {
        plural(detail.reviews.size, "reviewer", "reviewers")
    }
    val findings = if (detail.ledger.isEmpty()) "no findings" else plural(detail.ledger.size, "finding", "findings")
    return ReviewView(
        words = "$reviewers · $findings",
        reviewers = detail.reviews.map { reviewerLine(it) },
        findings = detail.ledger.map { findingLine(it) },
    )
}

/** The Release section of an orreryd epic with release steps, reached in a release phase or once a step is in view. */
fun releaseOf(detail: EpicDetail): ReleaseView? {
    val phase = detail.epic.outcome
    val steps = detail.releaseProof
    if (steps.isEmpty()) {
        return null
    }
    if (phase !in RELEASE_PHASES && detail.releases.isEmpty() && steps.all { it.state == "waiting" }) {
        return null
    }
    val then = detail.contract?.releaseThen ?: ""
    return ReleaseView(
        words = releaseWords(phase, steps),
        rows = steps.map { rowOf(it, detail.releases) },
        then = if (then.isEmpty()) "" else "Then: $then",
        offer = offerOf(phase, steps),
    )
}

/** The Report section of an orreryd epic, reached when its report is set or its phase is a closed one. */
fun reportOf(detail: EpicDetail): ReportView? {
    val phase = detail.epic.outcome
    val report = detail.report
    if (report == null && !phase.startsWith("closed_")) {
        return null
    }
    val shipped = when {
        report == null -> ""
        report.shipped.isEmpty() -> "none"
        else -> report.shipped.joinToString(", ")
    }
    return ReportView(
        sentence = sentenceOf(report?.outcome ?: phase),
        why = report?.why ?: "",
        shipped = shipped,
        notShipped = report?.notShipped?.joinToString(", ") ?: "",
        replacedBy = detail.supersededBy.substringAfterLast("/"),
        gaps = phase == "ended_with_gaps",
    )
}

/** The listed epics a superseded close may name: those on this epic's path, other than this epic, in listed order. */
fun replacementsOf(detail: EpicDetail, listed: List<EpicRow>): List<Replacement> {
    val self = detail.epic
    if (self.path.isEmpty()) {
        return emptyList()
    }
    return listed
        .filter { it.path == self.path && it.id != self.id }
        .map { Replacement(id = it.id, slug = it.slug) }
}

private fun plural(count: Int, one: String, many: String): String = if (count == 1) "1 $one" else "$count $many"

private fun verdictTone(verdict: String): ChipTone = when (verdict) {
    "holds" -> ChipTone.Ok
    "fails" -> ChipTone.Bad
    "gap" -> ChipTone.Attention
    else -> ChipTone.Neutral
}

private fun reviewerLine(row: EpicReviewVerdictRow): ReviewerLine =
    ReviewerLine(
        reviewer = row.reviewer,
        reason = row.reason,
        verdict = row.verdict,
        tone = verdictTone(row.verdict),
    )

private fun findingChip(finding: EpicLedgerFindingRow): Pair<String, ChipTone> = when (finding.result) {
    "confirmed" -> "confirmed" to ChipTone.Bad
    "dropped" -> "dropped" to ChipTone.Neutral
    "undetermined" -> "undetermined" to ChipTone.Attention
    "" -> if (finding.unverified) {
        "left unverified" to ChipTone.Attention
    } else {
        "being verified" to ChipTone.Live
    }
    else -> finding.result to ChipTone.Neutral
}

private fun closureLine(closure: EpicClosureRow): ClosureLine {
    val reason = if (closure.reason.isEmpty()) "" else " · ${closure.reason}"
    val tone = if (closure.result == "closed") ChipTone.Ok else ChipTone.Attention
    return ClosureLine(text = "round ${closure.round} · ${closure.result}$reason", tone = tone)
}

private fun findingLine(finding: EpicLedgerFindingRow): FindingLine {
    val (word, tone) = findingChip(finding)
    return FindingLine(
        id = finding.id,
        what = finding.claim,
        where = finding.location,
        result = word,
        tone = tone,
        reason = finding.reason,
        closures = finding.closures.map { closureLine(it) },
    )
}

private fun stateCell(state: String): Pair<String, ReleaseTone> = when (state) {
    "waiting" -> "waiting" to ReleaseTone.None
    "running" -> "running" to ReleaseTone.Live
    "passed" -> "passed" to ReleaseTone.Ok
    "failed" -> "failed" to ReleaseTone.Bad
    else -> state to ReleaseTone.None
}

private fun runLine(run: EpicReleaseRunRow): String {
    val result = if (run.passed) "passed" else "failed"
    val reason = if (run.reason.isEmpty()) "" else " · ${run.reason}"
    return "attempt ${run.attempt} · $result$reason"
}

private fun rowOf(step: EpicReleaseProofRow, releases: List<EpicReleaseRunRow>): ReleaseLine {
    val (word, tone) = stateCell(step.state)
    val runs = releases.filter { (it.part == "release" || it.part == "named") && it.position == step.position }
    return ReleaseLine(
        position = step.position,
        step = step.step,
        target = step.target,
        undo = step.undo.ifEmpty { "not given" },
        state = word,
        tone = tone,
        reason = if (step.state == "failed") step.reason else "",
        evidence = step.evidence,
        runs = runs.map { runLine(it) },
    )
}

// The first release step whose state is not passed, if any.
private fun firstUnpassed(steps: List<EpicReleaseProofRow>): EpicReleaseProofRow? =
    steps.firstOrNull { it.state != "passed" }

// The count words of the Release section: the step a releasing or stopped epic is at, else the steps passed.
private fun releaseWords(phase: String, steps: List<EpicReleaseProofRow>): String {
    val n = steps.size
    return when (phase) {
        "releasing" -> {
            val at = steps.firstOrNull { it.state == "running" } ?: firstUnpassed(steps)
            "step ${at?.position ?: n.toLong()} of $n"
        }
        "stopped" -> "stopped at step ${firstUnpassed(steps)?.position ?: n.toLong()} of $n"
        else -> "${steps.count { it.state == "passed" }} of $n steps passed"
    }
}

// The one step Release from step N is offered for: the first unpassed step, when it failed with no undo in a stopped
// epic. Zero when no step is offered.
private fun offerOf(phase: String, steps: List<EpicReleaseProofRow>): Long {
    val step = firstUnpassed(steps)
    if (phase != "stopped" || step == null || step.state != "failed" || !isUndoNone(step.undo)) {
        return 0L
    }
    return step.position
}

private fun sentenceOf(outcome: String): String = when (outcome) {
    "released" -> "Released."
    "done" -> "Done."
    "met_at_base" -> "Already met at its base."
    "ended_with_gaps" -> "Ended with gaps. It did not release."
    "closed_superseded" -> "Closed as superseded."
    "closed_shipped" -> "Closed as shipped outside orrery."
    "closed_abandoned" -> "Closed as abandoned."
    else -> "Ended: $outcome."
}
