package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.ContractRequirementWorkRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow

/** The tag of the work's root node, the section of a contract that shows its epic's work (O.contractWork in contract.js). */
internal const val CONTRACT_WORK_TAG = "contract-work"

/** The tag prefix of one requirement's row in the work table, followed by the requirement's id (O.contractWork in contract.js). */
internal const val WORK_ROW_TAG_PREFIX = "work-row:"

/**
 * The work on a contract once it is accepted, seen from the contract's side (O.contractWork in contract.js): the contract's
 * epic as its head, with how many of its requirements are proven, then a row per requirement in the contract's order with
 * its state, the tasks working it, and its red and green proof. [work] are the requirements' rows, [runs] the evidence runs
 * of each requirement by its id, and [epic] the epic's name. [onOpenEpic] opens the epic. The root node carries
 * [CONTRACT_WORK_TAG].
 */
@Composable
internal fun ContractWork(
    work: List<ContractRequirementWorkRow>,
    runs: Map<String, List<EvidenceRunRow>>,
    epic: String,
    onOpenEpic: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(modifier.testTag(CONTRACT_WORK_TAG)) {}
}
