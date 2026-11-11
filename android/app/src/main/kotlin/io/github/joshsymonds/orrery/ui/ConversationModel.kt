package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ConversationItem

data class ToolCall(
    val id: String,
    val name: String,
    val input: String,
    val result: String?,
    val error: Boolean,
    val time: Long = 0L,
)

sealed interface ConvoEntry {
    data class Time(val seconds: Long) : ConvoEntry
    data class Prompt(val text: String, val time: Long = 0L) : ConvoEntry
    data class Reply(val text: String, val time: Long = 0L) : ConvoEntry
    data class Tool(val call: ToolCall) : ConvoEntry
    data class Group(val calls: List<ToolCall>) : ConvoEntry

    /** A compaction: its [trigger] ("auto", "manual", or ""), the context [tokens] before it, and its summary [text]. */
    data class Compaction(val trigger: String, val tokens: Long, val text: String, val time: Long = 0L) : ConvoEntry

    /** A session event: [sort] is the item's `notice` value, [error] marks a failed or killed task. */
    data class Notice(val sort: String, val text: String, val error: Boolean, val time: Long = 0L) : ConvoEntry
}

private const val MIN_GROUP_SIZE = 3

private class Outcome(val text: String, val error: Boolean)

/**
 * Folds the server's conversation items, oldest first, into the entries the conversation view
 * draws. A `result` fills in the earlier call with the same id and is otherwise dropped. Runs of
 * three or more calls, with only results and notices between them, become one group, and the
 * notices that fell inside the run follow the group. A shorter run keeps its calls and notices in
 * their own order.
 */
fun foldConversation(items: List<ConversationItem>): List<ConvoEntry> {
    val outcomes = outcomesByCallIndex(items)
    val entries = mutableListOf<ConvoEntry>()
    // The open run: its calls as Tool entries, with the notices that arrived inside it, in order.
    val run = mutableListOf<ConvoEntry>()

    items.forEachIndexed { index, item ->
        when (item.kind) {
            "tool", "question" -> run.add(ConvoEntry.Tool(toolCall(item, outcomes[index])))
            "result" -> Unit
            "notice" -> {
                val notice = ConvoEntry.Notice(item.notice, item.text, item.error, item.time)
                if (run.isEmpty()) entries.add(notice) else run.add(notice)
            }
            else -> {
                flushRun(run, entries)
                when (item.kind) {
                    "prompt" -> {
                        if (item.time > 0) entries.add(ConvoEntry.Time(item.time))
                        entries.add(ConvoEntry.Prompt(item.text, item.time))
                    }
                    "reply" -> {
                        if (item.time > 0) entries.add(ConvoEntry.Time(item.time))
                        entries.add(ConvoEntry.Reply(item.text, item.time))
                    }
                    "compaction" -> entries.add(ConvoEntry.Compaction(item.trigger, item.tokens, item.text, item.time))
                }
            }
        }
    }
    flushRun(run, entries)
    return entries
}

/**
 * One key per entry, in order. A key comes from the entry's own content (its time, a hash of its
 * text, its call ids), never its list position, so it holds while the server's window slides. A
 * key that repeats gets an occurrence suffix counted from the oldest entry.
 */
fun entryKeys(entries: List<ConvoEntry>): List<String> {
    val seen = HashMap<String, Int>()
    return entries.map { entry ->
        val base = baseKey(entry)
        val occurrence = seen.getOrDefault(base, 0)
        seen[base] = occurrence + 1
        if (occurrence == 0) base else "$base#$occurrence"
    }
}

private fun baseKey(entry: ConvoEntry): String = when (entry) {
    is ConvoEntry.Time -> "time:${entry.seconds}"
    is ConvoEntry.Prompt -> "prompt:${entry.time}:${entry.text.hashCode()}"
    is ConvoEntry.Reply -> "reply:${entry.time}:${entry.text.hashCode()}"
    is ConvoEntry.Tool -> "tool:${entry.call.id}"
    is ConvoEntry.Group -> "group:${entry.calls.first().id}"
    is ConvoEntry.Compaction -> "compaction:${entry.time}:${entry.trigger}:${entry.tokens}:${entry.text.hashCode()}"
    is ConvoEntry.Notice -> "notice:${entry.time}:${entry.sort}:${entry.text.hashCode()}"
}

/**
 * The index of the first entry newer than [seen] (unix seconds), or -1 when none is or [seen] is
 * null. An entry is newer when its time is above zero and above [seen].
 */
fun firstNewerIndex(entries: List<ConvoEntry>, seen: Long?): Int {
    if (seen == null) return -1
    return entries.indexOfFirst { entry ->
        val time = entryTime(entry)
        time > 0 && time > seen
    }
}

/** The time an entry is read by: a tool by its call, a group by its newest call. */
internal fun entryTime(entry: ConvoEntry): Long = when (entry) {
    is ConvoEntry.Time -> entry.seconds
    is ConvoEntry.Prompt -> entry.time
    is ConvoEntry.Reply -> entry.time
    is ConvoEntry.Tool -> entry.call.time
    is ConvoEntry.Group -> entry.calls.maxOfOrNull { it.time } ?: 0L
    is ConvoEntry.Compaction -> entry.time
    is ConvoEntry.Notice -> entry.time
}

/** Names the calls' tools in order of first appearance, "Name ×N" when a name repeats. */
fun groupSummary(calls: List<ToolCall>): String {
    val counts = LinkedHashMap<String, Int>()
    calls.forEach { counts.merge(it.name, 1, Int::plus) }
    return counts.entries.joinToString(" · ") { (name, count) ->
        if (count > 1) "$name ×$count" else name
    }
}

private fun outcomesByCallIndex(items: List<ConversationItem>): Map<Int, Outcome> {
    val callIndexById = HashMap<String, Int>()
    val outcomes = HashMap<Int, Outcome>()
    items.forEachIndexed { index, item ->
        when (item.kind) {
            "tool", "question" -> callIndexById[item.id] = index
            "result" -> callIndexById[item.id]?.let { outcomes[it] = Outcome(item.text, item.error) }
        }
    }
    return outcomes
}

private fun toolCall(item: ConversationItem, outcome: Outcome?): ToolCall {
    val input = if (item.kind == "question") item.text else item.input
    return ToolCall(
        id = item.id,
        name = item.tool,
        input = input,
        result = outcome?.text,
        error = outcome?.error ?: false,
        time = item.time,
    )
}

private fun flushRun(run: MutableList<ConvoEntry>, entries: MutableList<ConvoEntry>) {
    val calls = run.filterIsInstance<ConvoEntry.Tool>().map { it.call }
    if (calls.size >= MIN_GROUP_SIZE) {
        entries.add(ConvoEntry.Group(calls))
        entries.addAll(run.filterIsInstance<ConvoEntry.Notice>())
    } else {
        entries.addAll(run)
    }
    run.clear()
}
