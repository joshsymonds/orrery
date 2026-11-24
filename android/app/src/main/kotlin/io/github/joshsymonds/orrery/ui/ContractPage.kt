package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.ContractRecordRow
import io.github.joshsymonds.orrery.api.ContractStageRow
import io.github.joshsymonds.orrery.api.DesignQuestionRow

/** The tag of a contract page's root node (O.contractPage and O.contractBody in contract.js). */
internal const val CONTRACT_PAGE_TAG = "contract-page"

/**
 * The tag prefix of a section of the contract page, followed by the section's name, in the document's order (the ten
 * sections of O.contractBody in contract.js).
 */
internal const val CONTRACT_SECTION_TAG_PREFIX = "contract-section:"

/** The tag of the contract's length meter, drawn above its checks (O.lengthMeter in design.js). */
internal const val CONTRACT_LENGTH_TAG = "contract-length"

/** The tag prefix of an addition's Keep control, followed by the addition's id (the Keep of O.contractBody in contract.js). */
internal const val ADDITION_KEEP_TAG_PREFIX = "addition-keep:"

/** The tag prefix of an addition's Strike control, followed by the addition's id (the Strike of O.contractBody in contract.js). */
internal const val ADDITION_STRIKE_TAG_PREFIX = "addition-strike:"

/**
 * The tag prefix of a step's "none" undo mark, followed by the step's row id, on a step that cannot be undone (the
 * undo column of O.contractBody in contract.js).
 */
internal const val STEP_UNDO_NONE_TAG_PREFIX = "step-undo-none:"

/**
 * A contract's page, drawn in its design's pane, the record round 8 draws for a contract (O.contractPage and O.contractBody in
 * contract.js): the decision line, the work once accepted, and the ten sections of its document in order, each row with its
 * controls and its refusals. [record] is the contract and its versions and states, and [stage] what it is being written
 * with. [questions] are the design's questions that concern it, and [thread] draws one of them. [onKeep] and [onStrike] take
 * an addition's id, and [onShown] reports the version and the rows the owner was shown. [onAbout], when given, opens a
 * row's About. [work] draws the work once the contract is accepted and [foot] the accept bar, both when given. The root node
 * carries [CONTRACT_PAGE_TAG].
 */
@Composable
internal fun ContractPage(
    record: ContractRecordRow,
    stage: ContractStageRow,
    questions: List<DesignQuestionRow>,
    onKeep: (String) -> Unit,
    onStrike: (String) -> Unit,
    onShown: (Long, List<String>) -> Unit,
    onAbout: ((RowAbout) -> Unit)?,
    thread: @Composable (DesignQuestionRow) -> Unit,
    work: (@Composable () -> Unit)?,
    foot: @Composable () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(modifier.testTag(CONTRACT_PAGE_TAG)) {}
}
