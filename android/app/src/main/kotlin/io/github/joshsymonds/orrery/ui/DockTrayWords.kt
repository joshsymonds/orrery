package io.github.joshsymonds.orrery.ui

data class TrayRowFacts(val waits: String, val cancelled: Boolean)

fun trayLabel(count: Int): String = "Queued $count"

fun trayWhy(pane: Boolean, rows: List<TrayRowFacts>): String {
    val live = rows.filter { !it.cancelled }
    if (pane) {
        return if (live.isNotEmpty()) "Claude Code takes them when it chooses"
        else "dropped when the turn was cancelled"
    }
    if (live.isEmpty()) return "dropped when the session ended"
    val starts = live.count { it.waits == "start" }
    return when {
        starts == 1 -> "Claude takes it as its next turn"
        starts > 1 -> "Claude takes them as its next turns"
        else -> "Claude takes them at its next tool result"
    }
}

fun waitsLine(pending: List<String>, putOff: Boolean): String {
    val speak = pending.count { it == "speak" }
    val request = if (putOff) pending.count { it == "request" } else 0
    return when {
        speak == 1 -> "1 event waits until you speak"
        speak > 1 -> "$speak events wait until you speak"
        request == 1 -> "1 event waits on the question you put off"
        request > 1 -> "$request events wait on the question you put off"
        else -> ""
    }
}

object DockLines {
    val sendRefused = "Not sent. Your draft is kept; send again."
    val sendUnknown = "No answer from the server. It may have been sent: check the conversation before sending again."
    val alreadyTook = "Claude already took it in."
    val takeBackFailed = "Could not take it back."
    val takeAllBackFailed = "Could not take them back."
    fun backInBox(n: Int): String =
        if (n == 1) "Your queued message is back in the box." else "Your $n queued messages are back in the box."
    fun sendNowHeld(n: Int): String =
        if (n == 1) "1 message was not sent again. It is back in the box."
        else "$n messages were not sent again. They are back in the box."
    fun resumeRefused(message: String): String = "Not resumed: $message"
    val resumeUnknown = "No answer from the server. It may have resumed: check before trying again."
    val notDelivered = "not delivered"
}
