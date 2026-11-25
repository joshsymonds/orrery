package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.ContractLeftRow
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.ContractStateRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The tag of the accept bar's root node, the bar at a contract's foot (O.acceptBar in contract.js). */
internal const val ACCEPT_BAR_TAG = "accept-bar"

/** The tag of the bar's Accept control, which accepts the contract at its version (the Accept of O.acceptBar in contract.js). */
internal const val ACCEPT_TAG = "accept-bar-accept"

/** The tag of the bar's Set aside control, which opens the form that sets the contract aside (O.acceptBar in contract.js). */
internal const val SET_ASIDE_TAG = "accept-bar-set-aside"

/** The tag of the bar's Reopen control, which proposes a set-aside contract again (O.acceptBar in contract.js). */
internal const val REOPEN_TAG = "accept-bar-reopen"

/** The tag of the bar's Talk about it control, which opens the conversation about the contract (O.acceptBar in contract.js). */
internal const val TALK_TAG = "accept-bar-talk"

/** The tag of the bar's Open its epic control, which opens the epic an accepted contract became (O.acceptBar in contract.js). */
internal const val OPEN_EPIC_TAG = "accept-bar-open-epic"

/** The tag of the who-tag that names who accepted or set the contract aside, carrying the maker as its semantics (O.who in design.js). */
internal const val ACCEPT_BAR_WHO_TAG = "accept-bar-who"

/** The tag of the set-aside form's words field, where the owner says why in his own words (O.asideSheet in contract.js). */
internal const val SET_ASIDE_WORDS_TAG = "accept-bar-set-aside-words"

/** The tag of the set-aside form's Set aside control, enabled once words are typed (O.asideSheet in contract.js). */
internal const val SET_ASIDE_CONFIRM_TAG = "accept-bar-set-aside-confirm"

/** The tag of the set-aside form's Keep it proposed control, which closes the form (O.asideSheet in contract.js). */
internal const val SET_ASIDE_KEEP_TAG = "accept-bar-set-aside-keep"

/**
 * What the accept bar's send is doing: [Idle] with nothing sent, [Sending] while the accept, set-aside or reopen goes to the
 * server, or [Failed] with the [reason] it did not land. The bar's "accepting…" line and its error line draw it (O.acceptBar's
 * sending and error states in contract.js).
 */
internal sealed interface BarSend {
    /** Nothing is being sent. */
    data object Idle : BarSend

    /** An accept, set-aside or reopen is on its way to the server. */
    data object Sending : BarSend

    /** The send did not land, for the [reason] given. */
    data class Failed(val reason: String) : BarSend
}

private const val ACCEPTING = "accepting…"
private const val READY_NOTE = "Nothing is left to settle. Accept here, or say so in the conversation."
private const val LEFT_TAIL = "Each can be said in the conversation."
private const val SET_ASIDE_WORDS_LABEL = "Why, in your words"
private const val SET_ASIDE_NOTE = "Nothing is built from it. It keeps its versions and your words, and Reopen proposes it again."

/**
 * The bar at a contract's foot (O.acceptBar in contract.js): what is still left to settle, with Accept, or what was done, with
 * Reopen. Accept is one press and the same as saying so in the conversation. [contract] is the contract and [left] what is
 * still to settle before it can be accepted; [states] are its states, oldest first. [send] is what the bar's last send is
 * doing. [onAccept], [onSetAside] (which takes the owner's words), and [onReopen] are its three controls; [onTalk], when
 * given, opens the conversation about the contract, and [onOpenEpic] opens the epic an accepted contract became. A contract
 * the bar does not know draws nothing but the root. The root node carries [ACCEPT_BAR_TAG].
 */
