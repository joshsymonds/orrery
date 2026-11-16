package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.unit.dp

/** One crumb of a title row: its [label], and the [onGo] that tapping it calls. */
data class Crumb(val label: String, val onGo: (() -> Unit)? = null)

/**
 * The title row every epic page shares: back, the crumbs, the state, the blocker, then [actions]. Back and every crumb
 * but the last are tappable; the last crumb is the page itself. The state and the blocker show only when given.
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
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        TextButton(onClick = onBack, modifier = Modifier.testTag("title-back")) {
            Text("‹")
        }
        Row(modifier = Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
            crumbs.forEachIndexed { index, crumb ->
                if (index > 0) {
                    Text("›", modifier = Modifier.padding(horizontal = 4.dp))
                }
                if (index == last) {
                    Text(crumb.label, modifier = Modifier.testTag("crumb:$index"))
                } else {
                    TextButton(onClick = { crumb.onGo?.invoke() }, modifier = Modifier.testTag("crumb:$index")) {
                        Text(crumb.label)
                    }
                }
            }
        }
        if (state.isNotEmpty()) {
            Text(state, modifier = Modifier.testTag("title-state"))
        }
        if (blocker.isNotEmpty()) {
            Text(blocker, color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("title-blocker"))
        }
        Row(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
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
