package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.KeysResult
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONException
import org.json.JSONObject

private data class QuestionOption(val label: String, val key: String)

private class ParsedQuestion(val title: String, val options: List<QuestionOption>)

private val toolOptions = listOf(
    QuestionOption("1 Yes", "1"),
    QuestionOption("2 Yes, don't ask again", "2"),
    QuestionOption("3 No", "3"),
)

/**
 * Reads the stored question. An AskUserQuestion value has one button per option,
 * posting its 1-based number. Any other value, including a truncated marker or
 * plain text, shows the title and the three permission buttons.
 */
private fun parseQuestion(stored: String): ParsedQuestion {
    val json = try {
        JSONObject(stored)
    } catch (e: JSONException) {
        return ParsedQuestion(stored, toolOptions)
    }

    val first = json.optJSONArray("questions")?.optJSONObject(0)
    if (first != null) {
        val options = first.optJSONArray("options")
        val labels = options?.let { array ->
            List(array.length()) { index -> array.optJSONObject(index)?.optString("label").orEmpty() }
        }.orEmpty()
        return ParsedQuestion(
            first.optString("question"),
            labels.mapIndexed { index, label -> QuestionOption(label, (index + 1).toString()) },
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

    Column(modifier = modifier.fillMaxWidth().padding(16.dp)) {
        Text(question.title)
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
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(option.label)
            }
        }
        if (status.isNotEmpty()) {
            Text(status)
        }
    }
}
