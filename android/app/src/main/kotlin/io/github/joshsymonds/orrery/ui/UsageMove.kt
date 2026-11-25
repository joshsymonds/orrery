package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.LlmRoutes
import io.github.joshsymonds.orrery.api.LlmWrite
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

/** The line a move or an Undo that landed nothing says. */
internal const val NOTHING_MOVED = "Nothing moved: the contexts no longer use the accounts they were on."

/** The reason a call that did not answer gives. */
private const val NO_ANSWER = "The server did not answer."

/** The line that says what moved could not be told, after the routes did not answer. */
private const val COULD_NOT_TELL = "Could not tell what moved: the routes did not answer."

/** The toast's root node, and its line, Undo and the line a failed Undo leaves under it. */
internal const val MOVE_TOAST_TAG = "move-toast"
internal const val MOVE_TOAST_LINE_TAG = "move-toast-line"
internal const val MOVE_TOAST_UNDO_TAG = "move-toast-undo"
internal const val MOVE_TOAST_FAILED_TAG = "move-toast-failed"

/**
 * What a move or an Undo did to the contexts it named. [Done]: every call it made landed, and [landed] are the contexts it
 * moved; a context it skipped is not among them. [Stopped]: a call did not land, and the routes were read after it, so
 * [landed] hold their target account now and [notLanded] do not; [reason] says why the call stopped. [CouldNotTell]: a call
 * did not land and the routes could not be read after it, so what moved is not known; [reason] says why it stopped.
 */
internal sealed interface MoveResult {
    data class Done(val landed: List<MovingContext>) : MoveResult

    data class Stopped(
        val landed: List<MovingContext>,
        val notLanded: List<MovingContext>,
        val reason: String,
    ) : MoveResult

    data class CouldNotTell(val reason: String) : MoveResult
}

/** One SetContextAccount a move or an Undo makes: [context] is set to the account [to]. */
private class Call(val context: MovingContext, val to: String)

/**
 * Moves the contexts of [plan] to the account [target], in plan order, with one SetContextAccount for each. A context whose
 * account in [latest] is empty, or no longer its planned account, is skipped. The moves stop at the first call that does not
 * land, and the routes are then read to say what moved. Makes blocking calls, so it runs off the main thread.
 */
internal fun moveContexts(
    client: OrreryClient,
    plan: List<MovingContext>,
    target: String,
    latest: LlmRoutes,
): MoveResult = runCalls(client, plan.map { Call(it, target) }) { call ->
        val account = accountOf(latest, call.context.context)
        account.isEmpty() || account != call.context.from
    }

/**
 * Sets each of [moved] back to the account it was on, in order, with the same stop and report as [moveContexts]. Makes
 * blocking calls, so it runs off the main thread.
 */
internal fun undoMoves(client: OrreryClient, moved: List<MovingContext>): MoveResult =
    runCalls(client, moved.map { Call(it, it.from) }) { false }

/** Makes the calls that [skip] does not skip, in order, and stops at the first that does not land. */
private fun runCalls(client: OrreryClient, calls: List<Call>, skip: (Call) -> Boolean): MoveResult {
    val landed = mutableListOf<MovingContext>()
    for (call in calls) {
        if (skip(call)) continue
        val reason = stopReasonOf(client, call)
        if (reason != null) return stoppedAt(client, calls, reason)
        landed += call.context
    }
    return MoveResult.Done(landed)
}

/** Why [call] did not land, or null when it landed. */
private fun stopReasonOf(client: OrreryClient, call: Call): String? = try {
    when (val write = client.setContextAccount(call.context.context, call.to)) {
        is LlmWrite.Done -> null
        is LlmWrite.Refused -> "The server refused it: ${write.reason}"
        LlmWrite.NoAnswer -> NO_ANSWER
    }
} catch (error: IOException) {
    NO_ANSWER
}

/** Reads the routes after a call that did not land, and says which of [calls] now hold their target account. */
private fun stoppedAt(client: OrreryClient, calls: List<Call>, reason: String): MoveResult {
    val routes = try {
        client.llmRoutes()
    } catch (error: IOException) {
        return MoveResult.CouldNotTell(reason)
    }
    val (landed, notLanded) = calls.partition { accountOf(routes, it.context.context) == it.to }
    return MoveResult.Stopped(landed.map { it.context }, notLanded.map { it.context }, reason)
}

/** The account [context] uses in [routes], empty when the routes do not name it. */
private fun accountOf(routes: LlmRoutes, context: String): String =
    routes.contexts.firstOrNull { it.id == context }?.account.orEmpty()

/**
 * The words a move or an Undo that did not land every context shows, in the confirm sheet or under the toast: what moved and
 * what did not, then why it stopped, or the could-not-tell words. [undo] words the contexts as moved back. A
 * [MoveResult.Done] that landed nothing says [NOTHING_MOVED].
 */
