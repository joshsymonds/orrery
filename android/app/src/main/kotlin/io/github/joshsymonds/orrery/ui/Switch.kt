package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.SwitchDefaults
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import androidx.compose.material3.Switch as MaterialSwitch

/** A toggle named [label] for assistive technology. Its track takes the primary role when [checked]. */
@Composable
fun Switch(
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
) {
    MaterialSwitch(
        checked = checked,
        onCheckedChange = onCheckedChange,
        modifier = modifier.semantics { contentDescription = label },
        colors = SwitchDefaults.colors(
            checkedThumbColor = OrreryTokens.Ink.onAccent,
            checkedTrackColor = OrreryTokens.Role.primary,
            checkedBorderColor = OrreryTokens.Role.primary,
            uncheckedThumbColor = OrreryTokens.Ink.three,
            uncheckedTrackColor = OrreryTokens.Surface.raised,
            uncheckedBorderColor = OrreryTokens.Line.firm,
        ),
    )
}
