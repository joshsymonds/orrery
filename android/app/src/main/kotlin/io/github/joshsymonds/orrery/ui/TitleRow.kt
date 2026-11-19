package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** Back's width: a touch target for its one glyph, not a button's 58 dp minimum. */
private val BACK_WIDTH = OrreryTokens.Size.controlTouch

/** A tappable crumb's padding: a button's height, with little beside the text so the crumbs keep the row's width. */
private val CRUMB_PADDING = PaddingValues(horizontal = OrreryTokens.Space.four, vertical = OrreryTokens.Space.eight)

/** One crumb of a title row: its [label], and the [onGo] that tapping it calls. */
data class Crumb(val label: String, val onGo: (() -> Unit)? = null)

/**
 * The title row every epic page shares, sized for a phone. Back leads the first line, and the crumbs follow it, each
 * on one line with the separator before it. A crumb that does not fit beside the ones before it starts the next line
 * whole, and only a crumb wider than the row on its own ends in an ellipsis. Under them, the state, the blocker, then
 * [actions] wrap onto as many lines as they need. Back and every crumb but the last are tappable; the last crumb is
 * the page itself. The state and the blocker show only when given.
 *
 * One row for all of it measured the state and the actions at their full width first and left the crumbs about one
 * character on a phone, so a slug stood one letter to a line.
 */
@Composable
fun TitleRow(
    crumbs: List<Crumb>,
    onBack: () -> Unit,
    state: String = "",
    blocker: String = "",
    actions: @Composable RowScope.() -> Unit = {},
) {
    val last = crumbs.lastIndex
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = OrreryTokens.Space.eight, vertical = OrreryTokens.Space.four),
    ) {
        Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            TextButton(
                onClick = onBack,
                contentPadding = CRUMB_PADDING,
                modifier = Modifier.width(BACK_WIDTH).testTag("title-back"),
            ) {
                Text("‹")
            }
            FlowRow(
                modifier = Modifier.weight(1f),
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
                itemVerticalAlignment = Alignment.CenterVertically,
            ) {
                crumbs.forEachIndexed { index, crumb ->
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (index > 0) {
                            Text("›", modifier = Modifier.padding(end = OrreryTokens.Space.four))
                        }
                        // The separator keeps its width; a crumb wider than the row gives way and ends in an ellipsis.
                        val room = Modifier.weight(1f, fill = false).testTag("crumb:$index")
                        if (index == last) {
                            Text(crumb.label, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = room)
                        } else {
                            TextButton(
                                onClick = { crumb.onGo?.invoke() },
                                contentPadding = CRUMB_PADDING,
                                modifier = room,
                            ) {
                                Text(crumb.label, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                    }
                }
            }
        }
        FlowRow(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            itemVerticalAlignment = Alignment.CenterVertically,
        ) {
            if (state.isNotEmpty()) {
                Text(state, modifier = Modifier.testTag("title-state"))
            }
            if (blocker.isNotEmpty()) {
                Text(blocker, color = OrreryTokens.Role.attention, modifier = Modifier.testTag("title-blocker"))
            }
            actions()
        }
    }
}

/**
 * A command that asks before it acts. Tapping [label] shows [question] with [confirm] and [keep] beside it. Confirm
 * hides them and calls [onConfirm] once; keep hides them and calls nothing. Tagged [tag] for the button, "<tag>-sure"
 * for the question, "<tag>-confirm" and "<tag>-keep" for the two answers.
 */
@Composable
fun AskFirst(label: String, question: String, confirm: String, keep: String, tag: String, onConfirm: () -> Unit) {
    var asking by remember { mutableStateOf(false) }
    TextButton(onClick = { asking = true }, modifier = Modifier.testTag(tag)) {
        Text(label)
    }
    if (asking) {
        Text(question, modifier = Modifier.testTag("$tag-sure"))
        TextButton(
            onClick = {
                asking = false
                onConfirm()
            },
            modifier = Modifier.testTag("$tag-confirm"),
        ) {
            Text(confirm)
        }
        TextButton(onClick = { asking = false }, modifier = Modifier.testTag("$tag-keep")) {
            Text(keep)
        }
    }
}
