package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.Conversation
import io.github.joshsymonds.orrery.api.ConversationDelta
import io.github.joshsymonds.orrery.api.ConversationItem
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.PendingItem
import io.github.joshsymonds.orrery.api.QueuedItem
import io.github.joshsymonds.orrery.api.SessionRequest
import io.github.joshsymonds.orrery.api.UnfinishedText

/**
 * What a screen holds of one conversation stream: the transcript and seq to resume from, the conversation, the pane,
 * the unfinished text of each open block of a headless session, the open request, whether the owner's interrupt is
 * recorded and the session still works (with its interrupt and turn times), the caller events held for the session,
 * the owner's queued messages, and the prompt ids of those messages the stream has served as canceled or discarded.
 */
data class StreamedConversation(
    val transcript: String,
    val seq: Long,
    val conversation: Conversation,
    val pane: Pane?,
    val unfinished: List<UnfinishedText> = emptyList(),
    val request: SessionRequest? = null,
    val interrupting: Boolean = false,
    val interruptingSince: Long = 0L,
    val turnSince: Long = 0L,
    val pending: List<PendingItem> = emptyList(),
    val queued: List<QueuedItem> = emptyList(),
    val unsent: List<String> = emptyList(),
)

/**
 * The items the transcript draws: the held items without each prompt item whose prompt id the queue holds in any state,
 * or that is in the unsent set. A queued prompt is drawn in the dock until the stream drops it from the queue, and a
 * canceled or discarded one is never drawn as sent.
 */
fun StreamedConversation.shownItems(): List<ConversationItem> {
    val hidden = (queued.map { it.promptId } + unsent).toSet()
    return conversation.items.filterNot { it.kind == "prompt" && it.promptId.isNotEmpty() && it.promptId in hidden }
}

/**
 * Folds [delta] into what the screen holds. A reset, or a delta applied to nothing held, holds the delta's items and
 * unfinished set and takes its state, or empty fields when the state is null. Any other delta appends its items and
 * replaces the unfinished set with its own, even an empty one. A set state replaces origin, goal, waiting, context,
 * pane, request, the interrupt facts and pending while a null state keeps what is held. Every delta replaces the queued
 * set with its own. The unsent set grows by the prompt ids of the delta's canceled and discarded messages and keeps the
 * ones it held. Items are trimmed to the newest [keep]. The result takes the delta's transcript and seq.
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
            unfinished = delta.unfinished,
            request = state?.request,
            interrupting = state?.interrupting ?: false,
            interruptingSince = state?.interruptingSince ?: 0L,
            turnSince = state?.turnSince ?: 0L,
            pending = state?.pending ?: emptyList(),
            queued = delta.queued,
            unsent = unsentIn(delta.queued),
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
        unfinished = delta.unfinished,
        request = if (state != null) state.request else held.request,
        interrupting = if (state != null) state.interrupting else held.interrupting,
        interruptingSince = if (state != null) state.interruptingSince else held.interruptingSince,
        turnSince = if (state != null) state.turnSince else held.turnSince,
        pending = if (state != null) state.pending else held.pending,
        queued = delta.queued,
        unsent = held.unsent + unsentIn(delta.queued).filter { it !in held.unsent },
    )
}

/** The prompt ids of the messages in [queued] that are canceled or discarded, in order. */
private fun unsentIn(queued: List<QueuedItem>): List<String> =
    queued.filter { it.state == "canceled" || it.state == "discarded" }.map { it.promptId }
