package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow

private const val RED = "red"
private const val GREEN = "green"
private const val DIM_ALPHA = 0.4f

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
 * The release proof: the contract's release steps with their target and undo, then its then line. It is reached only
 * once the epic is released.
 */
@Composable
fun ReleaseProof(contract: EpicContractRow?, released: Boolean) {
    ProofSection(name = "release", title = "Release proof", reached = released) {
        contract?.releaseSteps?.forEachIndexed { index, step ->
            Column(modifier = Modifier.fillMaxWidth().testTag("release-step:$index")) {
                Text(step.step, style = MaterialTheme.typography.bodyMedium)
                Text(step.target, style = MaterialTheme.typography.bodySmall)
                Text(step.undo, style = MaterialTheme.typography.labelSmall)
            }
        }
        if (contract != null) {
            Text(contract.releaseThen, style = MaterialTheme.typography.bodySmall)
        }
    }
}

/** The report section. It has no content yet and is always drawn dim. */
@Composable
fun Report() {
    ProofSection(name = "report", title = "Report", reached = false) {}
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
            .alpha(if (reached) 1f else DIM_ALPHA)
            .padding(horizontal = 16.dp, vertical = 8.dp),
    ) {
        Text(title, style = MaterialTheme.typography.titleSmall)
        content()
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
            .padding(vertical = 6.dp),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(requirement.id, style = MaterialTheme.typography.bodyMedium)
            Text(requirement.text, modifier = Modifier.weight(1f), style = MaterialTheme.typography.bodySmall)
            if (requirement.kind.isNotEmpty()) {
                Text(requirement.kind, style = MaterialTheme.typography.labelMedium)
            }
        }
        if (requirement.conclusion.isNotEmpty()) {
            Text(requirement.conclusion, style = MaterialTheme.typography.bodySmall)
        }
        EvidenceRuns(
            requirement = index,
            runs = requirement.runs.filter { it.phase == phase },
            loadArtifact = loadArtifact,
        )
    }
}
