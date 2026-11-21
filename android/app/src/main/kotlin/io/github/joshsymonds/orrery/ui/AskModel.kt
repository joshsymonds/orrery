package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.SessionRequest
import org.json.JSONException
import org.json.JSONObject

/** The kind of an Ask: a pane's question, a session's question or permission, an epic's question, or an epic's park. */
internal enum class AskKind { SESSION, PERMISSION, PANE, EPIC, PARK }

/** One option of an Ask, numbered from 1. [answerable] is false past the ninth, which no key picks. */
internal data class AskOption(
    val n: Int,
    val name: String,
    val does: String,
    val recommended: Boolean,
    val why: String,
    val answerable: Boolean,
)

/** The call an answer, not-now, bring-back or decline makes: a pane's keys, or a headless session's open request. */
internal sealed interface AskTarget

internal data class PaneTarget(val id: String, val questionHash: String, val changed: Long) : AskTarget

internal data class HeadlessTarget(val id: String, val requestId: String, val options: List<String>) : AskTarget

/** The tool a permission asks about and the command or detail it shows. */
internal data class AskCommand(val tool: String, val text: String)

/**
 * An Ask as the phone draws it and sends it. [markdown] is true when the question is the assistant's markdown, drawn with
 * its markup. [asked] is the unix time the question began waiting. [archive] is the Epics.Archive kind and id, null when
 * the conversation has no orrery id. [epicId] and [questionId] are "" outside an epic's Ask; a park has no [questionId].
 * [escalation] is the line naming who escalated an epic's question and why, "" while it is not escalated.
 */
internal data class AskModel(
    val kind: AskKind,
    val who: String,
    val asked: Long,
    val question: String,
    val markdown: Boolean,
    val command: AskCommand?,
    val options: List<AskOption>,
    val putOff: Boolean,
    val archive: Pair<String, String>?,
    val target: AskTarget?,
    val epicId: String,
    val questionId: String,
    val escalation: String = "",
)

private const val LAST_KEYED_OPTION = 9
private const val SECONDS_PER_DAY = 86_400L
private const val STATE_WAITING = "waiting"
private const val RUN_THIS_COMMAND = "Run this command?"

/** The three answers a pane permission offers, in the order keys 1, 2 and 3 send them. */
private val panePermissionOptions = listOf(
    "Yes" to "",
    "Yes, and don’t ask again" to "for commands like this one in this session",
    "No" to "and say what to do instead, below",
)

/**
 * A waiting pane's stored question as its Ask. An AskUserQuestion value draws its first question with one option per
 * entry, "question 1 of N" when it asks N > 1. A tool permission draws its question and command, with the three
 * permission answers. Any other value, including plain text, is a permission whose question is the text itself.
 * Null when the pane is not waiting or has no question.
 */
internal fun paneAsk(pane: Pane): AskModel? {
    if (pane.state != STATE_WAITING || pane.question.isEmpty()) {
        return null
    }
    val who = joinWords(directoryOf(pane.cwd), pane.label)
    val target = PaneTarget(pane.id, pane.questionHash, pane.changed)
    val stored = jsonObject(pane.question)
    val questions = stored?.optJSONArray("questions")
    val tool = stored?.optString("tool").orEmpty()

    if (questions?.optJSONObject(0) != null) {
        val parsed = parseQuestion(pane.question)
        return paneModel(
            pane = pane,
            kind = AskKind.PANE,
            who = if (parsed.count > 1) "$who · question 1 of ${parsed.count}" else who,
            question = parsed.title,
            markdown = parsed.markdown,
            command = null,
            options = parsed.options.mapIndexed { index, option ->
                optionOf(index + 1, option.label, option.description.orEmpty())
            },
            target = target,
        )
    }
    if (tool.isNotEmpty()) {
        val detail = permissionDetail(stored?.optJSONObject("input"))
        return paneModel(
            pane = pane,
            kind = AskKind.PERMISSION,
            who = who,
            question = permissionQuestion(tool, detail),
            markdown = false,
            command = if (detail.isEmpty()) null else AskCommand(tool, detail),
            options = permissionOptions(),
            target = target,
        )
    }
    return paneModel(
        pane = pane,
        kind = AskKind.PERMISSION,
        who = who,
        question = pane.question,
        markdown = false,
        command = null,
        options = permissionOptions(),
        target = target,
    )
}

