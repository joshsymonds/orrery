package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.BackgroundTask
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.ConversationItem
import io.github.joshsymonds.orrery.api.QueuedItem
import io.github.joshsymonds.orrery.api.StepFacts
import io.github.joshsymonds.orrery.api.UnfinishedText
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId

data class ToolCall(
    val id: String,
    val name: String,
    val input: String,
    val result: String?,
    val error: Boolean,
    val time: Long = 0L,
    val contract: String = "",
    val added: Long = 0L,
    val removed: Long = 0L,
)

/**
 * What the transcript draws, in order. Each entry is one shape: a block someone speaks in, a line of machinery, or a rule
 * between. [promptId] is the client-chosen id of the prompt an entry came from, and [time] is unix seconds (0 for none).
 */
sealed interface ConvoEntry {
    /** The owner's message, drawn as YOU; with the phone mark when [phone] is set, as a prompt sent from the phone app. */
    data class Prompt(val text: String, val time: Long = 0L, val promptId: String = "", val phone: Boolean = false) : ConvoEntry

    /** A session event the engine sent, drawn as EVENT from the engine. */
    data class Event(val text: String, val time: Long = 0L, val promptId: String = "") : ConvoEntry

    /** A cron prompt, drawn as CRON. */
    data class Cron(val text: String, val time: Long = 0L, val promptId: String = "") : ConvoEntry

    /**
     * A step's end: its [who], the task the step ran or else its step id, its [agent], how it [end]ed, its [summary] and its
     * [facts] when it carries them.
     */
    data class StepEnd(
        val who: String,
        val agent: String,
        val end: String,
        val summary: String,
        val facts: StepFacts?,
        val time: Long = 0L,
        val promptId: String = "",
    ) : ConvoEntry

    /**
     * An epic's [kind] of event (question, park, release or end) about [epic], answering [tail] (the question's id, or the
     * phase the epic reached), with its first [sentence].
     */
    data class EpicEvent(
        val kind: String,
        val epic: String,
        val tail: String,
        val sentence: String,
        val time: Long = 0L,
        val promptId: String = "",
    ) : ConvoEntry

    /** The [epic] a create call made, from an accepted contract. */
    data class Created(val epic: String, val time: Long = 0L) : ConvoEntry

    /** A move of an epic, drawn as its move line. */
    data class Move(val event: MoveLineEvent, val time: Long = 0L, val promptId: String = "") : ConvoEntry

    /** A step's brief, folded to a line that opens to its [text]. */
    data class Brief(val text: String, val time: Long = 0L, val promptId: String = "") : ConvoEntry

    /** A round-zero engine prompt, folded to a line named by its first line. */
    data class Engine(val text: String, val time: Long = 0L, val promptId: String = "") : ConvoEntry

    /** A send-back from the engine in [round] 1 or later, drawn as CHECKS. */
    data class Check(val round: Int, val text: String, val time: Long = 0L, val promptId: String = "") : ConvoEntry

    /** The session's answer as Markdown. */
    data class Reply(val text: String, val time: Long = 0L) : ConvoEntry

    /** A tool call on its own line. */
    data class Tool(val call: ToolCall) : ConvoEntry

    /** Three or more tool calls in a row, drawn as one run line. */
    data class Group(val calls: List<ToolCall>) : ConvoEntry

    /** A call that writes a contract and has a result that did not fail: its [call] draws as the contract's one line. */
    data class ContractLine(val call: ToolCall) : ConvoEntry

    /** A compaction: its [trigger] ("auto", "manual", or ""), the context [tokens] before it, and its summary [text]. */
    data class Compaction(val trigger: String, val tokens: Long, val text: String, val time: Long = 0L) : ConvoEntry

    /** A session notice: its [sort] names its label, and [error] marks a failed task. */
    data class Notice(val sort: String, val text: String, val error: Boolean, val time: Long = 0L) : ConvoEntry

    /** A finished subagent or a background task as one line: its [type], subagent type, description and [status]. */
    data class Task(
        val type: String,
        val subagentType: String,
        val description: String,
        val status: String,
        val time: Long = 0L,
    ) : ConvoEntry

    /** An unfinished thinking block of a headless session, drawn as a line of thinking that opens to its [text]. */
    data class Thinking(val id: String, val text: String) : ConvoEntry

    /** An unfinished text block of a headless session, drawn as a reply with its caret. */
    data class LiveReply(val id: String, val text: String) : ConvoEntry

    /** A message the page sent that the stored conversation does not hold yet, drawn as a YouBlock while sending. */
    data class Sending(val text: String) : ConvoEntry

