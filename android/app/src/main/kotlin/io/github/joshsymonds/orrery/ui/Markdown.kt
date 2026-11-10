package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import io.github.joshsymonds.orrery.ui.theme.Mocha

enum class ColumnAlign { Start, Center, End }

/**
 * One block of a parsed reply. [lead] is the indent, in monospace character cells, that places the
 * block under the text of the list item it continues; it is 0 outside lists.
 */
sealed interface MdBlock {
    val lead: Int

    data class Paragraph(val text: AnnotatedString, override val lead: Int = 0) : MdBlock

    data class Heading(val level: Int, val text: AnnotatedString, override val lead: Int = 0) : MdBlock

    /** [marker] is the bullet glyph or number, padded on the left to its list's widest marker. */
    data class Item(val marker: String, val text: AnnotatedString, override val lead: Int = 0) : MdBlock

    data class Code(val language: String, val code: String, override val lead: Int = 0) : MdBlock

    /** [rows] starts with the header row; every row has one cell per alignment. */
    data class Table(
        val alignments: List<ColumnAlign>,
        val rows: List<List<AnnotatedString>>,
        override val lead: Int = 0,
    ) : MdBlock

    data class Quote(val blocks: List<MdBlock>, override val lead: Int = 0) : MdBlock

    data class Rule(override val lead: Int = 0) : MdBlock
}

private const val MAX_QUOTE_NESTING = 8
private const val MAX_INLINE_NESTING = 12
private const val ASCII_PUNCTUATION = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
private val BULLETS = listOf("•", "◦", "▪")

private val HEADING = Regex("""^(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$""")
private val RULE = Regex("""^(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$""")
private val ITEM = Regex("""^([ ]*)([-*+]|\d{1,9}[.)])(?:[ \t]+(.*))?$""")
private val DELIMITER_ROW = Regex("""^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-*:?\s*)*\|?\s*$""")

private val CODE_STYLE = SpanStyle(color = Mocha.Peach, background = Mocha.Surface0)
private val BOLD_STYLE = SpanStyle(fontWeight = FontWeight.Bold)
private val ITALIC_STYLE = SpanStyle(fontStyle = FontStyle.Italic)
private val STRIKE_STYLE = SpanStyle(textDecoration = TextDecoration.LineThrough)
private val DEAD_LINK_STYLE = SpanStyle(color = Mocha.Blue)
private val LINK_STYLES = TextLinkStyles(
    style = SpanStyle(color = Mocha.Blue, textDecoration = TextDecoration.Underline),
    pressedStyle = SpanStyle(color = Mocha.Blue, background = Mocha.Surface1, textDecoration = TextDecoration.Underline),
)

/**
 * Parses an assistant reply's markdown into blocks. It never throws and never drops text: markup
 * it cannot close stays literal, and a fence the server cut off runs to the end of the reply.
 */
fun parseMarkdown(text: String): List<MdBlock> = parseLines(splitLines(text), nesting = 0)

/**
 * Styles one line of markdown: code spans, bold, italics, strikethrough, links and bare URLs.
 * With [links] false a link keeps its colour but is not tappable.
 */
fun parseInline(text: String, links: Boolean = true): AnnotatedString {
    val builder = AnnotatedString.Builder(text.length)
    InlineParser(text, builder, links).run(0, text.length, 0)
    return builder.toAnnotatedString()
}

/**
 * Keeps parsed replies by their text so a reply that scrolls back into view, or comes back in the
 * next poll, is not parsed again. The oldest-used entry goes first once [capacity] is reached.
 */
class MarkdownCache(private val capacity: Int = 256) {
    private val entries = object : LinkedHashMap<String, List<MdBlock>>(16, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, List<MdBlock>>?) = size > capacity
    }

    fun blocks(text: String): List<MdBlock> = entries.getOrPut(text) { parseMarkdown(text) }
}

private fun splitLines(text: String): List<String> =
    text.replace("\r\n", "\n").replace('\r', '\n').split('\n').map(::expandLeadingTabs)

private fun expandLeadingTabs(line: String): String {
    if (!line.startsWith('\t') && !line.startsWith(" \t")) return line
    val out = StringBuilder()
    var i = 0
    while (i < line.length && (line[i] == ' ' || line[i] == '\t')) {
        if (line[i] == '\t') repeat(4 - out.length % 4) { out.append(' ') } else out.append(' ')
        i++
    }
    return out.append(line, i, line.length).toString()
}

