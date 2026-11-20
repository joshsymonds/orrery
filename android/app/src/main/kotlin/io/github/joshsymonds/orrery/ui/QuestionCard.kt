package io.github.joshsymonds.orrery.ui

import org.json.JSONException
import org.json.JSONObject

internal data class QuestionOption(val label: String, val description: String?)

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

/**
 * Reads the stored question. An AskUserQuestion value has one button per option,
 * posting its 1-based number, with the option's description under its label when it
 * has one; its text is the assistant's markdown. Any other value, including a
 * truncated marker or plain text, is a title with no options: the Ask gives a tool permission and plain text its three
 * permission answers itself.
 */
internal fun parseQuestion(stored: String): ParsedQuestion {
    val json = try {
        JSONObject(stored)
    } catch (e: JSONException) {
        return ParsedQuestion(stored, emptyList())
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
            entries.map { entry ->
                QuestionOption(
                    label = entry?.optString("label").orEmpty(),
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
    return ParsedQuestion(if (detail == null) tool else "$tool: $detail", emptyList())
}
