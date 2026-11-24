package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.DesignContractRow
import io.github.joshsymonds.orrery.api.DesignQuestionRow
import io.github.joshsymonds.orrery.api.DesignRecordRow

/** The tag of a design page's root node (O.designPage and O.designBody in design.js). */
internal const val DESIGN_PAGE_TAG = "design-page"

/** The tag prefix of a stage of the design's stage strip, followed by the stage's name (O.dtrack in design.js). */
internal const val DESIGN_STAGE_TAG_PREFIX = "design-stage:"

/**
 * The tag prefix of a section of the design page, followed by the section's name: now, decided, contracts, steelman,
 * approach, questions, research or log (the sections of O.designBody in design.js).
 */
internal const val DESIGN_SECTION_TAG_PREFIX = "design-section:"

/**
 * A design's page, the record round 8 draws for a design (O.designPage and O.designBody in design.js): what was asked for,
 * the stage strip, what waits on the owner now, the decisions the Director made for him, the contracts in start order, the
 * steelman, the approach, the questions, the research and the log. [record] is the design and what it holds, and [now] is
 * the time the page is read at, in unix seconds. [thread] draws a question's thread wherever a question is shown, and
 * [onOpenContract] opens a contract of the design. The root node carries [DESIGN_PAGE_TAG].
 */
@Composable
internal fun DesignPage(
    record: DesignRecordRow,
    now: Long,
    thread: @Composable (DesignQuestionRow) -> Unit,
    onOpenContract: (DesignContractRow) -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(modifier.testTag(DESIGN_PAGE_TAG)) {}
}