private fun indentOf(line: String): Int = line.indexOfFirst { it != ' ' }.let { if (it < 0) line.length else it }

private fun parseLines(lines: List<String>, nesting: Int): List<MdBlock> =
    layoutLists(BlockParser(lines, nesting).parse())

/** A block before list layout: an item's [depth] is its nesting, any other block's is the item depth it continues plus one. */
private class Pending(val block: MdBlock, val depth: Int, val isItem: Boolean)

private class BlockParser(private val lines: List<String>, private val nesting: Int) {
    private val out = mutableListOf<Pending>()
    private val listIndents = ArrayList<Int>()
    private var i = 0

    fun parse(): List<Pending> {
        while (i < lines.size) {
            val line = lines[i]
            if (line.isBlank()) {
                i++
                continue
            }
            val indent = indentOf(line)
            while (listIndents.isNotEmpty() && indent <= listIndents.last()) listIndents.removeAt(listIndents.lastIndex)
            val item = itemMatch(line)
            if (item != null) {
                val depth = listIndents.size
                listIndents.add(indent)
                parseItem(item, depth)
            } else {
                parseBlock(line.trimStart(), indent, listIndents.size)
            }
        }
        return out
    }

    private fun itemMatch(line: String): MatchResult? {
        if (RULE.matches(line.trim())) return null
        return ITEM.matchEntire(line.trimEnd())
    }

    private fun parseItem(match: MatchResult, depth: Int) {
        val raw = match.groupValues[2]
        val marker = if (raw[0].isDigit()) raw else BULLETS[minOf(depth, BULLETS.lastIndex)]
        val text = StringBuilder(match.groupValues[3])
        i++
        while (i < lines.size && lines[i].isNotBlank() && !isBlockStart(i)) {
            text.append('\n').append(lines[i].trim())
            i++
        }
        out.add(Pending(MdBlock.Item(marker, parseInline(text.toString())), depth, isItem = true))
    }

    private fun parseBlock(trimmed: String, indent: Int, depth: Int) {
        when {
            fenceOpening(trimmed) != null -> parseFence(trimmed, indent, depth)
            HEADING.matches(trimmed) -> {
                val match = HEADING.matchEntire(trimmed)!!
                val level = match.groupValues[1].length
                out.add(Pending(MdBlock.Heading(minOf(level, 4), parseInline(match.groupValues[2])), depth, isItem = false))
                i++
            }
            RULE.matches(trimmed.trimEnd()) -> {
                out.add(Pending(MdBlock.Rule(), depth, isItem = false))
                i++
            }
            trimmed.startsWith('>') && nesting < MAX_QUOTE_NESTING -> parseQuote(depth)
            isTableStart(i) -> parseTable(depth)
            else -> parseParagraph(depth)
        }
    }

    private fun isBlockStart(index: Int): Boolean {
        val line = lines[index]
        val trimmed = line.trimStart()
        return itemMatch(line) != null ||
            fenceOpening(trimmed) != null ||
            HEADING.matches(trimmed) ||
            RULE.matches(trimmed.trimEnd()) ||
            (trimmed.startsWith('>') && nesting < MAX_QUOTE_NESTING) ||
            isTableStart(index)
    }

    private fun isTableStart(index: Int): Boolean {
        if (index + 1 >= lines.size) return false
        val delimiter = lines[index + 1]
        return '|' in lines[index] && '|' in delimiter && DELIMITER_ROW.matches(delimiter)
    }

    private fun parseFence(trimmed: String, indent: Int, depth: Int) {
        val fence = fenceOpening(trimmed)!!
        val language = trimmed.substring(fence.length).trim().takeWhile { !it.isWhitespace() }
        val body = mutableListOf<String>()
        i++
        while (i < lines.size) {
            val candidate = lines[i].trim()
            if (candidate.length >= fence.length && candidate.all { it == fence[0] }) {
                i++
                break
            }
            body.add(lines[i].drop(minOf(indent, indentOf(lines[i]))))
            i++
        }
        while (body.isNotEmpty() && body.last().isBlank()) body.removeAt(body.lastIndex)
        out.add(Pending(MdBlock.Code(language, body.joinToString("\n")), depth, isItem = false))
    }

