package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.LlmAccount
import io.github.joshsymonds.orrery.api.LlmAccounts
import io.github.joshsymonds.orrery.api.LlmContext
import io.github.joshsymonds.orrery.api.LlmRoutes
import io.github.joshsymonds.orrery.api.LlmSeat
import io.github.joshsymonds.orrery.api.LlmWrite
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

/** The page's note when a write got no answer: the accounts are read again, and show what stands. */
private const val NO_ANSWER = "Could not tell whether that landed: no answer came. This is what stands now."

/** The name of the login a context with no account sends its forward seats with. */
private const val OWN_LOGIN = "the session's own login"

/** The auth mode whose seats go to an AWS region rather than to an upstream URL. */
private const val SIGV4 = "sigv4"

/** The end of a refusal's note about a removal that is in use. */
private const val MOVE_FIRST = "Move them to another account first, then remove it. Nothing changed."

/** The sheet an act of the accounts page is open on. */
sealed interface ActSheet {
    /** An account's ⋯: its Rename and Remove. */
    data class Menu(val account: LlmAccount) : ActSheet

    /** Rename: the account's new name, sent on its Rename. */
    data class Rename(val account: LlmAccount) : ActSheet

    /** Remove's ask, which sends nothing until its Remove is tapped. */
    data class ConfirmRemove(val account: LlmAccount) : ActSheet

    /** A context's ›: the session's own login, then every account, one of which the context sends with. */
    data class Pick(val context: LlmContext) : ActSheet

    /** The Seats row's ›: every seat. */
    data object Seats : ActSheet
}

/**
 * The acts of the accounts page: the writes its sheets send, and what each answer does. A Done is applied to the page
 * through [onRenamed], [onRemoved] or [onRouted]. A refusal is worded for the sheet it came from. A write that got no
 * answer, or never reached the server, closes its sheet, notes that nothing is known, and calls [onNoAnswer], which
 * reads the accounts again. A send runs in [scope], so dismissing its sheet does not cancel it.
 *
 * [sheet] is the sheet open now, [note] what it says after a refusal, and [sending] whether one of its sends is out.
 * An answer to a send whose sheet has since closed still applies to the page, but says nothing on another sheet.
 */
