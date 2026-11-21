package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.withStyle
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

private const val PLACEHOLDER = "Find in this conversation"

/** One occurrence of a query: [entry] is the index of its text in the transcript, [range] the characters it covers. */
data class FindMatch(val entry: Int, val range: IntRange)

/** How a run of an entry's text is drawn: plain, a match, or the current match. */
enum class FindMark { Plain, Match, Current }

/** A run of an entry's text and how it is marked. */
data class MarkedRun(val text: String, val mark: FindMark)

/**
 * Every case-insensitive occurrence of [query] in [texts], in entry order, each found from the end of the one before as
 * the web finds them, so occurrences do not overlap. An empty query matches nothing.
 */
fun findMatches(texts: List<String>, query: String): List<FindMatch> {
    if (query.isEmpty()) return emptyList()
    val matches = mutableListOf<FindMatch>()
    texts.forEachIndexed { entry, text ->
        var from = 0
        while (true) {
            val start = text.indexOf(query, from, ignoreCase = true)
            if (start < 0) break
            matches.add(FindMatch(entry, start until start + query.length))
            from = start + query.length
        }
    }
    return matches
}

/** The match [step] away from [current] among [count], wrapping past either end. With no matches there is none: -1. */
fun stepFind(current: Int, count: Int, step: Int): Int =
    if (count == 0) -1 else Math.floorMod(current + step, count)

/** The count the find bar shows: `2 of 3`, `No matches` when a query finds none, and nothing for an empty query. */
fun findCountWords(query: String, count: Int, current: Int): String = when {
    query.isEmpty() -> ""
    count == 0 -> "No matches"
    else -> "${current + 1} of $count"
}

/**
 * [text], the text of entry [entry], split at each of its matches in [matches]. The match at index [current] of
 * [matches] is [FindMark.Current]; the other matches of the entry are [FindMark.Match].
 */
fun markedRuns(text: String, entry: Int, matches: List<FindMatch>, current: Int): List<MarkedRun> {
    val runs = mutableListOf<MarkedRun>()
    var from = 0
    for ((index, match) in matches.withIndex()) {
        if (match.entry != entry) continue
        if (match.range.first > from) runs.add(MarkedRun(text.substring(from, match.range.first), FindMark.Plain))
        val mark = if (index == current) FindMark.Current else FindMark.Match
        runs.add(MarkedRun(text.substring(match.range.first, match.range.last + 1), mark))
        from = match.range.last + 1
    }
    if (from < text.length) runs.add(MarkedRun(text.substring(from), FindMark.Plain))
    return runs
}

/** [markedRuns] drawn: the matches tinted, and the current one solid. */
fun findHighlight(text: String, entry: Int, matches: List<FindMatch>, current: Int): AnnotatedString =
    buildAnnotatedString {
        for (run in markedRuns(text, entry, matches, current)) {
            when (run.mark) {
                FindMark.Plain -> append(run.text)
                FindMark.Match -> withStyle(SpanStyle(background = OrreryTokens.Role.markLine)) { append(run.text) }
                FindMark.Current -> withStyle(
                    SpanStyle(color = OrreryTokens.Ink.onAccent, background = OrreryTokens.Role.mark),
                ) { append(run.text) }
            }
        }
    }

/**
 * The find bar over a transcript: the query field, the count words, Previous and Next, and Close. The search action of
 * the keyboard steps to the next match.
 */
@Composable
fun FindBar(
    query: String,
    count: Int,
    current: Int,
    onQuery: (String) -> Unit,
    onNext: () -> Unit,
    onPrevious: () -> Unit,
    onClose: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .testTag("find-bar")
            .background(OrreryTokens.Surface.raised, RoundedCornerShape(OrreryTokens.Radius.two))
            .padding(start = OrreryTokens.Space.twelve, end = OrreryTokens.Space.four),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        BasicTextField(
            value = query,
            onValueChange = onQuery,
            modifier = Modifier.weight(1f).semantics { contentDescription = PLACEHOLDER },
            textStyle = OrreryTokens.Type.body.copy(color = OrreryTokens.Ink.one),
            cursorBrush = SolidColor(OrreryTokens.Role.focus),
            singleLine = true,
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { onNext() }),
            decorationBox = { inner ->
                Box {
                    if (query.isEmpty()) {
                        Text(PLACEHOLDER, color = OrreryTokens.Ink.three, style = OrreryTokens.Type.body)
                    }
                    inner()
                }
            },
        )
        Text(
            text = findCountWords(query, count, current),
            modifier = Modifier.testTag("find-count"),
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.small,
        )
        FindButton(glyph = "↑", description = "Previous match", tag = "find-previous", onClick = onPrevious)
        FindButton(glyph = "↓", description = "Next match", tag = "find-next", onClick = onNext)
        FindButton(glyph = "×", description = "Close find", tag = "find-close", onClick = onClose)
    }
}

@Composable
private fun FindButton(glyph: String, description: String, tag: String, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .size(OrreryTokens.Size.controlTouch)
            .testTag(tag)
            .semantics { contentDescription = description }
            .clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Text(glyph, color = OrreryTokens.Ink.two, style = OrreryTokens.Type.title)
    }
}
