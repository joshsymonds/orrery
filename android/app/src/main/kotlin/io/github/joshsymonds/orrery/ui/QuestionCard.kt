package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.AnnotatedString
import io.github.joshsymonds.orrery.api.KeysResult
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONException
import org.json.JSONObject

internal data class QuestionOption(val label: String, val key: String, val description: String?)

/**
 * [markdown] is true when the title and options were written by the assistant and are drawn as markdown. [count] is how
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

/**
 * A pane's question with one button per option. A tap posts that option's key with
 * the pane's question hash, so the server refuses it if the prompt has moved on.
 */
@Composable
fun QuestionCard(pane: Pane, client: OrreryClient, onRefresh: () -> Unit, modifier: Modifier = Modifier) {
    val question = remember(pane.question) { parseQuestion(pane.question) }
    val scope = rememberCoroutineScope()
    var status by remember(pane.question) { mutableStateOf("") }
    val cardShape = RoundedCornerShape(OrreryTokens.Radius.three)
    val buttonShape = RoundedCornerShape(OrreryTokens.Radius.two)

    Column(modifier = modifier.fillMaxWidth().padding(OrreryTokens.Space.sixteen)) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .clip(cardShape)
                .background(OrreryTokens.Role.attentionFill)
                .border(OrreryTokens.Size.hairline, OrreryTokens.Role.attention, cardShape)
                .padding(OrreryTokens.Space.twelve),
        ) {
            Text(
                "CLAUDE IS ASKING".inLabelCase(),
                color = OrreryTokens.Role.attention,
                style = OrreryTokens.Type.label,
            )
            if (question.markdown) {
                MarkdownBody(
                    blocks = remember(question.title) { parseMarkdown(question.title) },
                    modifier = Modifier.padding(top = OrreryTokens.Space.two),
                    fontWeight = OrreryTokens.Font.Weight.bold,
                )
            } else {
                Text(
                    question.title,
                    style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                    modifier = Modifier.padding(top = OrreryTokens.Space.two),
                )
            }
            question.options.forEach { option ->
                Button(
                    onClick = {
                        scope.launch {
                            val result = withContext(Dispatchers.IO) {
                                try {
                                    client.sendKeys(pane.id, listOf(option.key), pane.questionHash)
                                } catch (e: IOException) {
                                    null
                                }
                            }
                            when (result) {
                                null -> status = "not sent"
                                KeysResult.PromptChanged -> {
                                    status = "prompt changed"
                                    onRefresh()
                                }
                                KeysResult.Sent -> onRefresh()
                            }
                        }
                    },
                    modifier = Modifier.fillMaxWidth().padding(top = OrreryTokens.Space.eight),
                    shape = buttonShape,
                    colors = ButtonDefaults.buttonColors(
                        containerColor = OrreryTokens.Surface.raised,
                        contentColor = OrreryTokens.Ink.one,
                    ),
                    border = BorderStroke(OrreryTokens.Size.hairline, OrreryTokens.Line.firm),
                    elevation = null,
                    contentPadding = PaddingValues(
                        horizontal = OrreryTokens.Space.twelve,
                        vertical = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                    ),
                ) {
                    Column(modifier = Modifier.fillMaxWidth()) {
                        Text(optionText(option.label, question.markdown))
                        option.description?.let { description ->
                            Text(
                                optionText(description, question.markdown),
                                color = OrreryTokens.Ink.two,
                                style = OrreryTokens.Type.small,
                            )
                        }
                    }
                }
            }
            if (status.isNotEmpty()) {
                Text(status)
            }
        }
    }
}

/** An option's text, its inline markdown styled when the question is the assistant's; links are not tappable inside a button. */
private fun optionText(text: String, markdown: Boolean): AnnotatedString =
    if (markdown) parseInline(text, links = false) else AnnotatedString(text)