class AccountActs internal constructor(
    private val client: OrreryClient,
    private val scope: CoroutineScope,
    private val onRenamed: (LlmAccount) -> Unit,
    private val onRemoved: (String) -> Unit,
    private val onRouted: (LlmRoutes) -> Unit,
    private val onNoAnswer: () -> Unit,
) {
    var sheet: ActSheet? by mutableStateOf(null)
        private set

    var note: String? by mutableStateOf(null)
        private set

    var sending: Boolean by mutableStateOf(false)
        private set

    /** The page's note after a write got no answer. The next sheet that opens clears it. */
    var notice: String? by mutableStateOf(null)
        private set

    /** Counts each sheet opened or closed, so a send can tell whether its sheet is still the one open. */
    private var opened = 0

    fun open(next: ActSheet) {
        opened++
        sheet = next
        note = null
        sending = false
        notice = null
    }

    fun close() {
        opened++
        sheet = null
        note = null
        sending = false
    }

    fun rename(account: LlmAccount, name: String) {
        val token = begin() ?: return
        scope.launch {
            when (val answer = write { client.renameAccount(account.id, name) }) {
                is LlmWrite.Done -> {
                    onRenamed(answer.answer)
                    finish(token)
                }
                is LlmWrite.Refused -> refuse(token, "Not renamed: ${answer.reason} Nothing changed.")
                LlmWrite.NoAnswer -> noAnswer(token)
            }
        }
    }

    fun remove(account: LlmAccount) {
        val token = begin() ?: return
        scope.launch {
            when (val answer = write { client.removeAccount(account.id) }) {
                is LlmWrite.Done -> {
                    onRemoved(account.id)
                    finish(token)
                }
                is LlmWrite.Refused -> {
                    val inUse = if (answer.code == Code.FAILED_PRECONDITION) inUseNote(account) else null
                    refuse(token, inUse ?: "Not removed: ${answer.reason} Nothing changed.")
                }
                LlmWrite.NoAnswer -> noAnswer(token)
            }
        }
    }

    /** Sets [context]'s forward seats to send with [account], an id, or with the session's own login when empty. */
    fun setContextAccount(context: LlmContext, account: String) {
        val token = begin() ?: return
        scope.launch {
            when (val answer = write { client.setContextAccount(context.id, account) }) {
                is LlmWrite.Done -> {
                    onRouted(answer.answer)
                    finish(token)
                }
                is LlmWrite.Refused -> refuse(token, "Not set: ${answer.reason} Nothing changed.")
                LlmWrite.NoAnswer -> noAnswer(token)
            }
        }
    }

    /** A write run off the main thread. One that never reached the server gets no answer, as one that got none. */
    private suspend fun <T> write(call: () -> LlmWrite<T>): LlmWrite<T> =
        try {
            withContext(Dispatchers.IO) { call() }
        } catch (error: IOException) {
            LlmWrite.NoAnswer
        }

    /**
     * The note for a removal the server refused as in use: every context that holds the account, read afresh. Null
     * when the read fails or names none, since then the refusal's own reason is all there is to say.
     */
    private suspend fun inUseNote(account: LlmAccount): String? {
        val contexts = try {
            withContext(Dispatchers.IO) { client.llmRoutes().contexts }
        } catch (error: IOException) {
            return null
        }
        val holders = contexts.filter { it.account == account.id }.map { it.id }
        if (holders.isEmpty()) return null
        return "${account.name} is in use by ${prose(holders)}. $MOVE_FIRST"
    }

    /** Starts a send for the sheet open now, or returns null while one of its sends is out. */
    private fun begin(): Int? {
        if (sending) return null
        sending = true
        note = null
        return opened
    }

    /** The send's sheet is still open when it is done, so it closes. */
    private fun finish(token: Int) {
        if (token == opened) close()
    }

    /** The send's sheet is still open when it is refused, so the refusal is worded on it. */
    private fun refuse(token: Int, message: String) {
        if (token == opened) {
            note = message
            sending = false
        }
    }

    private fun noAnswer(token: Int) {
        finish(token)
        notice = NO_ANSWER
        onNoAnswer()
    }
}

/** Names as prose: "a", "a and b", then "a, b and c". */
private fun prose(names: List<String>): String =
    if (names.size <= 1) names.joinToString() else names.dropLast(1).joinToString(", ") + " and " + names.last()

/** The sheet [acts] has open, if any, drawn over the page whose [accounts] it acts on. */
@Composable
fun AccountActsSheet(acts: AccountActs, accounts: LlmAccounts) {
    when (val open = acts.sheet) {
        null -> Unit
        is ActSheet.Menu -> MenuSheet(open.account, acts)
        is ActSheet.Rename -> RenameSheet(open.account, acts)
        is ActSheet.ConfirmRemove -> RemoveSheet(open.account, acts)
        is ActSheet.Pick -> PickSheet(open.context, accounts.accounts, acts)
        ActSheet.Seats -> SeatsSheet(accounts.routes.seats, acts)
    }
}

/** A sheet of the accounts page: its [content] in a column under the app's bottom sheet, dismissed by [onDismiss]. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ActSheetFrame(tag: String, onDismiss: () -> Unit, content: @Composable ColumnScope.() -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            Modifier
                .fillMaxWidth()
                .testTag(tag)
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            content = content,
        )
    }
}

@Composable
private fun SheetTitle(text: String) {
    Text(text, style = OrreryTokens.Type.title, color = OrreryTokens.Ink.one)
}

/** The refusal the sheet's last send got, if any. */
@Composable
private fun SheetNote(acts: AccountActs) {
    acts.note?.let { InlineNote(it, NoteTone.Error, Modifier.testTag("act-note")) }
}