internal fun unfinishedWordsOf(result: MoveResult, undo: Boolean): String = when (result) {
    is MoveResult.Done -> NOTHING_MOVED
    is MoveResult.Stopped -> {
        val movedWord = if (undo) "Moved back" else "Moved"
        val notMovedWord = if (undo) "Did not move back" else "Did not move"
        listOfNotNull(
            result.landed.takeIf { it.isNotEmpty() }?.let { "$movedWord: ${namesOf(it)}." },
            result.notLanded.takeIf { it.isNotEmpty() }?.let { "$notMovedWord: ${namesOf(it)}." },
            result.reason,
        ).joinToString(" ")
    }
    is MoveResult.CouldNotTell -> "${result.reason} $COULD_NOT_TELL"
}

/**
 * What the confirm sheet says the contexts of [contexts] do: one sentence for each account they move from, naming its
 * contexts with the account's name from [names], as "personal and sandbox move from max-1." A source with one context
 * takes "moves".
 */
internal fun movingWordsOf(contexts: List<MovingContext>, names: Map<String, String>): String =
    contexts.groupBy { it.from }.entries.joinToString(" ") { (from, sources) ->
        "${namesOf(sources)} ${if (sources.size == 1) "moves" else "move"} from ${names[from] ?: from}."
    }

/** The names of [contexts] in a sentence: "a", "a and b", "a, b and c". [contexts] is never empty. */
private fun namesOf(contexts: List<MovingContext>): String {
    val names = contexts.map { it.context }
    return if (names.size == 1) names.single() else names.dropLast(1).joinToString(", ") + " and " + names.last()
}

/** The move the toast is for: the contexts it moved, and the name of the account they now use. */
internal class MovedEntry(val moved: List<MovingContext>, val target: String)

/**
 * The toast's state: the move it is for, null when no toast shows, and the line a failed Undo left under it. A [show] replaces
 * both, so one move's toast never carries another's failure.
 */
internal class MoveToastState {
    internal var shown by mutableStateOf<MovedEntry?>(null)
    internal var failure by mutableStateOf<String?>(null)

    /** Shows the toast for [entry]. */
    internal fun show(entry: MovedEntry) {
        shown = entry
        failure = null
    }

    /** Ends the toast when it still shows [entry], the move Undo has brought back. */
    internal fun undone(entry: MovedEntry) {
        if (shown === entry) {
            shown = null
            failure = null
        }
    }

    /** Shows [line] under the toast when it still shows [entry], the move whose Undo did not go through. */
    internal fun undoFailed(entry: MovedEntry, line: String) {
        if (shown === entry) failure = line
    }
}

/** The toast state the composition remembers, for the page that shows the toast. */
@Composable
internal fun rememberMoveToast(): MoveToastState = remember { MoveToastState() }

/**
 * The toast a move that landed shows: one line, `Moved <contexts> to <account>.`, and Undo at its end. Undo makes the moves
 * back, each context to the account it was on. A landed Undo ends the toast. An Undo that does not land keeps the toast and
 * its Undo, and says what moved back and what did not under the line. [onUndid] runs after every Undo, since an Undo that
 * stopped may still have moved some contexts, so the routes are read again. Nothing ends the toast on a timer.
 */
@Composable
internal fun MoveToast(
    state: MoveToastState,
    client: OrreryClient,
    onUndid: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val scope = rememberCoroutineScope()
    val entry = state.shown ?: return
    val failure = state.failure
    val shape = RoundedCornerShape(OrreryTokens.Radius.two)
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(start = OrreryTokens.Space.sixteen, end = OrreryTokens.Space.sixteen, bottom = OrreryTokens.Space.sixteen)
            .testTag(MOVE_TOAST_TAG)
            .shadow(
                elevation = OrreryTokens.Elevation.Overlay.y,
                shape = shape,
                ambientColor = OrreryTokens.Elevation.Overlay.color.copy(alpha = OrreryTokens.Elevation.Overlay.alpha),
                spotColor = OrreryTokens.Elevation.Overlay.color.copy(alpha = OrreryTokens.Elevation.Overlay.alpha),
            )
            .background(OrreryTokens.Surface.pressed, shape)
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.twelve),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                "Moved ${namesOf(entry.moved)} to ${entry.target}.",
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.one,
                modifier = Modifier.weight(1f).testTag(MOVE_TOAST_LINE_TAG),
            )
            TextButton(
                onClick = {
                    scope.launch {
                        val result = withContext(Dispatchers.IO) { undoMoves(client, entry.moved) }
                        if (result is MoveResult.Done) {
                            state.undone(entry)
                        } else {
                            state.undoFailed(entry, unfinishedWordsOf(result, undo = true))
                        }
                        onUndid()
                    }
                },
                modifier = Modifier.testTag(MOVE_TOAST_UNDO_TAG),
            ) {
                Text("Undo")
            }
        }
        failure?.let { line ->
            Text(
                line,
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Role.attention,
                modifier = Modifier.testTag(MOVE_TOAST_FAILED_TAG),
            )
        }
    }
}
