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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.github.joshsymonds.orrery.api.KeysResult
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.Mocha
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONException
import org.json.JSONObject

private data class QuestionOption(val label: String, val key: String, val description: String?)

private class ParsedQuestion(val title: String, val options: List<QuestionOption>)

private val toolOptions = listOf(
    QuestionOption("1 Yes", "1", null),
    QuestionOption("2 Yes, don't ask again", "2", null),
    QuestionOption("3 No", "3", null),
)

/**
 * Reads the stored question. An AskUserQuestion value has one button per option,
 * posting its 1-based number, with the option's description under its label when it
 * has one. Any other value, including a truncated marker or plain text, shows the
 * title and the three permission buttons.
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
    val cardShape = RoundedCornerShape(14.dp)
    val buttonShape = RoundedCornerShape(10.dp)

    Column(modifier = modifier.fillMaxWidth().padding(16.dp)) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .clip(cardShape)
                .background(Mocha.Peach.copy(alpha = 0.06f))
                .border(1.dp, Mocha.Peach, cardShape)
                .padding(12.dp),
        ) {
            Text("CLAUDE IS ASKING", color = Mocha.Peach, fontWeight = FontWeight.Bold, fontSize = 12.sp)
            Text(question.title, fontWeight = FontWeight.Bold, modifier = Modifier.padding(top = 2.dp))
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
                    modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                    shape = buttonShape,
                    colors = ButtonDefaults.buttonColors(
                        containerColor = Mocha.Surface0,
                        contentColor = Mocha.Text,
                    ),
                    border = BorderStroke(1.dp, Mocha.Surface1),
                    elevation = null,
                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 10.dp),
                ) {
                    Column(modifier = Modifier.fillMaxWidth()) {
                        Text(option.label)
                        option.description?.let { description ->
                            Text(description, color = Mocha.Subtext0, fontSize = 12.sp)
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
