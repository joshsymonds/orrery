package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

private val GOAL_RING = OrreryTokens.Space.eight
private val GOAL_RING_TOP = OrreryTokens.Space.four

/**
 * The pane's active goal on one compact line: a mauve ring and "goal", then the goal's text cut
 * to one line with an ellipsis. A tap opens the whole text in place and a second tap folds it
 * again; a new goal starts folded. An empty [goal] draws nothing and takes no space.
 */
@Composable
fun GoalLine(goal: String, modifier: Modifier = Modifier) {
    if (goal.isEmpty()) return
    var open by remember(goal) { mutableStateOf(false) }
    Row(
        modifier = modifier
            .testTag("goal-line")
            .fillMaxWidth()
            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
            .clickable { open = !open }
            .semantics { stateDescription = if (open) "expanded" else "collapsed" }
            .padding(horizontal = OrreryTokens.Space.eight + OrreryTokens.Space.two, vertical = OrreryTokens.Space.four),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Box(
            modifier = Modifier
                .padding(top = GOAL_RING_TOP)
                .size(GOAL_RING)
                .border(OrreryTokens.Size.hairline, OrreryTokens.Role.you, CircleShape),
        )
        Text(
            text = "goal".inLabelCase(),
            color = OrreryTokens.Role.you,
            style = OrreryTokens.Type.label.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
        Text(
            text = goal,
            modifier = Modifier.weight(1f).testTag("goal-text"),
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.small,
            maxLines = if (open) Int.MAX_VALUE else 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            text = "›",
            modifier = Modifier
                .width(OrreryTokens.Space.eight)
                .graphicsLayer { rotationZ = if (open) 90f else 0f },
            color = OrreryTokens.Ink.four,
            style = OrreryTokens.Type.small,
            maxLines = 1,
        )
    }
}
