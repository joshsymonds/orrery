package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.ContractLeftRow
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.ContractStateRow

/** The tag of the accept bar's root node, the bar at a contract's foot (O.acceptBar in contract.js). */
internal const val ACCEPT_BAR_TAG = "accept-bar"

/** The tag of the bar's Accept control, which accepts the contract at its version (the Accept of O.acceptBar in contract.js). */
internal const val ACCEPT_TAG = "accept-bar-accept"

/** The tag of the bar's Set aside control, which opens the form that sets the contract aside (O.acceptBar in contract.js). */
internal const val SET_ASIDE_TAG = "accept-bar-set-aside"

/** The tag of the bar's Reopen control, which proposes a set-aside contract again (O.acceptBar in contract.js). */
internal const val REOPEN_TAG = "accept-bar-reopen"

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

/**
 * The bar at a contract's foot (O.acceptBar in contract.js): what is still left to settle, with Accept, or what was done, with
 * Reopen. Accept is one press and the same as saying so in the conversation. [contract] is the contract and [left] what is
 * still to settle before it can be accepted; [states] are its states, oldest first. [send] is what the bar's last send is
 * doing. [onAccept], [onSetAside] (which takes the owner's words), and [onReopen] are its three controls; [onTalk], when
 * given, opens the conversation about the contract, and [onOpenEpic] opens the epic an accepted contract became. The root
 * node carries [ACCEPT_BAR_TAG].
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
    Box(modifier.testTag(ACCEPT_BAR_TAG)) {}
}
