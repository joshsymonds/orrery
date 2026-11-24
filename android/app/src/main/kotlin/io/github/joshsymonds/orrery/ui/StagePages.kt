package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.SemanticsPropertyReceiver
import io.github.joshsymonds.orrery.api.RecordMakerRow

/**
 * Semantics key: the maker a stage record's node names, as [whoOf] words it ("you", "director" or "orreryd"). Round 8 draws
 * each maker as a ByMark or a WhoTag (O.by and O.who in design.js), and a test reads the key from the node.
 */
internal val MakerKey = SemanticsPropertyKey<String>("Maker")

/**
 * The maker a stage record's node carries, set as [MakerKey]. It is write-only, as every semantics property is in Compose:
 * a test reads the key from the node's configuration.
 */
internal var SemanticsPropertyReceiver.maker: String
    get() = throw UnsupportedOperationException("maker is write-only; read MakerKey from the node")
    set(value) {
        this[MakerKey] = value
    }

/**
 * Who a record's [maker] is, in the words round 8 gives the three makers (MAKER in design.js): "you" for the owner, the
 * owner through serve and the owner in the conversation; "director" for the conversation that decided for him; and
 * "orreryd" for orreryd, and for a record with no maker.
 */
internal fun whoOf(maker: RecordMakerRow?): String =
    when (maker?.kind) {
        "owner", "owner_serve", "owner_conversation" -> "you"
        "conversation" -> "director"
        else -> "orreryd"
    }

/**
 * Where a question thread is drawn: in the conversation, where the Director asked it, or in the design, or the contract
 * that carries it. An answer given in either place shows in both (the [where] of O.qthread in design.js).
 */
internal enum class ThreadPlace { CONVERSATION, DESIGN }

/**
 * What a question thread's send is doing: [Idle] with nothing being sent, [Sending] while an answer goes to the server, or
 * [Failed] with the [reason] it did not land. The thread's Send control and its sending and error lines draw it (O.qthread's
 * sending and error in design.js).
 */
internal sealed interface ThreadSend {
    /** Nothing is being sent. */
    data object Idle : ThreadSend

    /** An answer is on its way to the server. */
    data object Sending : ThreadSend

    /** The answer did not land, for the [reason] given. */
    data class Failed(val reason: String) : ThreadSend
}
