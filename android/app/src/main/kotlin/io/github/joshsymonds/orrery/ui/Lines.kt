package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

/** The colour a line's thread is drawn in, read by tests from the thread's node. */
internal val LineColourKey = SemanticsPropertyKey<Color>("LineColour")

/**
 * A line: the hairline thread down its left edge, in the live tone while [live] and in the firm line otherwise, then its
 * [head] row and what it [opened] under the head. The head row is the line's `line:<kind>` node, merged so its words read
 * together, and it is pressed when [onTap] is set; a line with nothing to open has no [onTap]. A line draws no rail.
 */
@Composable
private fun LineFrame(
    kind: String,
    live: Boolean = false,
    onTap: (() -> Unit)? = null,
    opened: @Composable () -> Unit = {},
    head: @Composable RowScope.() -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .height(IntrinsicSize.Min)
            .padding(vertical = OrreryTokens.Space.four),
    ) {
        val threadColour = if (live) OrreryTokens.Role.live else OrreryTokens.Line.firm
        Box(
            modifier = Modifier
                .testTag("line-thread")
                .semantics { this[LineColourKey] = threadColour }
                .width(OrreryTokens.Size.hairline)
                .fillMaxHeight()
                .background(threadColour),
        )
        Column(modifier = Modifier.weight(1f).padding(start = OrreryTokens.Space.eight + OrreryTokens.Space.two)) {
            Row(
                modifier = Modifier
                    .testTag("line:$kind")
                    .semantics(mergeDescendants = true) {}
                    .fillMaxWidth()
                    .let { if (onTap != null) it.clickable(onClick = onTap) else it },
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                content = head,
            )
            opened()
        }
    }
}

/** What a line opens under its head: its text, in [colour], with the find's marks drawn from [base] in the entry's text. */
@Composable
private fun OpenedText(text: String, colour: Color, marks: EntryMarks? = null, base: Int = 0) {
    FindText(
        text = markedText(AnnotatedString(text), base, marks),
        modifier = Modifier.fillMaxWidth().padding(top = OrreryTokens.Space.two),
        color = colour,
        style = OrreryTokens.Type.small,
    )
}

/** The caret of a line that opens: a chevron turned down while it is open. */
@Composable
private fun LineCaret(isOpen: Boolean) {
    Text(
        text = "›",
        modifier = Modifier
            .width(OrreryTokens.Space.eight)
            .graphicsLayer { rotationZ = if (isOpen) 90f else 0f },
        color = OrreryTokens.Ink.four,
        style = OrreryTokens.Type.small,
        maxLines = 1,
    )
}

@Composable
private fun LineMark(symbol: String, colour: Color) {
    Text(text = symbol, color = colour, style = OrreryTokens.Type.small, maxLines = 1)
}

/** The tool's glyph, described by its kind word for a reader of the tree. */
@Composable
private fun ToolGlyph(kind: String) {
    Box(modifier = Modifier.testTag("glyph").semantics { contentDescription = kind }) {
        Text(text = glyphSymbolOf(kind), color = OrreryTokens.Ink.three, style = OrreryTokens.Type.small, maxLines = 1)
    }
}

/** The kind of glyph a tool's line draws: a doc, an edit, a shell, a search, an agent, or a gear for any other tool. */
internal fun toolGlyphKindOf(name: String): String = when (name) {
    "Read" -> "doc"
    "Write", "Edit", "MultiEdit", "NotebookEdit" -> "edit"
    "Bash" -> "shell"
    "Grep", "Glob" -> "search"
    "Agent", "Task" -> "agent"
    else -> "gear"
}

private fun glyphSymbolOf(kind: String): String = when (kind) {
    "doc" -> "▤"
    "edit" -> "✎"
    "shell" -> "❯"
    "search" -> "⌕"
    "agent" -> "◇"
    else -> "⚙"
}

/**
 * A tool call as one line: its tool's glyph, its name and its input, the lines it added and removed when either is above
 * 0 (in the ok and bad tones), then a done or failed mark, or the word running while [running], whose thread is drawn in
 * the live tone. A call with a result opens it under the line on a tap, and a second tap folds it again; a failed result
 * is drawn in the bad tone.
 */
