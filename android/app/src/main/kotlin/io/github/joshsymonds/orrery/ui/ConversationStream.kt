package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.Conversation
import io.github.joshsymonds.orrery.api.ConversationDelta
import io.github.joshsymonds.orrery.api.Pane

/** What a pane screen holds of one conversation stream: the transcript and seq to resume from, the conversation, and the pane. */
data class StreamedConversation(
    val transcript: String,
    val seq: Long,
    val conversation: Conversation,
    val pane: Pane?,
)

/**
 * Folds [delta] into what the screen holds. A reset, or a delta applied to nothing held, holds the delta's items and
 * takes its state, or empty fields when the state is null. Any other delta appends its items, and a set state replaces
 * origin, goal, waiting, context and pane while a null state keeps what is held. Items are trimmed to the newest [keep].
 * The result takes the delta's transcript and seq.
 */
fun StreamedConversation?.applyDelta(delta: ConversationDelta, keep: Int = 50): StreamedConversation {
    val held = this
    val state = delta.state
    if (held == null || delta.reset) {
        return StreamedConversation(
            transcript = delta.transcript,
            seq = delta.seq,
            conversation = Conversation(
                origin = state?.origin.orEmpty(),
                items = delta.items.takeLast(keep),
                goal = state?.goal.orEmpty(),
                waiting = state?.waiting ?: emptyList(),
                context = state?.context,
            ),
            pane = state?.pane,
        )
    }
    val conversation = held.conversation
    return StreamedConversation(
        transcript = delta.transcript,
        seq = delta.seq,
        conversation = Conversation(
            origin = state?.origin ?: conversation.origin,
            items = (conversation.items + delta.items).takeLast(keep),
            goal = state?.goal ?: conversation.goal,
            waiting = state?.waiting ?: conversation.waiting,
            context = if (state != null) state.context else conversation.context,
        ),
        pane = if (state != null) state.pane else held.pane,
    )
}
