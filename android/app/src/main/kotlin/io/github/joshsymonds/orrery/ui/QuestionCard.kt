package io.github.joshsymonds.orrery.ui

import org.json.JSONException
import org.json.JSONObject

internal data class QuestionOption(val label: String, val key: String, val description: String?)

/**
 * [markdown] is true when the title and options were written by the assistant, so they may carry its markdown. [count] is how
 * many questions the stored value asks; 1 for a tool permission and for plain text.
 */
internal class ParsedQuestion(
    val title: String,
    val options: List<QuestionOption>,
    val markdown: Boolean = false,
    val count: Int = 1,
)

private val toolOptions = listOf(
    QuestionOption("1 Yes", "1", null),
    QuestionOption("2 Yes, don't ask again", "2", null),
    QuestionOption("3 No", "3", null),
)

/**
 * Reads the stored question. An AskUserQuestion value has one button per option,
 * posting its 1-based number, with the option's description under its label when it
 * has one; its text is the assistant's markdown. Any other value, including a
 * truncated marker or plain text, shows the title and the three permission buttons.
 */
internal fun parseQuestion(stored: String): ParsedQuestion {
    val json = try {
        JSONObject(stored)
    } catch (e: JSONException) {
        return ParsedQuestion(stored, toolOptions)
    }

    val questions = json.optJSONArray("questions")
    val first = questions?.optJSONObject(0)
    if (questions != null && first != null) {
        val options = first.optJSONArray("options")
        val entries = options?.let { array ->
            List(array.length()) { index -> array.optJSONObject(index) }
        }.orEmpty()
        return ParsedQuestion(
            first.optString("question"),
            entries.mapIndexed { index, entry ->
                QuestionOption(
                    label = entry?.optString("label").orEmpty(),
                    key = (index + 1).toString(),
                    description = entry?.optString("description")?.takeIf { it.isNotEmpty() },
                )
            },
            markdown = true,
            count = questions.length(),
        )
    }

    val input = json.optJSONObject("input")
    val detail = listOf("command", "file_path", "url")
        .firstNotNullOfOrNull { key -> input?.optString(key)?.takeIf { it.isNotEmpty() } }
    val tool = json.optString("tool")
    return ParsedQuestion(if (detail == null) tool else "$tool: $detail", toolOptions)
}
