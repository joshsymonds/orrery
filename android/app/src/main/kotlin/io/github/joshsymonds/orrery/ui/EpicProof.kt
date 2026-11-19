package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicReleaseProofRow
import io.github.joshsymonds.orrery.api.EpicReportRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

private const val RED = "red"
private const val GREEN = "green"
private const val WAITING = "waiting"

/** The red proof: a card per requirement with its red runs. It is reached once any requirement has a red run. */
@Composable
fun RedProof(
    requirements: List<EpicRequirementRow>,
    loadArtifact: ArtifactLoader,
    wedges: List<Wedge> = emptyList(),
    onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null,
) {
    val reached = requirements.any { requirement -> requirement.runs.any { it.phase == RED } }
    ProofSection(name = RED, title = "Red proof", reached = reached) {
        requirements.forEachIndexed { index, requirement ->
            ProofCard(
                name = RED,
                index = index,
                requirement = requirement,
                phase = RED,
                loadArtifact = loadArtifact,
                onOpen = openOf(requirement, wedges, onOpenPhase, Phase.IMPLEMENTING),
            )
        }
    }
}

/**
 * The green proof: a card per requirement with its green runs. It is reached once any green run has passed, and
 * a passed red run does not count.
 */
@Composable
fun GreenProof(
    requirements: List<EpicRequirementRow>,
    loadArtifact: ArtifactLoader,
    wedges: List<Wedge> = emptyList(),
    onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null,
) {
    val reached = requirements.any { requirement -> requirement.runs.any { it.phase == GREEN && it.passed } }
    ProofSection(name = GREEN, title = "Green proof", reached = reached) {
        requirements.forEachIndexed { index, requirement ->
            ProofCard(
                name = GREEN,
                index = index,
                requirement = requirement,
                phase = GREEN,
                loadArtifact = loadArtifact,
                onOpen = openOf(requirement, wedges, onOpenPhase, Phase.CHECKING),
            )
        }
    }
}

/**
 * The release proof: the contract's release steps with their target and undo, each step's state and, when a run has
 * decided it, its evidence and reason, then its then line. A step's proof is the one at its position, counted from 1.
 * It is reached once the epic is released or a step is not waiting.
 */
@Composable
fun ReleaseProof(contract: EpicContractRow?, releaseProof: List<EpicReleaseProofRow>, released: Boolean) {
    val reached = released || releaseProof.any { it.state != WAITING }
    ProofSection(name = "release", title = "Release proof", reached = reached) {
        contract?.releaseSteps?.forEachIndexed { index, step ->
            val proof = releaseProof.firstOrNull { it.position == index + 1L }
            Column(modifier = Modifier.fillMaxWidth().testTag("release-step:$index")) {
                Text(step.step, style = OrreryTokens.Type.body)
                Text(step.target, style = OrreryTokens.Type.small)
                Text(step.undo, style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
                if (proof != null) {
                    Text(
                        proof.state,
                        style = OrreryTokens.Type.small,
                        modifier = Modifier.testTag("release-step-state:$index"),
                    )
                    if (proof.evidence.isNotEmpty()) {
                        Text(
                            proof.evidence,
                            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                            modifier = Modifier.testTag("release-step-evidence:$index"),
                        )
                    }
                    if (proof.reason.isNotEmpty()) {
                        Text(
                            proof.reason,
                            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                            modifier = Modifier.testTag("release-step-reason:$index"),
                        )
                    }
                }
            }
        }
        if (contract != null) {
            Text(contract.releaseThen, style = OrreryTokens.Type.small)
        }
    }
}

/**
 * The report: its heading alone and dim until the epic has one. Once it has one, the section is reached and shows the
 * outcome, the Requirement ids shipped and not shipped, each list drawn only when it has an id, and why.
 */
@Composable
fun Report(report: EpicReportRow?) {
    ProofSection(name = "report", title = "Report", reached = report != null) {
        if (report != null) {
            Text(report.outcome, style = OrreryTokens.Type.body, modifier = Modifier.testTag("report-outcome"))
            if (report.shipped.isNotEmpty()) {
                Text(
                    "shipped ${report.shipped.joinToString(", ")}",
                    style = OrreryTokens.Type.small,
                    modifier = Modifier.testTag("report-shipped"),
                )
            }
            if (report.notShipped.isNotEmpty()) {
                Text(
                    "not shipped ${report.notShipped.joinToString(", ")}",
                    style = OrreryTokens.Type.small,
                    modifier = Modifier.testTag("report-not-shipped"),
                )
            }
            Text(report.why, style = OrreryTokens.Type.small, modifier = Modifier.testTag("report-why"))
        }
    }
}

/**
 * One proof section, tagged "proof:[name]". It is drawn dim until [reached], and its state description says which.
 */
@Composable
private fun ProofSection(name: String, title: String, reached: Boolean, content: @Composable () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("proof:$name")
            .semantics { stateDescription = if (reached) "reached" else "dim" }
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
    ) {
        // A dim section draws its text in the lower ink, not with opacity, as the web's .proof.dim does.
        CompositionLocalProvider(LocalContentColor provides (if (reached) OrreryTokens.Ink.one else OrreryTokens.Ink.four)) {
            Text(title.inLabelCase(), style = OrreryTokens.Type.label)
            content()
        }
    }
}

/**
 * The tap of a proof card: opens [phase] of the first wedge naming the requirement. It is null when there is no
 * callback or no wedge names the requirement, so the card has no click action.
 */
private fun openOf(
    requirement: EpicRequirementRow,
    wedges: List<Wedge>,
    onOpenPhase: ((task: String, phase: Phase) -> Unit)?,
    phase: Phase,
): (() -> Unit)? {
    val open = onOpenPhase ?: return null
    val wedge = wedges.firstOrNull { requirement.id in it.requirements } ?: return null
    return { open(wedge.task, phase) }
}

/**
 * One requirement's card in a proof section, tagged "proof-card:[name]:[index]": its id, text, kind when it has one,
 * its conclusion when it has one, and its runs of [phase]. It runs [onOpen] when it is tapped.
 */
@Composable
private fun ProofCard(
    name: String,
    index: Int,
    requirement: EpicRequirementRow,
    phase: String,
    loadArtifact: ArtifactLoader,
    onOpen: (() -> Unit)?,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("proof-card:$name:$index")
            .then(if (onOpen != null) Modifier.clickable(onClick = onOpen) else Modifier)
            .padding(vertical = OrreryTokens.Space.four + OrreryTokens.Space.two),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            Text(requirement.id, style = OrreryTokens.Type.body)
            Text(requirement.text, modifier = Modifier.weight(1f), style = OrreryTokens.Type.small)
            if (requirement.kind.isNotEmpty()) {
                Text(requirement.kind, style = OrreryTokens.Type.small)
            }
        }
        if (requirement.conclusion.isNotEmpty()) {
            Text(requirement.conclusion, style = OrreryTokens.Type.small)
        }
        EvidenceRuns(
            requirement = index,
            runs = requirement.runs.filter { it.phase == phase },
            loadArtifact = loadArtifact,
        )
    }
}
