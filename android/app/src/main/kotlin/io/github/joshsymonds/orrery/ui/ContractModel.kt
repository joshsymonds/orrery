package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ContractDecisionRow
import io.github.joshsymonds.orrery.api.ContractRecordRow
import io.github.joshsymonds.orrery.api.ContractVersionRow
import java.time.Instant
import java.time.ZoneId

// The rules and words a contract shares across Android's contract screen, transcript block and subject band: what is
// left to settle, when Accept is offered, the record line, the state pill and the About message. The web's
// contracts.ts says the same words, so both clients show one contract the same way.

// contractSlug is a contract's id past its last slash: "tidepool/tide-alerts" is "tide-alerts".
fun contractSlug(id: String): String = id.substringAfterLast('/')

// newestVersion is the last of a record's versions, which come oldest first.
fun newestVersion(record: ContractRecordRow): ContractVersionRow? = record.versions.lastOrNull()

// openDecisions counts the decisions that have no answer yet.
fun openDecisions(decisions: List<ContractDecisionRow>): Int = decisions.count { it.answer == "" }

// concernedRows is the row of every open decision that names one.
fun concernedRows(decisions: List<ContractDecisionRow>): Set<String> =
    decisions.filter { it.answer == "" && it.row != "" }.map { it.row }.toSet()

// readKey is the key a viewer's read of a contract's irreversible section is stored under.
fun readKey(id: String, version: Long): String = "$id@$version"

// Left is what a contract still asks of its owner: its open decisions, and whether its irreversible section is unread.
data class Left(val decisions: Int, val unread: Boolean)

// leftOf is what a contract still asks: its open decisions, and an irreversible section unread at severe care.
fun leftOf(decisions: Int, care: String, shown: Boolean): Left = Left(decisions, unread = care == "severe" && !shown)

// leftItems words each thing left to settle, decisions first.
fun leftItems(left: Left): List<String> = buildList {
    if (left.decisions > 0) {
        add(if (left.decisions == 1) "1 decision is yours" else "${left.decisions} decisions are yours")
    }
    if (left.unread) {
        add("read “can’t be undone” before Accept")
    }
}

// bandText is the band line's words for what is left to settle.
fun bandText(left: Left): String {
    val items = leftItems(left)
    return if (items.isEmpty()) "nothing left to settle" else items.joinToString("; ")
}

// footText is the foot line's sentence for what is left to settle.
fun footText(left: Left): String {
    val items = leftItems(left)
    if (items.isEmpty()) {
        return "Nothing is left to settle."
    }
    val things = if (items.size == 1) "1 thing left" else "${items.size} things left"
    return "$things: ${items.joinToString("; ")}."
}

// acceptable is whether Accept is offered for the version drawn: the contract is proposed, the drawn version is the
// newest and has no open decision, a severe contract's irreversible section was read, and nothing is being sent.
fun acceptable(record: ContractRecordRow, drawn: Long, read: Set<String>, sending: Boolean): Boolean {
    val contract = record.contract
    val newest = newestVersion(record) ?: return false
    if (sending || contract.state != "proposed" || newest.version != drawn) {
        return false
    }
    if (openDecisions(newest.decisions) > 0) {
        return false
    }
    return contract.care != "severe" || readKey(contract.id, drawn) in read
}

// AcceptStep is what the Accept press does after its re-read: send the Accept, or redraw the newer answer instead.
sealed interface AcceptStep {
    data object Send : AcceptStep
    data class Redraw(val record: ContractRecordRow) : AcceptStep
}

// acceptStep sends the Accept only while the contract is still proposed at the version drawn.
fun acceptStep(drawn: Long, reread: ContractRecordRow): AcceptStep {
    val contract = reread.contract
    return if (contract.state == "proposed" && contract.version == drawn) AcceptStep.Send else AcceptStep.Redraw(reread)
}

// recordLine is the decision line's record words: when the contract was proposed, or the last accept or set-aside.
fun recordLine(record: ContractRecordRow, zone: ZoneId): String {
    val contract = record.contract
    val newest = newestVersion(record) ?: return ""
    if (contract.state == "proposed") {
        if (record.versions.size == 1) {
            return "v1 proposed ${clockText(newest.time, zone)}"
        }
        val first = record.versions.first()
        return "v${newest.version} proposed ${clockText(newest.time, zone)} · first written ${clockText(first.time, zone)}"
    }
    val change = record.states.lastOrNull { it.state == contract.state }
    if (change == null) {
        val words = if (contract.state == "set_aside") "set aside" else contract.state
        return "v${newest.version} $words"
    }
    val who = if (change.by == "owner") "you" else "the session"
    if (contract.state == "set_aside") {
        return "v${change.version} set aside by $who ${clockText(change.time, zone)} with “${change.words}”"
    }
    val withWords = if (change.words == "") "" else " with “${change.words}”"
    return "v${change.version} accepted by $who ${clockText(change.time, zone)}$withWords"
}

// statePill is the words of a contract's state.
fun statePill(state: String): String = when (state) {
    "proposed" -> "proposed · waits for you"
    "accepted" -> "accepted"
    "set_aside" -> "set aside"
    else -> state
}

// aboutMessage is the message sent with a row's About: the row's id and text, then the draft.
fun aboutMessage(id: String, text: String, draft: String): String = "About $id: $text\n\n$draft"

// clockText is a change's time as the clock reads it in the zone given; no time reads as nothing, as the web's clock does.
private fun clockText(time: Long, zone: ZoneId): String =
    if (time <= 0) "" else Instant.ofEpochSecond(time).atZone(zone).format(CLOCK_TIME)
