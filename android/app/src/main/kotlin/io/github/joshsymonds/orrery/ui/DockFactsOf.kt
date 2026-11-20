package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ConversationItem
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.currentTool

/**
 * The dock's facts for one screen: the pane's state, changed and tool for a pane or shell, the stream's interrupt,
 * turn and running-call facts for a session or step, and the stream's newest interrupted item and request's put-off.
 */
fun dockFactsOf(
    kind: DockKind,
    name: String,
    pane: Pane?,
    streamed: StreamedConversation?,
    reason: String = "",
    step: DockStep? = null,
): DockFacts {
    val items = streamed?.conversation?.items.orEmpty()
    return DockFacts(
        kind = kind,
        name = name,
        state = pane?.state.orEmpty(),
        interrupting = streamed?.interrupting == true,
        interruptingSince = streamed?.interruptingSince ?: 0L,
        turnSince = streamed?.turnSince ?: 0L,
        tool = when (kind) {
            DockKind.PANE, DockKind.SHELL -> pane?.currentTool()?.let { DockTool(it.name, it.input, it.start) }
            DockKind.SESSION, DockKind.STEP -> runningCall(items)
        },
        changed = pane?.changed ?: 0L,
        reason = reason,
        interruptedAt = items.lastOrNull()?.takeIf { it.kind == "interrupted" }?.time ?: 0L,
        putOff = (streamed?.request?.putOff ?: false) || (pane?.putOff ?: false),
        step = step,
    )
}

/**
 * The call still running at the stream's end: walking back over the trailing tool and result items, the newest call
 * whose id no later result carries. Any other item ends the walk, and a run with every call answered has none.
 */
private fun runningCall(items: List<ConversationItem>): DockTool? {
    val answered = mutableSetOf<String>()
    for (item in items.asReversed()) {
        when (item.kind) {
            "result" -> answered += item.id
            "tool" -> if (item.id !in answered) return DockTool(item.tool, item.input, item.time)
            else -> return null
        }
    }
    return null
}