@Composable
internal fun ContractAcceptBar(
    contract: ContractRow,
    left: List<ContractLeftRow>,
    states: List<ContractStateRow>,
    send: BarSend,
    onAccept: () -> Unit,
    onSetAside: (String) -> Unit,
    onReopen: () -> Unit,
    onTalk: (() -> Unit)?,
    onOpenEpic: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var setAsideOpen by remember(contract.id, contract.state) { mutableStateOf(false) }
    var words by remember(contract.id, contract.state) { mutableStateOf("") }
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight)
            .testTag(ACCEPT_BAR_TAG),
    ) {
        when (contract.state) {
            "proposed" -> ProposedBar(
                contract = contract,
                left = left,
                send = send,
                onAccept = onAccept,
                onTalk = onTalk,
                onSetAsideOpen = { setAsideOpen = true },
            )
            "accepted" -> AcceptedBar(
                contract = contract,
                states = states,
                send = send,
                onOpenEpic = onOpenEpic,
            )
            "set_aside" -> SetAsideBar(
                states = states,
                send = send,
                onReopen = onReopen,
            )
        }
    }
    if (setAsideOpen) {
        SetAsideSheet(
            slug = contractSlug(contract.id),
            words = words,
            onWords = { words = it },
            onKeep = {
                setAsideOpen = false
                words = ""
            },
            onConfirm = {
                setAsideOpen = false
                onSetAside(words)
            },
        )
    }
}

/**
 * A proposed contract's bar: what is left to settle, or the send's line while one is out, then Talk about it when given,
 * Set aside…, and Accept. Accept is enabled once nothing is left, or after a failed send, and never while one is out.
 */
@Composable
private fun ProposedBar(
    contract: ContractRow,
    left: List<ContractLeftRow>,
    send: BarSend,
    onAccept: () -> Unit,
    onTalk: (() -> Unit)?,
    onSetAsideOpen: () -> Unit,
) {
    if (send == BarSend.Idle) {
        if (left.isEmpty()) {
            BarText(READY_NOTE, color = OrreryTokens.Role.ok)
        } else {
            BarText(leftNote(left))
        }
    } else {
        SendLine(send)
    }
    val sending = send == BarSend.Sending
    val acceptable = send is BarSend.Failed || (send == BarSend.Idle && left.isEmpty())
    Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        if (onTalk != null) {
            BarButton(label = "Talk about it", tag = TALK_TAG, onClick = onTalk)
        }
        BarButton(label = "Set aside…", tag = SET_ASIDE_TAG, enabled = !sending, onClick = onSetAsideOpen)
        Button(
            onClick = onAccept,
            enabled = acceptable,
            modifier = Modifier.testTag(ACCEPT_TAG),
        ) {
            Text("Accept v${contract.version}", style = OrreryTokens.Type.small)
        }
    }
}

/** An accepted contract's bar: that it was accepted and frozen at its version, by whom and with what words, then Open its epic. */
@Composable
private fun AcceptedBar(
    contract: ContractRow,
    states: List<ContractStateRow>,
    send: BarSend,
    onOpenEpic: () -> Unit,
) {
    BarText("Accepted, and frozen at v${contract.version}", color = OrreryTokens.Role.ok)
    WhoLine(states.lastOrNull { it.state == "accepted" })
    SendLine(send)
    Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        BarButton(label = "Open its epic", tag = OPEN_EPIC_TAG, onClick = onOpenEpic)
    }
}

/** A set-aside contract's bar: that it was set aside, by whom and with what words, then Reopen. */
@Composable
private fun SetAsideBar(
    states: List<ContractStateRow>,
    send: BarSend,
    onReopen: () -> Unit,
) {
    BarText("Set aside")
    WhoLine(states.lastOrNull { it.state == "set_aside" })
    SendLine(send)
    Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        BarButton(label = "Reopen", tag = REOPEN_TAG, enabled = send != BarSend.Sending, onClick = onReopen)
    }
}

/**
 * The change's who-tag and its words: the maker's name, carrying the maker as [MakerKey], then the words the change was made
 * with. Nothing is drawn when [change] is null.
 */