    private fun parseQuote(depth: Int) {
        val inner = mutableListOf<String>()
        while (i < lines.size && lines[i].trimStart().startsWith('>')) {
            val rest = lines[i].trimStart().substring(1)
            inner.add(if (rest.startsWith(' ')) rest.substring(1) else rest)
            i++
        }
        out.add(Pending(MdBlock.Quote(parseLines(inner, nesting + 1)), depth, isItem = false))
    }

    private fun parseTable(depth: Int) {
        val header = splitCells(lines[i])
        val alignments = splitCells(lines[i + 1]).map(::alignmentOf)
        i += 2
        val body = mutableListOf<List<String>>()
        while (i < lines.size && lines[i].isNotBlank() && '|' in lines[i]) {
            body.add(splitCells(lines[i]))
            i++
        }
        val rows = listOf(header) + body
        val columns = rows.maxOf { it.size }
        out.add(
            Pending(
                MdBlock.Table(
                    alignments = List(columns) { alignments.getOrElse(it) { ColumnAlign.Start } },
                    rows = rows.map { row -> List(columns) { parseInline(row.getOrElse(it) { "" }) } },
                ),
                depth,
                isItem = false,
            ),
        )
    }

    private fun parseParagraph(depth: Int) {
        val text = StringBuilder(lines[i].trim())
        i++
        while (i < lines.size && lines[i].isNotBlank() && !isBlockStart(i)) {
            text.append('\n').append(lines[i].trim())
            i++
        }
        out.add(Pending(MdBlock.Paragraph(parseInline(text.toString())), depth, isItem = false))
    }
}

/** The opening fence run of a fenced code line, or null when [trimmed] does not open one. */
private fun fenceOpening(trimmed: String): String? {
    val c = trimmed.firstOrNull() ?: return null
    if (c != '`' && c != '~') return null
    val run = trimmed.takeWhile { it == c }
    if (run.length < 3) return null
    if (c == '`' && '`' in trimmed.substring(run.length)) return null
    return run
}

private fun splitCells(line: String): List<String> {
    var s = line.trim()
    if (s.startsWith('|')) s = s.substring(1)
    if (s.endsWith('|') && !s.endsWith("\\|")) s = s.dropLast(1)
    val cells = mutableListOf<String>()
    val cell = StringBuilder()
    var i = 0
    while (i < s.length) {
        val c = s[i]
        when {
            c == '\\' && i + 1 < s.length -> {
                cell.append(c).append(s[i + 1])
                i += 2
            }
            c == '|' -> {
                cells.add(cell.toString().trim())
                cell.clear()
                i++
            }
            else -> {
                cell.append(c)
                i++
            }
        }
    }
    cells.add(cell.toString().trim())
    return cells
}

private fun alignmentOf(cell: String): ColumnAlign = when {
    cell.length > 1 && cell.startsWith(':') && cell.endsWith(':') -> ColumnAlign.Center
    cell.endsWith(':') -> ColumnAlign.End
    else -> ColumnAlign.Start
}

/**
 * Pads each list's markers to its widest at every depth and turns depths into character leads, so
 * nested items and continued blocks start under their parent item's text.
 */
private fun layoutLists(pending: List<Pending>): List<MdBlock> {
    val result = ArrayList<MdBlock>(pending.size)
    var start = 0
    while (start < pending.size) {
        if (!pending[start].isItem && pending[start].depth == 0) {
            result.add(pending[start].block)
            start++
            continue
        }
        val ordered = isOrderedItem(pending[start])
        var end = start + 1
        while (end < pending.size && (pending[end].isItem || pending[end].depth > 0)) {
            val p = pending[end]
            if (p.isItem && p.depth == 0 && isOrderedItem(p) != ordered) break
            end++
        }
        val run = pending.subList(start, end)
        val maxDepth = run.maxOf { it.depth }
        val widths = IntArray(maxDepth + 1)
        run.forEach { p -> if (p.isItem) widths[p.depth] = maxOf(widths[p.depth], (p.block as MdBlock.Item).marker.length) }
        val textStart = IntArray(maxDepth + 1)
        run.forEach { p ->
            if (p.isItem) {
                val item = p.block as MdBlock.Item
                val lead = if (p.depth == 0) 0 else textStart[p.depth - 1]
                textStart[p.depth] = lead + widths[p.depth] + 1
                result.add(item.copy(marker = item.marker.padStart(widths[p.depth]), lead = lead))
            } else {
                result.add(withLead(p.block, textStart[p.depth - 1]))
            }
        }
        start = end
    }
    return result
}

