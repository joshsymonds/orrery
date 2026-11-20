package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * A row of a list: [title] on its first line with [titleExtra] after it, [supporting] under it when not empty, and
 * [end] at the right. With [onClick] the whole row is one button.
 */
@Composable
fun ListItem(
    title: String,
    modifier: Modifier = Modifier,
    supporting: String = "",
    onClick: (() -> Unit)? = null,
    titleExtra: @Composable RowScope.() -> Unit = {},
    end: @Composable RowScope.() -> Unit = {},
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .then(if (onClick != null) Modifier.clickable(role = Role.Button, onClick = onClick) else Modifier)
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.twelve),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve),
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                Text(title, color = OrreryTokens.Ink.one, style = OrreryTokens.Type.body)
                titleExtra()
            }
            if (supporting.isNotEmpty()) {
                Text(supporting, color = OrreryTokens.Ink.two, style = OrreryTokens.Type.small)
            }
        }
        end()
    }
}
