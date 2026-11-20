package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.semantics.error
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

/** What an invalid field says to assistive technology. */
private const val INVALID_DESCRIPTION = "invalid"

/**
 * A single-line text field under its [label]. The placeholder shows while [value] is empty. An [error] field draws
 * its edge in the bad role and carries the error semantics on the editable node itself.
 */
@Composable
fun Field(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    placeholder: String = "",
    error: Boolean = false,
    keyboardType: KeyboardType = KeyboardType.Text,
) {
    val interactionSource = remember { MutableInteractionSource() }
    val focused by interactionSource.collectIsFocusedAsState()
    val edge = when {
        error -> OrreryTokens.Role.bad
        focused -> OrreryTokens.Role.focus
        else -> OrreryTokens.Line.firm
    }
    val shape = RoundedCornerShape(OrreryTokens.Radius.two)

    Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four)) {
        Text(label.inLabelCase(), color = OrreryTokens.Ink.two, style = OrreryTokens.Type.label)
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            modifier = modifier
                .fillMaxWidth()
                .background(OrreryTokens.Surface.well, shape)
                .border(OrreryTokens.Size.hairline, edge, shape)
                .semantics { if (error) this.error(INVALID_DESCRIPTION) },
            textStyle = OrreryTokens.Type.body.copy(color = OrreryTokens.Ink.one),
            cursorBrush = SolidColor(OrreryTokens.Role.focus),
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = keyboardType),
            interactionSource = interactionSource,
            decorationBox = { inner ->
                Box(
                    modifier = Modifier.padding(
                        horizontal = OrreryTokens.Space.twelve,
                        vertical = OrreryTokens.Space.eight,
                    ),
                ) {
                    if (value.isEmpty()) {
                        Text(placeholder, color = OrreryTokens.Ink.three, style = OrreryTokens.Type.body)
                    }
                    inner()
                }
            },
        )
    }
}
