package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * The Report section of an epic: its heading, its outcome sentence, its why, what shipped and what did not, and the
 * epic it was superseded by.
 */
@Composable
internal fun EpicReportSection(view: ReportView) {
    SectionHeading("Report")
    Column(modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen)) {
        Text(view.sentence, style = OrreryTokens.Type.body, modifier = Modifier.testTag("report-sentence"))
        if (view.why.isNotEmpty()) {
            Text(view.why, style = OrreryTokens.Type.body, modifier = Modifier.testTag("report-why"))
        }
        if (view.shipped.isNotEmpty()) {
            Text("shipped ${view.shipped}", style = OrreryTokens.Type.small, modifier = Modifier.testTag("report-shipped"))
        }
        if (view.notShipped.isNotEmpty()) {
            Text(
                "not shipped ${view.notShipped}",
                style = OrreryTokens.Type.small,
                modifier = Modifier.testTag("report-not-shipped"),
            )
        }
        if (view.replacedBy.isNotEmpty()) {
            Text(
                "Superseded by ${view.replacedBy}",
                style = OrreryTokens.Type.small,
                modifier = Modifier.testTag("report-replaced"),
            )
        }
    }
}