@Composable
internal fun ToolLine(
    call: ToolCall,
    running: Boolean = false,
    expanded: Set<String>,
    onToggle: (String) -> Unit,
    marks: EntryMarks? = null,
    base: Int = 0,
) {
    val isOpen = call.id in expanded
    val result = call.result
    val tap: (() -> Unit)? = if (result != null) ({ onToggle(call.id) }) else null
    LineFrame(
        kind = "tool",
        live = running,
        onTap = tap,
        opened = {
            if (isOpen && result != null) {
                OpenedText(
                    result,
                    if (call.error) OrreryTokens.Role.bad else OrreryTokens.Ink.two,
                    marks,
                    base + call.input.length + 1,
                )
            }
        },
    ) {
        ToolGlyph(toolGlyphKindOf(call.name))
        Text(
            text = call.name,
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
        // An input that holds a match draws whole, so the match is on the line rather than past its ellipsis.
        FindText(
            text = markedText(AnnotatedString(call.input), base, marks),
            modifier = Modifier.weight(1f),
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.code,
            maxLines = if (holdsMatch(marks, base, call.input.length)) Int.MAX_VALUE else 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (call.added > 0 || call.removed > 0) {
            Text(text = "+${call.added}", color = OrreryTokens.Role.ok, style = OrreryTokens.Type.small, maxLines = 1)
            Text(text = "−${call.removed}", color = OrreryTokens.Role.bad, style = OrreryTokens.Type.small, maxLines = 1)
        }
        when {
            running -> LineMark("running", OrreryTokens.Role.live)
            call.error -> LineMark("✗", OrreryTokens.Role.bad)
            result != null -> LineMark("✓", OrreryTokens.Role.ok)
        }
    }
}

/**
 * A run of tool calls as one line: its count, the tools with their counts, the word running while one of its calls
 * (named in [runningIds]) runs, and a trail of one dot per call in call order. A tap opens a line per call under it,
 * and a second tap folds them again. The run is keyed by its first call.
 */
@Composable
internal fun ToolRun(
    calls: List<ToolCall>,
    expanded: Set<String>,
    onToggle: (String) -> Unit,
    runningIds: Set<String> = emptySet(),
    marks: EntryMarks? = null,
) {
    val key = calls.first().id
    val isOpen = key in expanded
    val running = calls.any { it.id in runningIds }
    val bases = callBases(calls)
    LineFrame(
        kind = "run",
        live = running,
        onTap = { onToggle(key) },
        opened = {
            if (isOpen) {
                Column(modifier = Modifier.padding(top = OrreryTokens.Space.two)) {
                    calls.forEachIndexed { index, call ->
                        ToolLine(
                            call,
                            running = call.id in runningIds,
                            expanded = expanded,
                            onToggle = onToggle,
                            marks = marks,
                            base = bases[index],
                        )
                    }
                }
            }
        },
    ) {
        LineCaret(isOpen)
        Text(
            text = "${calls.size} tool calls",
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
        Text(
            text = toolCounts(calls),
            modifier = Modifier.weight(1f),
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.small,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (running) LineMark("running", OrreryTokens.Role.live)
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four)) {
            calls.forEach { call -> CallDot(dotColourOf(call, runningIds)) }
        }
    }
}

@Composable
private fun CallDot(colour: Color) {
    Box(
        modifier = Modifier
            .testTag("dot")
            .size(OrreryTokens.Size.dot)
            .clip(CircleShape)
            .background(colour),
    )
}

/**
 * A call's dot: the live tone while it runs, the bad tone when it failed, the quiet tone when it finished, and the firm
 * line when it has no result and is not running.
 */
private fun dotColourOf(call: ToolCall, runningIds: Set<String>): Color = when {
    call.id in runningIds -> OrreryTokens.Role.live
    call.result == null -> OrreryTokens.Line.firm
    call.error -> OrreryTokens.Role.bad
    else -> OrreryTokens.Ink.four
}

/** Names the calls' tools in order of first appearance, with a count when a name repeats, joined with commas. */
private fun toolCounts(calls: List<ToolCall>): String {
    val counts = LinkedHashMap<String, Int>()
    calls.forEach { counts.merge(it.name, 1, Int::plus) }
    return counts.entries.joinToString(", ") { (name, count) ->
        if (count > 1) "$name ×$count" else name
    }
}

/** The model's thinking as one line, its word `thinking`; a tap opens its text under the line, and a second tap folds it. */
@Composable
internal fun ThinkingLine(text: String, key: String, expanded: Set<String>, onToggle: (String) -> Unit) {
    val isOpen = key in expanded
    LineFrame(
        kind = "thinking",
        onTap = { onToggle(key) },
        opened = { if (isOpen) OpenedText(text, OrreryTokens.Ink.three) },
    ) {
        LineCaret(isOpen)
        Text(text = "thinking", color = OrreryTokens.Ink.three, style = OrreryTokens.Type.small, maxLines = 1)
    }
}

/**
 * A step's brief, or an engine prompt, folded to one line: `Brief` with `from the engine`, or `Engine` with the text's
 * first line. A tap opens the whole text under the line, and a second tap folds it.
 */
@Composable
internal fun EngineFold(
    brief: Boolean,
    text: String,
    key: String,
    expanded: Set<String>,
    onToggle: (String) -> Unit,
    marks: EntryMarks? = null,
) {
    val isOpen = key in expanded
    val words = if (brief) AnnotatedString("from the engine") else markedText(AnnotatedString(text.lineSequence().first()), 0, marks)
    LineFrame(
        kind = "fold",
        onTap = { onToggle(key) },
        opened = { if (isOpen) OpenedText(text, OrreryTokens.Ink.three, marks) },
    ) {
        LineCaret(isOpen)
        Text(
            text = if (brief) "Brief" else "Engine",
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
        FindText(
            text = words,
            modifier = Modifier.weight(1f),
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.small,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * A background task or a finished subagent as one line: its agent or shell glyph, its name (the subagent's type, or
 * `shell`), its description, and its end word. The thread is drawn in the live tone while the task [status] is running.
 */
@Composable
internal fun TaskLine(type: String, subagentType: String, description: String, status: String, marks: EntryMarks? = null) {
    val name = taskNameOf(type, subagentType)
    val endColour = when (status) {
        "failed" -> OrreryTokens.Role.bad
        "running" -> OrreryTokens.Role.live
        else -> OrreryTokens.Ink.three
    }
    val descriptionBase = name.length + 1
    LineFrame(kind = "task", live = status == "running") {
        ToolGlyph(if (type == "shell") "shell" else "agent")
        FindText(
            text = markedText(AnnotatedString(name), 0, marks),
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
        // A description that holds a match draws whole, so the match is on the line rather than past its ellipsis.
        FindText(
            text = markedText(AnnotatedString(description), descriptionBase, marks),
            modifier = Modifier.weight(1f),
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.small,
            maxLines = if (holdsMatch(marks, descriptionBase, description.length)) Int.MAX_VALUE else 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(text = taskEndWordOf(status), color = endColour, style = OrreryTokens.Type.small, maxLines = 1)
    }
}

/** The name a task line draws: `shell` for a shell, else the subagent's type, or `agent` when it has none. */
internal fun taskNameOf(type: String, subagentType: String): String =
    if (type == "shell") "shell" else subagentType.ifEmpty { "agent" }

/** The word a task's line ends with: a finished task reads `finished`, and every other status reads as it is. */
private fun taskEndWordOf(status: String): String = if (status == "done") "finished" else status

/**
 * A session event as one line: its notice word as the label, then its text. An error is drawn in the bad tone and marked
 * with a cross.
 */
@Composable
internal fun NoticeLine(notice: ConvoEntry.Notice, marks: EntryMarks? = null) {
    val tone = if (notice.error) OrreryTokens.Role.bad else OrreryTokens.Ink.three
    val label = noticeLabel(notice.sort).inLabelCase()
    LineFrame(kind = "notice") {
        FindText(
            text = markedText(AnnotatedString(label), 0, marks),
            color = tone,
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
        // The text follows the label and a line break, as findParts joins them.
        FindText(
            text = markedText(AnnotatedString(notice.text), label.length + 1, marks),
            modifier = Modifier.weight(1f),
            color = tone,
            style = OrreryTokens.Type.small,
        )
        if (notice.error) LineMark("✗", OrreryTokens.Role.bad)
    }
}
