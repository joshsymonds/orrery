package io.github.joshsymonds.orrery.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.LlmAccount
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The confirm sheet's root node: the scrim and the sheet over the inbox, which Use opens. */
internal const val USAGE_CONFIRM_TAG = "usage-confirm"

/** The confirm sheet's Cancel, which dismisses it. */
internal const val USAGE_CONFIRM_CANCEL_TAG = "usage-confirm-cancel"

/** The confirm sheet's Move button, which makes the moves. */
internal const val USAGE_CONFIRM_MOVE_TAG = "usage-confirm-move"

/** The line a refused or unfinished move leaves in the sheet. */
internal const val USAGE_CONFIRM_REFUSAL_TAG = "usage-confirm-refusal"

/**
 * The move the confirm sheet asks about: the account it moves [contexts] to, and the names of the accounts the contexts
 * move from, by their ids.
 */
internal class UsePlan(val target: LlmAccount, val contexts: List<MovingContext>, val names: Map<String, String>)

/**
 * The confirm sheet Use opens: a scrim over the inbox, and the sheet on the side's ground under its grab bar. It asks
 * "Move to <target>?", names each source's contexts with the account they move from, and says that their sessions send
 * their next request from the target. [refusal] is what a move that did not land every context said, shown under those
 * lines. Cancel, a tap on the scrim and the back gesture call [onCancel]; Move to the target calls [onMove].
 */
@Composable
internal fun UsageConfirmSheet(
    plan: UsePlan,
    refusal: String?,
    onCancel: () -> Unit,
    onMove: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val target = plan.target.name
    BackHandler(onBack = onCancel)
    Column(modifier = modifier.fillMaxSize().testTag(USAGE_CONFIRM_TAG)) {
        Box(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                .background(Color.Black.copy(alpha = OrreryTokens.Alpha.scrim))
                .clickable(onClick = onCancel),
        )
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .background(
                    OrreryTokens.Surface.overlay,
                    RoundedCornerShape(topStart = OrreryTokens.Radius.three, topEnd = OrreryTokens.Radius.three),
                )
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.sixteen),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Box(
                modifier = Modifier
                    .align(Alignment.CenterHorizontally)
                    .size(OrreryTokens.Size.controlDesk, OrreryTokens.Size.meterBarHeight)
                    .clip(CircleShape)
                    .background(OrreryTokens.Line.firm),
            )
            Text("Move to $target?", style = OrreryTokens.Type.title, color = OrreryTokens.Ink.one)
            Text(movingWordsOf(plan.contexts, plan.names), style = OrreryTokens.Type.body, color = OrreryTokens.Ink.two)
            Text(
                "Their sessions send their next request from $target.",
                style = OrreryTokens.Type.body,
                color = OrreryTokens.Ink.two,
            )
            refusal?.let { line ->
                Text(
                    line,
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Role.attention,
                    modifier = Modifier.testTag(USAGE_CONFIRM_REFUSAL_TAG),
                )
            }
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.End,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                TextButton(onClick = onCancel, modifier = Modifier.testTag(USAGE_CONFIRM_CANCEL_TAG)) {
                    Text("Cancel")
                }
                TextButton(onClick = onMove, modifier = Modifier.testTag(USAGE_CONFIRM_MOVE_TAG)) {
                    Text("Move to $target")
                }
            }
        }
    }
}