private fun isOrderedItem(p: Pending): Boolean = p.isItem && (p.block as MdBlock.Item).marker[0].isDigit()

private fun withLead(block: MdBlock, lead: Int): MdBlock = when (block) {
    is MdBlock.Paragraph -> block.copy(lead = lead)
    is MdBlock.Heading -> block.copy(lead = lead)
    is MdBlock.Item -> block.copy(lead = lead)
    is MdBlock.Code -> block.copy(lead = lead)
    is MdBlock.Table -> block.copy(lead = lead)
    is MdBlock.Quote -> block.copy(lead = lead)
    is MdBlock.Rule -> block.copy(lead = lead)
}

private fun isWebUrl(url: String): Boolean {
    val lower = url.lowercase()
    return lower.startsWith("https://") || lower.startsWith("http://") || lower.startsWith("mailto:")
}

/** Each step appends at least one character's worth of output and returns the index after what it consumed. */
private class InlineParser(
    private val s: String,
    private val builder: AnnotatedString.Builder,
    private val links: Boolean,
) {
    private var inLink = false

    fun run(start: Int, end: Int, depth: Int) {
        var i = start
        while (i < end) {
            i = when (s[i]) {
                '\\' -> escape(i, end)
                '`' -> codeSpan(i, end)
                '*', '_', '~' -> emphasis(i, end, depth)
                '[' -> link(i, end, depth)
                '<' -> autolink(i, end)
                'h', 'H' -> bareUrl(i, end)
                else -> {
                    builder.append(s[i])
                    i + 1
                }
            }
        }
    }

    private fun escape(i: Int, end: Int): Int {
        val next = if (i + 1 < end) s[i + 1] else null
        return when {
            next != null && next in ASCII_PUNCTUATION -> {
                builder.append(next)
                i + 2
            }
            next == '\n' -> {
                builder.append('\n')
                i + 2
            }
            else -> {
                builder.append('\\')
                i + 1
            }
        }
    }

    private fun runLength(from: Int, c: Char, end: Int): Int {
        var j = from
        while (j < end && s[j] == c) j++
        return j - from
    }

    /** The start of the next run of exactly [length] backticks at or after [from], or -1. */
    private fun backtickRun(from: Int, end: Int, length: Int): Int {
        var j = from
        while (j < end) {
            if (s[j] == '`') {
                val run = runLength(j, '`', end)
                if (run == length) return j
                j += run
            } else {
                j++
            }
        }
        return -1
    }

    private fun codeSpan(i: Int, end: Int): Int {
        val length = runLength(i, '`', end)
        val close = backtickRun(i + length, end, length)
        if (close < 0) {
            builder.append(s, i, i + length)
            return i + length
        }
        var content = s.substring(i + length, close).replace('\n', ' ')
        if (content.length >= 2 && content.startsWith(' ') && content.endsWith(' ') && content.isNotBlank()) {
            content = content.substring(1, content.length - 1)
        }
        builder.pushStyle(CODE_STYLE)
        builder.append(content)
        builder.pop()
        return close + length
    }

    private fun emphasis(i: Int, end: Int, depth: Int): Int {
        val c = s[i]
        val run = runLength(i, c, end)
        val length = when {
            c == '~' -> if (run == 2) 2 else 0
            run == 1 -> 1
            else -> 2
        }
        val after = i + length
        val opens = length > 0 &&
            depth < MAX_INLINE_NESTING &&
            after < end &&
            !s[after].isWhitespace() &&
            (c != '_' || i == 0 || !s[i - 1].isLetterOrDigit())
        val close = if (opens) closer(after, end, c, length) else -1
        if (close < 0) {
            builder.append(s, i, i + run)
            return i + run
        }
        builder.pushStyle(
            when {
                c == '~' -> STRIKE_STYLE
                length == 2 -> BOLD_STYLE
                else -> ITALIC_STYLE
            },
        )
        run(after, close, depth + 1)
        builder.pop()
        return close + length
    }

    /** Where the delimiter of [length] [c]s that closes an emphasis opened before [from] starts, or -1. */
    private fun closer(from: Int, end: Int, c: Char, length: Int): Int {
        var j = from
        while (j < end) {
            val ch = s[j]
            when {
                ch == '\\' -> j += 2
                ch == '`' -> {
                    val run = runLength(j, '`', end)
                    val close = backtickRun(j + run, end, run)
                    j = if (close >= 0) close + run else j + run
                }
                ch == c -> {
                    val run = runLength(j, c, end)
                    val runEnd = j + run
                    val fits = if (length == 1) run != 2 else run >= 2
                    val at = runEnd - length
                    if (fits && at > from && !s[at - 1].isWhitespace() &&
                        (c != '_' || runEnd >= s.length || !s[runEnd].isLetterOrDigit())
                    ) {
                        return at
                    }
                    j = runEnd
                }
                else -> j++
            }
        }
        return -1
    }

    private fun link(i: Int, end: Int, depth: Int): Int {
        val labelEnd = if (inLink || depth >= MAX_INLINE_NESTING) -1 else closingBracket(i, end)
        if (labelEnd < 0 || labelEnd + 1 >= end || s[labelEnd + 1] != '(') {
            builder.append('[')
            return i + 1
        }
        val destinationEnd = closingParen(labelEnd + 1, end)
        if (destinationEnd < 0) {
            builder.append('[')
            return i + 1
        }
        var destination = s.substring(labelEnd + 2, destinationEnd).trim()
        if (destination.startsWith('<') && destination.contains('>')) {
            destination = destination.substring(1, destination.indexOf('>'))
        } else {
            destination = destination.takeWhile { !it.isWhitespace() }
        }
        val labelStart = i + 1
        inLink = true
        if (links && isWebUrl(destination)) {
            builder.pushLink(LinkAnnotation.Url(destination, LINK_STYLES))
        } else {
            builder.pushStyle(DEAD_LINK_STYLE)
        }
        if (labelEnd == labelStart) builder.append(destination) else run(labelStart, labelEnd, depth + 1)
        builder.pop()
        inLink = false
        return destinationEnd + 1
    }

    private fun closingBracket(open: Int, end: Int): Int {
        var level = 0
        var j = open
        while (j < end) {
            when (s[j]) {
                '\\' -> j++
                '\n' -> return -1
                '`' -> {
                    val run = runLength(j, '`', end)
                    val close = backtickRun(j + run, end, run)
                    if (close >= 0) j = close + run - 1 else j += run - 1
                }
                '[' -> level++
                ']' -> {
                    level--
                    if (level == 0) return j
                }
            }
            j++
        }
        return -1
    }

    private fun closingParen(open: Int, end: Int): Int {
        var level = 0
        var j = open
        while (j < end) {
            when (s[j]) {
                '\\' -> j++
                '\n' -> return -1
                '(' -> level++
                ')' -> {
                    level--
                    if (level == 0) return j
                }
            }
            j++
        }
        return -1
    }

    private fun autolink(i: Int, end: Int): Int {
        val close = s.indexOf('>', i + 1).takeIf { it in (i + 1) until end } ?: -1
        val url = if (close > 0) s.substring(i + 1, close) else ""
        if (inLink || close < 0 || !isWebUrl(url) || url.any { it.isWhitespace() }) {
            builder.append('<')
            return i + 1
        }
        appendUrl(url)
        return close + 1
    }

    private fun bareUrl(i: Int, end: Int): Int {
        val rest = s.regionMatches(i, "https://", 0, 8, ignoreCase = true) ||
            s.regionMatches(i, "http://", 0, 7, ignoreCase = true)
        if (inLink || !rest || (i > 0 && s[i - 1].isLetterOrDigit())) {
            builder.append(s[i])
            return i + 1
        }
        var j = i
        while (j < end && !s[j].isWhitespace() && s[j] !in "<>`\"") j++
        while (j > i) {
            val last = s[j - 1]
            val url = s.substring(i, j)
            val unbalanced = (last == ')' && url.count { it == '(' } < url.count { it == ')' }) ||
                (last == ']' && url.count { it == '[' } < url.count { it == ']' })
            if (last in ".,:;!?'*_~" || unbalanced) j-- else break
        }
        val url = s.substring(i, j)
        if (url.substringAfter("://").isEmpty()) {
            builder.append(s[i])
            return i + 1
        }
        appendUrl(url)
        return j
    }

    private fun appendUrl(url: String) {
        if (links) builder.pushLink(LinkAnnotation.Url(url, LINK_STYLES)) else builder.pushStyle(DEAD_LINK_STYLE)
        builder.append(url)
        builder.pop()
    }
}