    /** The owner's interruption, drawn as a rule with its time. */
    data class Interrupted(val time: Long) : ConvoEntry

    /** The local day [seconds] falls on, drawn as a rule where the day changes. */
    data class Day(val seconds: Long) : ConvoEntry
}

private const val MIN_GROUP_SIZE = 3
private const val EPIC_PREFIX = "epic:"
private const val STEP_PREFIX = "step:"
private const val STEP_KIND = "step_end"
private const val MORE_PREFIX = "More: "
private val EPIC_KINDS = setOf("question", "park", "release", "end")
private const val THINKING_BLOCK = "thinking"
private const val TEXT_BLOCK = "text"
private const val PHONE_CLIENT = "orrery-android"

// The item kinds that draw an entry, and so can open a local day.
private val DAY_KINDS = setOf("prompt", "reply", "tool", "question", "notice", "compaction", "interrupted", "agent_finished")

// blockPattern matches an event block as enginehost.CallerEventBlock writes it: the opening tag with its kind and id
// first and any further attributes after them, the body on lines of its own, and the closing tag.
private val blockPattern = Regex(
    """<orrery-event kind="([^"]*)" id="([^"]*)"((?: [a-z_]+="[^"]*")*)>\n([\s\S]*)\n</orrery-event>""",
)
private val attributePattern = Regex(""" ([a-z_]+)="([^"]*)"""")
private val entityPattern = Regex("""&(amp|lt|gt|quot);""")
private val entities = mapOf("amp" to "&", "lt" to "<", "gt" to ">", "quot" to "\"")

// firstSentencePattern matches a body's first sentence, up to a full stop that ends a word, or else its first line.
private val firstSentencePattern = Regex("""^[^\n]*?\.(?=\s|$)|^[^\n]*""")

/** An event block as its text holds it: its kind and id, its attributes, and its body, with every value unescaped. */
private class ParsedBlock(val kind: String, val id: String, val attributes: Map<String, String>, val body: String)

/** Reads [text] as an event block, or returns null for any other text. */
private fun blockOf(text: String): ParsedBlock? {
    val match = blockPattern.matchEntire(text.trim()) ?: return null
    val (kind, id, further, body) = match.destructured
    val attributes = attributePattern.findAll(further)
        .associate { it.groupValues[1] to unescape(it.groupValues[2]) }
    return ParsedBlock(unescape(kind), unescape(id), attributes, unescape(body))
}

private fun unescape(text: String): String = entityPattern.replace(text) { entities.getValue(it.groupValues[1]) }

/** A step's end as its block gives it: who it is named by, the agent that ran it, how it ended, and its brief. */
private class ParsedStep(val who: String, val agent: String, val end: String, val summary: String)

// stepEndOf reads a block as a step's end: a step_end block whose id is step:<id>, with end and status attributes. It is
// named by its task when the block names one, else by its step id.
private fun stepEndOf(block: ParsedBlock): ParsedStep? {
    if (block.kind != STEP_KIND || !block.id.startsWith(STEP_PREFIX)) return null
    val end = block.attributes["end"] ?: return null
    if (!block.attributes.containsKey("status")) return null
    val who = block.attributes["task"].orEmpty().ifEmpty { block.id.removePrefix(STEP_PREFIX) }
    return ParsedStep(who, block.attributes["agent"].orEmpty(), end, briefOf(block.body))
}

// briefOf is a step's brief from its body: the body's lines, without a last line that starts with More:, which points to
// the full return.
private fun briefOf(body: String): String {
    if (body.isEmpty()) return ""
    val lines = body.split("\n")
    return (if (lines.last().startsWith(MORE_PREFIX)) lines.dropLast(1) else lines).joinToString("\n")
}

// firstSentence is a body's first sentence, trimmed.
private fun firstSentence(body: String): String =
    firstSentencePattern.find(body.trimStart())?.value?.trim().orEmpty()

/**
 * Folds the server's conversation items, oldest first, into the entries the transcript draws, by the kind-to-shape table:
 * a prompt by its sender, an event by its block, a call as a line, a run of three or more calls as one run line, and
 * the rules a change of local day, an interruption and a compaction draw. A `result` fills in the earlier call with the
 * same id and is otherwise dropped. Runs of three or more calls, with only results and notices between them, become one
 * group, and the notices that fell inside the run follow the group. A shorter run keeps its calls and notices in their
 * own order. A call that writes a contract and has a result that did not fail is its own contract line, and so is an
 * epic_create call with a result that did not fail, its created line; each ends the run before it, so it never joins a
 * group. A day rule is drawn before an item whose local day, in [zone], differs from the item before it, and before the
 * first item when its day is not the day of [now] (unix seconds, the caller's clock). Any kind not named draws nothing,
 * and so does a prompt from a sender not named.
 */
fun foldConversation(
    items: List<ConversationItem>,
    now: Long,
    zone: ZoneId = ZoneId.systemDefault(),
): List<ConvoEntry> {
    val outcomes = outcomesByCallIndex(items)
    val entries = mutableListOf<ConvoEntry>()
    // The open run: its calls as Tool entries, with the notices that arrived inside it, in order.
    val run = mutableListOf<ConvoEntry>()

    items.forEachIndexed { index, item ->
        if (item.kind in DAY_KINDS && dayOpens(items, index, now, zone)) {
            flushRun(run, entries)
            entries.add(ConvoEntry.Day(item.time))
        }
        when (item.kind) {
            "tool", "question" -> {
                val outcome = outcomes[index]
                val call = toolCall(item, outcome)
                val answered = outcome != null && !outcome.error
                when {
                    item.epic != "" && answered -> {
                        flushRun(run, entries)
                        entries.add(ConvoEntry.Created(item.epic, item.time))
                    }
                    call.contract != "" && answered -> {
                        flushRun(run, entries)
                        entries.add(ConvoEntry.ContractLine(call))
                    }
                    else -> run.add(ConvoEntry.Tool(call))
                }
            }
            "result" -> Unit
            "notice" -> {
                val notice = ConvoEntry.Notice(item.notice, item.text, item.error, item.time)
                if (run.isEmpty()) entries.add(notice) else run.add(notice)
            }
            else -> {
                flushRun(run, entries)
                entryOf(item)?.let(entries::add)
            }
        }
    }
    flushRun(run, entries)
    return entries
}

// entryOf is the entry an item of a kind other than a call or a notice draws, or null for a kind that draws nothing.
private fun entryOf(item: ConversationItem): ConvoEntry? = when (item.kind) {
    "prompt" -> promptEntry(item)
    "reply" -> ConvoEntry.Reply(item.text, item.time)
    "compaction" -> ConvoEntry.Compaction(item.trigger, item.tokens, item.text, item.time)
    "interrupted" -> ConvoEntry.Interrupted(item.time)
    "agent_finished" -> ConvoEntry.Task("agent", item.tool, item.text, item.notice.ifEmpty { "done" }, item.time)
    else -> null
}

// promptEntry is a prompt's entry by its sender: the owner's or one with no sender, the engine's, the brief, a check or
// round-zero fold, a cron, or an event, which is read by its block. A prompt of any other sender is no entry.
private fun promptEntry(item: ConversationItem): ConvoEntry? = when (item.sender) {
    "owner", "" -> ConvoEntry.Prompt(item.text, item.time, item.promptId, phone = item.client == PHONE_CLIENT)
    "brief" -> ConvoEntry.Brief(item.text, item.time, item.promptId)
    "engine" ->
        if (item.round >= 1) {
            ConvoEntry.Check(item.round, item.text, item.time, item.promptId)
        } else {
            ConvoEntry.Engine(item.text, item.time, item.promptId)
        }
    "cron" -> ConvoEntry.Cron(item.text, item.time, item.promptId)
    "event" -> eventEntry(item)
    else -> null
}

// eventEntry is an event prompt's entry: a step's end, a move, an epic's question, park, release or end, or else the
// engine's event as it is written.
private fun eventEntry(item: ConversationItem): ConvoEntry {
    val block = blockOf(item.text)
    if (block != null) {
        stepEndOf(block)?.let {
            return ConvoEntry.StepEnd(it.who, it.agent, it.end, it.summary, item.stepFacts, item.time, item.promptId)
        }
    }
    moveLineOf(item.text)?.let { return ConvoEntry.Move(it, item.time, item.promptId) }
    if (block != null && block.kind in EPIC_KINDS && block.id.startsWith(EPIC_PREFIX)) {
        splitEpicId(block.id)?.let { (epic, tail) ->
            return ConvoEntry.EpicEvent(block.kind, epic, tail, firstSentence(block.body), item.time, item.promptId)
        }
    }
    return ConvoEntry.Event(item.text, item.time, item.promptId)
}

// splitEpicId reads an event id, epic:<epic>:<tail>, as its epic and its tail, which follows the last colon. Null when the
// id has no epic or no tail.
private fun splitEpicId(id: String): Pair<String, String>? {
    val rest = id.removePrefix(EPIC_PREFIX)
    val cut = rest.lastIndexOf(':')
    return if (cut <= 0 || cut == rest.length - 1) null else rest.substring(0, cut) to rest.substring(cut + 1)
}

/**
 * The entries of a headless session's unfinished blocks, in order: a thinking block as thinking, a text block as a live
 * reply. A subagent's block, a block of another kind and an empty block draw nothing.
 */
fun unfinishedEntries(unfinished: List<UnfinishedText>): List<ConvoEntry> =
    unfinished.filter { it.parentToolUseId.isEmpty() && it.text.isNotEmpty() }.mapNotNull { block ->
        val id = "${block.messageId}/${block.index}"
        when (block.kind) {
            THINKING_BLOCK -> ConvoEntry.Thinking(id, block.text)
            TEXT_BLOCK -> ConvoEntry.LiveReply(id, block.text)
            else -> null
        }
    }

/**
 * The background tasks of a headless session as task lines, in the state's order. A task has no time of its own, so it
 * never counts as new or read.
 */
fun backgroundEntries(tasks: List<BackgroundTask>): List<ConvoEntry> =
    tasks.map { ConvoEntry.Task(it.type, it.subagentType, it.description, it.status) }

/**
 * A message a screen sent: its [text], the [promptId] a session's send was named by ("" when the send named none, as a
 * pane's or a step's does), and the newest item the conversation held when the send began, null when none.
 */
data class SentText(val text: String, val after: ConversationItem?, val promptId: String = "")

/**
 * The sent messages the conversation does not hold yet, in order. A message with a prompt id is held once a prompt item
 * or a queued message carries that id. A message without one is held once a prompt with its text, compared trimmed,
 * follows the item its send began after (or any prompt, when that item is no longer held), or once a queued message has
 * its text. A pane has no queue, so its [queued] is empty.
 */
fun unsentTexts(sent: List<SentText>, items: List<ConversationItem>, queued: List<QueuedItem>): List<SentText> =
    sent.filterNot { entry ->
        if (entry.promptId.isNotEmpty()) {
            items.any { it.kind == "prompt" && it.promptId == entry.promptId } || queued.any { it.promptId == entry.promptId }
        } else {
            heldAfter(entry, items) || queued.any { sameText(it.text, entry.text) }
        }
    }

private fun heldAfter(entry: SentText, items: List<ConversationItem>): Boolean {
    val from = entry.after?.let { items.indexOf(it) } ?: -1
    return items.withIndex().any { (index, item) ->
        index > from && item.kind == "prompt" && sameText(item.text, entry.text)
    }
}

private fun sameText(a: String, b: String): Boolean = a.trim() == b.trim()

// dayOpens is whether the item at index opens a local day: the first item when its day is not the day of now, and any
// other item whose day differs from the item before it. An item with no time, or whose predecessor has none, opens no day.
private fun dayOpens(items: List<ConversationItem>, index: Int, now: Long, zone: ZoneId): Boolean {
    val day = dayOfTime(items[index].time, zone) ?: return false
    if (index == 0) return day != dayAt(now, zone)
    val before = dayOfTime(items[index - 1].time, zone) ?: return false
    return before != day
}

private fun dayAt(seconds: Long, zone: ZoneId): LocalDate = Instant.ofEpochSecond(seconds).atZone(zone).toLocalDate()

// dayOfTime is the local day a time falls on, or null for a time of 0, which is no time.
private fun dayOfTime(seconds: Long, zone: ZoneId): LocalDate? = if (seconds <= 0) null else dayAt(seconds, zone)

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
    is ConvoEntry.Prompt -> "prompt:${entry.time}:${entry.text.hashCode()}"
    is ConvoEntry.Event -> "event:${entry.time}:${entry.text.hashCode()}"
    is ConvoEntry.Cron -> "cron:${entry.time}:${entry.text.hashCode()}"
    is ConvoEntry.StepEnd -> "stepend:${entry.time}:${entry.end}:${entry.summary.hashCode()}"
    is ConvoEntry.EpicEvent -> "epicevent:${entry.time}:${entry.kind}:${entry.epic}"
    is ConvoEntry.Created -> "created:${entry.time}:${entry.epic}"
    is ConvoEntry.Move -> "move:${entry.time}:${entry.event.epic}"
    is ConvoEntry.Brief -> "brief:${entry.time}:${entry.text.hashCode()}"
    is ConvoEntry.Engine -> "engine:${entry.time}:${entry.text.hashCode()}"
    is ConvoEntry.Check -> "check:${entry.time}:${entry.round}:${entry.text.hashCode()}"
    is ConvoEntry.Reply -> "reply:${entry.time}:${entry.text.hashCode()}"
    is ConvoEntry.Tool -> "tool:${entry.call.id}"
    is ConvoEntry.Group -> "group:${entry.calls.first().id}"
    is ConvoEntry.ContractLine -> "contract:${entry.call.id}"
    is ConvoEntry.Compaction -> "compaction:${entry.time}:${entry.trigger}:${entry.tokens}:${entry.text.hashCode()}"
    is ConvoEntry.Notice -> "notice:${entry.time}:${entry.sort}:${entry.text.hashCode()}"
    is ConvoEntry.Task -> "task:${entry.time}:${entry.type}:${entry.subagentType}:${entry.description.hashCode()}:${entry.status}"
    is ConvoEntry.Interrupted -> "interrupted:${entry.time}"
    is ConvoEntry.Day -> "day:${entry.seconds}"
    is ConvoEntry.Thinking -> "thinking:${entry.id}"
    is ConvoEntry.LiveReply -> "live:${entry.id}"
    is ConvoEntry.Sending -> "sending:${entry.text.hashCode()}"
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

/** The time an entry is read by: a call by its own time, a run by its newest call. A day rule has no time of its own. */
internal fun entryTime(entry: ConvoEntry): Long = when (entry) {
    is ConvoEntry.Prompt -> entry.time
    is ConvoEntry.Event -> entry.time
    is ConvoEntry.Cron -> entry.time
    is ConvoEntry.StepEnd -> entry.time
    is ConvoEntry.EpicEvent -> entry.time
    is ConvoEntry.Created -> entry.time
    is ConvoEntry.Move -> entry.time
    is ConvoEntry.Brief -> entry.time
    is ConvoEntry.Engine -> entry.time
    is ConvoEntry.Check -> entry.time
    is ConvoEntry.Reply -> entry.time
    is ConvoEntry.Tool -> entry.call.time
    is ConvoEntry.Group -> entry.calls.maxOfOrNull { it.time } ?: 0L
    is ConvoEntry.ContractLine -> entry.call.time
    is ConvoEntry.Compaction -> entry.time
    is ConvoEntry.Notice -> entry.time
    is ConvoEntry.Task -> entry.time
    is ConvoEntry.Interrupted -> entry.time
    is ConvoEntry.Day -> 0L
    is ConvoEntry.Thinking -> 0L
    is ConvoEntry.LiveReply -> 0L
    is ConvoEntry.Sending -> 0L
}

/** The tone a contract's transcript line is drawn in. */
enum class ContractLineTone { Attention, Ok, Quiet }

/** A contract's transcript line: its [tone], its [title], and the [rest] of its words after the title. */
data class ContractLineWords(val tone: ContractLineTone, val title: String, val rest: String)

/**
 * The words of the transcript line for contract [id], as the web's ContractBlock says them: waiting, accepted with its
 * clock time in [zone], or set aside. A [contract] the list does not hold, null here, draws its id alone.
 */
fun contractLineWords(id: String, contract: ContractRow?, zone: ZoneId = ZoneId.systemDefault()): ContractLineWords = when {
    contract == null -> ContractLineWords(ContractLineTone.Quiet, id, "")
    contract.state == "proposed" ->
        ContractLineWords(ContractLineTone.Attention, contract.title, " · ${contract.care} care · waits for you")
    contract.state == "accepted" ->
        ContractLineWords(
            ContractLineTone.Ok,
            contract.title,
            " · accepted ${clockOf(contract.changed, zone)} · ${contract.care} care",
        )
    else -> ContractLineWords(ContractLineTone.Quiet, contract.title, " · set aside")
}

// clockOf is a change's time as the clock reads it in the zone given; no time reads as nothing.
private fun clockOf(time: Long, zone: ZoneId): String =
    if (time <= 0) "" else Instant.ofEpochSecond(time).atZone(zone).format(CLOCK_TIME)

private fun outcomesByCallIndex(items: List<ConversationItem>): Map<Int, Outcome> {
    val callIndexById = HashMap<String, Int>()
    val outcomes = HashMap<Int, Outcome>()
    items.forEachIndexed { index, item ->
        when (item.kind) {
            "tool", "question" -> callIndexById[item.id] = index
            "result" -> callIndexById[item.id]?.let { outcomes[it] = Outcome(item.text, item.error, item.added, item.removed) }
        }
    }
    return outcomes
}

private class Outcome(val text: String, val error: Boolean, val added: Long, val removed: Long)

private fun toolCall(item: ConversationItem, outcome: Outcome?): ToolCall {
    val input = if (item.kind == "question") item.text else item.input
    return ToolCall(
        id = item.id,
        name = item.tool,
        input = input,
        result = outcome?.text,
        error = outcome?.error ?: false,
        time = item.time,
        contract = item.contract,
        added = outcome?.added ?: 0L,
        removed = outcome?.removed ?: 0L,
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