@Composable
private fun MenuSheet(account: LlmAccount, acts: AccountActs) {
    ActSheetFrame(tag = "act-menu", onDismiss = acts::close) {
        SheetTitle(account.name)
        TextButton(
            onClick = { acts.open(ActSheet.Rename(account)) },
            modifier = Modifier.fillMaxWidth().testTag("account-rename"),
        ) {
            Text("Rename")
        }
        TextButton(
            onClick = { acts.open(ActSheet.ConfirmRemove(account)) },
            modifier = Modifier.fillMaxWidth().testTag("account-remove"),
        ) {
            Text("Remove", color = OrreryTokens.Role.bad)
        }
    }
}

@Composable
private fun RenameSheet(account: LlmAccount, acts: AccountActs) {
    var name by remember(account.id) { mutableStateOf(account.name) }
    ActSheetFrame(tag = "act-rename", onDismiss = acts::close) {
        SheetTitle("Rename")
        Field(
            value = name,
            onValueChange = { name = it },
            label = "Name",
            modifier = Modifier.testTag("rename-field"),
        )
        SheetNote(acts)
        TextButton(
            onClick = { acts.rename(account, name.trim()) },
            enabled = !acts.sending && name.isNotBlank(),
            modifier = Modifier.fillMaxWidth().testTag("rename-confirm"),
        ) {
            Text("Rename")
        }
    }
}

@Composable
private fun RemoveSheet(account: LlmAccount, acts: AccountActs) {
    ActSheetFrame(tag = "act-remove", onDismiss = acts::close) {
        Text(
            "Remove ${account.name}?",
            style = OrreryTokens.Type.title,
            color = OrreryTokens.Ink.one,
            modifier = Modifier.testTag("remove-title"),
        )
        SheetNote(acts)
        TextButton(onClick = acts::close, modifier = Modifier.fillMaxWidth().testTag("remove-keep")) {
            Text("Keep")
        }
        TextButton(
            onClick = { acts.remove(account) },
            enabled = !acts.sending,
            modifier = Modifier.fillMaxWidth().testTag("remove-confirm"),
        ) {
            Text("Remove", color = OrreryTokens.Role.bad)
        }
    }
}

@Composable
private fun PickSheet(context: LlmContext, accounts: List<LlmAccount>, acts: AccountActs) {
    ActSheetFrame(tag = "act-pick", onDismiss = acts::close) {
        SheetTitle("Account for ${context.id}")
        PickRow(
            tag = "pick-own",
            title = OWN_LOGIN,
            current = context.account.isEmpty(),
            enabled = !acts.sending,
        ) {
            acts.setContextAccount(context, "")
        }
        accounts.forEach { account ->
            PickRow(
                tag = "pick-${account.id}",
                title = account.name,
                current = context.account == account.id,
                enabled = !acts.sending,
            ) {
                acts.setContextAccount(context, account.id)
            }
        }
        SheetNote(acts)
    }
}

/** One choice of the Pick sheet: a row that picks it when [enabled], marked "now" when it is the context's account. */
@Composable
private fun PickRow(tag: String, title: String, current: Boolean, enabled: Boolean, onPick: () -> Unit) {
    ListItem(
        title = title,
        supporting = if (current) "now" else "",
        modifier = Modifier.testTag(tag),
        onClick = if (enabled) onPick else null,
    )
}

@Composable
private fun SeatsSheet(seats: List<LlmSeat>, acts: AccountActs) {
    ActSheetFrame(tag = "act-seats", onDismiss = acts::close) {
        SheetTitle("Seats")
        seats.forEach { seat ->
            ListItem(title = seat.id, supporting = seatLine(seat), modifier = Modifier.testTag("seat-${seat.id}"))
        }
    }
}

/** A seat's kind, where it goes and its model, each left out when empty. A sigv4 seat goes to its AWS region. */
private fun seatLine(seat: LlmSeat): String {
    val where = if (seat.authMode == SIGV4 && seat.awsRegion.isNotEmpty()) "region ${seat.awsRegion}" else seat.upstream
    return listOf(seat.authMode, where, seat.model).filter { it.isNotEmpty() }.joinToString(" · ")
}
