package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

/** A section's title in label case, then its [count] when there is one, the two on one baseline. */
@Composable
fun SectionHead(title: String, modifier: Modifier = Modifier, count: String = "") {
    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            title.inLabelCase(),
            modifier = Modifier.alignByBaseline(),
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.label,
        )
        if (count.isNotEmpty()) {
            Text(
                count,
                modifier = Modifier.alignByBaseline(),
                color = OrreryTokens.Ink.three,
                style = OrreryTokens.Type.small,
            )
        }
    }
}
