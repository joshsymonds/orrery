package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import io.github.joshsymonds.orrery.ui.theme.MochaTerminal

private const val ESC = '\u001B'
private const val BEL = '\u0007'
private val CUBE_LEVELS = intArrayOf(0, 95, 135, 175, 215, 255)

/**
 * Turns `tmux capture-pane -e` output into text styled by its SGR sequences. Every other escape
 * sequence is removed, and a sequence cut off by the end of the input is dropped.
 */
fun parseAnsi(text: String): AnnotatedString {
    val out = StyledOutput()
    var i = 0
    while (i < text.length) {
        val c = text[i]
        i = if (c == ESC) skipEscape(text, i, out) else {
            out.append(c)
            i + 1
        }
    }
    return out.build()
}

/** Handles the escape sequence starting at the ESC at [start] and returns the index after it. */
private fun skipEscape(text: String, start: Int, out: StyledOutput): Int {
    if (start + 1 >= text.length) return text.length
    return when (text[start + 1]) {
        '[' -> skipCsi(text, start, out)
        ']' -> skipOsc(text, start + 2)
        else -> skipOtherEscape(text, start + 1)
    }
}

private fun skipCsi(text: String, start: Int, out: StyledOutput): Int {
    var j = start + 2
    while (j < text.length && text[j] in ' '..'?') j++
    if (j == text.length) return text.length
    val final = text[j]
    if (final !in '@'..'~') return j
    if (final == 'm') out.applySgr(text.substring(start + 2, j))
    return j + 1
}

private fun skipOsc(text: String, from: Int): Int {
    for (j in from until text.length) {
        when (text[j]) {
            BEL -> return j + 1
            ESC -> return if (text.getOrNull(j + 1) == '\\') j + 2 else j
        }
    }
    return text.length
}

private fun skipOtherEscape(text: String, from: Int): Int {
    var j = from
    while (j < text.length && text[j] in ' '..'/') j++
    if (j == text.length) return text.length
    return if (text[j] in '0'..'~') j + 1 else j
}

private data class Attributes(
    val fg: Color? = null,
    val bg: Color? = null,
    val bold: Boolean = false,
    val italic: Boolean = false,
) {
    fun toSpanStyle() = SpanStyle(
        color = fg ?: Color.Unspecified,
        background = bg ?: Color.Unspecified,
        fontWeight = if (bold) FontWeight.Bold else null,
        fontStyle = if (italic) FontStyle.Italic else null,
    )
}

/** Collects visible text with the style in effect when each run was written. */
private class StyledOutput {
    private val builder = AnnotatedString.Builder()
    private val pending = StringBuilder()
    private var attributes = Attributes()

    fun append(c: Char) {
        pending.append(c)
    }

    fun applySgr(body: String) {
        val next = applySgrParams(attributes, body)
        if (next != attributes) {
            flush()
            attributes = next
        }
    }

    fun build(): AnnotatedString {
        flush()
        return builder.toAnnotatedString()
    }

    private fun flush() {
        if (pending.isEmpty()) return
        val chunk = pending.toString()
        val style = attributes.toSpanStyle()
        if (style == SpanStyle()) {
            builder.append(chunk)
        } else {
            builder.pushStyle(style)
            builder.append(chunk)
            builder.pop()
        }
        pending.clear()
    }
}

private fun applySgrParams(attributes: Attributes, body: String): Attributes {
    val params = if (body.isEmpty()) listOf("") else body.split(';')
    var result = attributes
    var i = 0
    while (i < params.size) {
        val param = params[i]
        val code = if (param.isEmpty()) 0 else (param.toIntOrNull() ?: -1)
        i++
        result = when (code) {
            0 -> Attributes()
            1 -> result.copy(bold = true)
            22 -> result.copy(bold = false)
            3 -> result.copy(italic = true)
            23 -> result.copy(italic = false)
            in 30..37 -> result.copy(fg = MochaTerminal.Ansi[code - 30])
            in 90..97 -> result.copy(fg = MochaTerminal.Ansi[code - 90 + 8])
            39 -> result.copy(fg = null)
            in 40..47 -> result.copy(bg = MochaTerminal.Ansi[code - 40])
            in 100..107 -> result.copy(bg = MochaTerminal.Ansi[code - 100 + 8])
            49 -> result.copy(bg = null)
            38, 48 -> {
                val (color, used) = extendedColor(params, i)
                i += used
                if (code == 38) result.copy(fg = color ?: result.fg) else result.copy(bg = color ?: result.bg)
            }
            else -> result
        }
    }
    return result
}

/** Reads the `5;n` or `2;r;g;b` arguments of a 38 or 48 code at [from]; returns the colour and the argument count. */
private fun extendedColor(params: List<String>, from: Int): Pair<Color?, Int> = when (params.getOrNull(from)) {
    "5" -> Pair(paletteColor(params.getOrNull(from + 1)), 2)
    "2" -> Pair(
        rgbColor(params.getOrNull(from + 1), params.getOrNull(from + 2), params.getOrNull(from + 3)),
        4,
    )
    else -> Pair(null, 0)
}

private fun paletteColor(index: String?): Color? {
    val n = index?.toIntOrNull()?.takeIf { it in 0..255 } ?: return null
    return when {
        n < 16 -> MochaTerminal.Ansi[n]
        n < 232 -> {
            val cube = n - 16
            Color(CUBE_LEVELS[cube / 36], CUBE_LEVELS[(cube / 6) % 6], CUBE_LEVELS[cube % 6])
        }
        else -> {
            val grey = 8 + 10 * (n - 232)
            Color(grey, grey, grey)
        }
    }
}

private fun rgbColor(red: String?, green: String?, blue: String?): Color? {
    val r = red?.toIntOrNull()?.takeIf { it in 0..255 } ?: return null
    val g = green?.toIntOrNull()?.takeIf { it in 0..255 } ?: return null
    val b = blue?.toIntOrNull()?.takeIf { it in 0..255 } ?: return null
    return Color(r, g, b)
}