@Composable
private fun WhoLine(change: ContractStateRow?) {
    if (change == null) return
    val who = whoOf(change.maker)
    Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        Text(
            text = whoName(who),
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.one,
            modifier = Modifier
                .testTag(ACCEPT_BAR_WHO_TAG)
                .semantics { maker = who }
                .border(OrreryTokens.Size.hairline, OrreryTokens.Line.firm, RoundedCornerShape(OrreryTokens.Radius.one))
                .padding(horizontal = OrreryTokens.Space.eight, vertical = OrreryTokens.Space.two),
        )
        if (change.words.isNotEmpty()) {
            Text(change.words, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
        }
    }
}

/** The send's line while it is out or after it failed. [BarSend.Idle] draws nothing. */
@Composable
private fun SendLine(send: BarSend) {
    when (send) {
        BarSend.Idle -> Unit
        BarSend.Sending -> BarText(ACCEPTING)
        is BarSend.Failed -> BarText(send.reason, color = OrreryTokens.Role.bad)
    }
}

/**
 * The set-aside form on a sheet (O.asideSheet in contract.js): what setting it aside does, the owner's words, Keep it proposed
 * and Set aside. Set aside is enabled once words are typed. Dismissing the sheet keeps the form shut, as Keep it proposed does.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SetAsideSheet(
    slug: String,
    words: String,
    onWords: (String) -> Unit,
    onKeep: () -> Unit,
    onConfirm: () -> Unit,
) {
    ModalBottomSheet(onDismissRequest = onKeep) {
        Column(
            modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text("Set $slug aside?", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.one)
            Text(SET_ASIDE_NOTE, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
            Field(
                value = words,
                onValueChange = onWords,
                label = SET_ASIDE_WORDS_LABEL,
                modifier = Modifier.testTag(SET_ASIDE_WORDS_TAG),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
                BarButton(label = "Keep it proposed", tag = SET_ASIDE_KEEP_TAG, onClick = onKeep)
                BarButton(
                    label = "Set aside",
                    tag = SET_ASIDE_CONFIRM_TAG,
                    enabled = words.isNotBlank(),
                    contentColor = OrreryTokens.Role.bad,
                    onClick = onConfirm,
                )
            }
        }
    }
}

/** A line of the bar's words, in the bar's small type. */
@Composable
private fun BarText(text: String, color: Color = OrreryTokens.Ink.one) {
    Text(
        text = text,
        style = OrreryTokens.Type.small,
        color = color,
        modifier = Modifier.padding(bottom = OrreryTokens.Space.eight),
    )
}

/** A quiet control of the bar: raised, with a hairline edge, as the ended bar's buttons are drawn. */
@Composable
private fun BarButton(
    label: String,
    tag: String,
    onClick: () -> Unit,
    enabled: Boolean = true,
    contentColor: Color = OrreryTokens.Ink.one,
) {
    Button(
        onClick = onClick,
        enabled = enabled,
        modifier = Modifier.testTag(tag),
        shape = RoundedCornerShape(OrreryTokens.Radius.two),
        colors = ButtonDefaults.buttonColors(
            containerColor = OrreryTokens.Surface.raised,
            contentColor = contentColor,
        ),
        border = BorderStroke(OrreryTokens.Size.hairline, OrreryTokens.Line.firm),
        elevation = null,
    ) {
        Text(label, style = OrreryTokens.Type.small)
    }
}

/** The bar's note for what is left: how many, each one, and that each can be said in the conversation. */
private fun leftNote(left: List<ContractLeftRow>): String =
    "${left.size} left: ${left.joinToString("; ") { it.text }}. $LEFT_TAIL"

/** The name a who-tag shows for a maker [whoOf] names: you, the Director, or orreryd. */
private fun whoName(who: String): String = when (who) {
    "you" -> "you"
    "director" -> "the Director"
    else -> "orreryd"
}