/** A headless session's open request as its Ask: a permission with the options the session serves, or a question. */
internal fun sessionAsk(pane: Pane, request: SessionRequest): AskModel {
    val permission = request.kind == "permission"
    return AskModel(
        kind = if (permission) AskKind.PERMISSION else AskKind.SESSION,
        who = joinWords(directoryOf(pane.cwd), pane.label),
        asked = pane.changed,
        question = if (permission) "Use ${request.question}?" else request.question,
        markdown = false,
        command = null,
        options = request.options.mapIndexed { index, name -> optionOf(index + 1, name) },
        putOff = request.putOff || pane.putOff,
        archive = archiveOf(pane),
        target = HeadlessTarget(pane.id, request.id, request.options),
        epicId = "",
        questionId = "",
    )
}

/** An epic's question with its choices in the order the planner declared them, and the recommended one's why. */
internal fun epicAsk(epic: EpicRow, question: EpicQuestionRow): AskModel = AskModel(
    kind = AskKind.EPIC,
    who = joinWords(epicProject(epic), epic.slug),
    asked = question.asked,
    question = question.text,
    markdown = false,
    command = null,
    options = question.choices.mapIndexed { index, choice ->
        val recommended = question.recommendedChoice.isNotEmpty() && question.recommendedChoice == choice.id
        optionOf(index + 1, choice.id, choice.does, recommended, if (recommended) question.recommendedWhy else "")
    },
    putOff = false,
    archive = null,
    target = null,
    epicId = epic.id,
    questionId = question.id,
    escalation = if (question.escalated > 0L) {
        "escalated by ${makerWord(question.escalationBy, question.escalationSession)}: ${question.escalationReason}"
    } else {
        ""
    },
)

/** An epic's park as its Ask: the parked reason, with the one answer that runs the parked work again. */
internal fun parkAsk(epic: EpicRow, parked: String): AskModel = AskModel(
    kind = AskKind.PARK,
    who = joinWords(epicProject(epic), epic.slug),
    asked = epic.phaseAt,
    question = parked,
    markdown = false,
    command = null,
    options = listOf(optionOf(1, "Retry", "Run the parked work again")),
    putOff = false,
    archive = null,
    target = null,
    epicId = epic.id,
    questionId = "",
)

/**
 * How long a question has waited, in whole days, once it has waited a day or more; null before that and when there is
 * no asked time.
 */
internal fun staleWords(asked: Long, now: Long): String? {
    if (asked <= 0L) {
        return null
    }
    val days = (now - asked) / SECONDS_PER_DAY
    return when {
        days < 1L -> null
        days == 1L -> "1 day"
        else -> "$days days"
    }
}

private fun paneModel(
    pane: Pane,
    kind: AskKind,
    who: String,
    question: String,
    markdown: Boolean,
    command: AskCommand?,
    options: List<AskOption>,
    target: AskTarget,
): AskModel = AskModel(
    kind = kind,
    who = who,
    asked = pane.changed,
    question = question,
    markdown = markdown,
    command = command,
    options = options,
    putOff = pane.putOff,
    archive = archiveOf(pane),
    target = target,
    epicId = "",
    questionId = "",
)

private fun permissionOptions(): List<AskOption> =
    panePermissionOptions.mapIndexed { index, (name, does) -> optionOf(index + 1, name, does) }

/** A Bash command reads as a command to run, once there is one to show. */
private fun permissionQuestion(tool: String, detail: String): String =
    if (tool == "Bash" && detail.isNotEmpty()) RUN_THIS_COMMAND else "Use $tool?"

/** The command, file or URL a permission's input names, "" when it names none. */
private fun permissionDetail(input: JSONObject?): String =
    listOf("command", "file_path", "url")
        .map { key -> input?.optString(key).orEmpty() }
        .firstOrNull { it.isNotEmpty() }
        .orEmpty()

/**
 * The Epics.Archive target of a conversation: a headless session by its id, a tmux pane by its orrery id, else none.
 */
private fun archiveOf(pane: Pane): Pair<String, String>? = when {
    pane.headless -> "headless" to pane.id
    pane.orreryId.isNotEmpty() -> "session" to pane.orreryId
    else -> null
}

/** The project an epic's Ask names: its repository's directory, or its repository when it has no path. */
internal fun epicProject(epic: EpicRow): String = directoryOf(epic.path).ifEmpty { epic.repository }

/** The last element of a path, "" for no path. */
private fun directoryOf(path: String): String = if (path.isEmpty()) "" else lastPathElement(path)

private fun optionOf(
    n: Int,
    name: String,
    does: String = "",
    recommended: Boolean = false,
    why: String = "",
): AskOption = AskOption(n, name, does, recommended, why, answerable = n <= LAST_KEYED_OPTION)

/** The parts that are not empty, joined with the middle dot the Ask's who uses. */
private fun joinWords(vararg parts: String): String = parts.filter { it.isNotEmpty() }.joinToString(" · ")

private fun jsonObject(stored: String): JSONObject? = try {
    JSONObject(stored)
} catch (e: JSONException) {
    null
}
