package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import org.junit.Assert.assertEquals
import org.junit.Test

class AnsiTextTest {
    private val esc = "\u001B"

    private val palette = listOf(
        0xFF45475A, 0xFFF38BA8, 0xFFA6E3A1, 0xFFF9E2AF, 0xFF89B4FA, 0xFFF5C2E7, 0xFF94E2D5, 0xFFBAC2DE,
        0xFF585B70, 0xFFF38BA8, 0xFFA6E3A1, 0xFFF9E2AF, 0xFF89B4FA, 0xFFF5C2E7, 0xFF94E2D5, 0xFFA6ADC8,
    )

    private val bold = SpanStyle(fontWeight = FontWeight.Bold)
    private val italic = SpanStyle(fontStyle = FontStyle.Italic)

    private fun fg(argb: Long) = SpanStyle(color = Color(argb))

    private fun bg(argb: Long) = SpanStyle(background = Color(argb))

    private fun span(style: SpanStyle, start: Int, end: Int) = AnnotatedString.Range(style, start, end)

    private fun assertParsed(input: String, text: String, vararg spans: AnnotatedString.Range<SpanStyle>) {
        val result = parseAnsi(input)
        assertEquals(text, result.text)
        assertEquals(spans.toList(), result.spanStyles)
    }

    @Test
    fun plainTextIsUnchanged() {
        assertParsed("hello world\nsecond line", "hello world\nsecond line")
    }

    @Test
    fun sixteenForegroundCodesMapToMochaPalette() {
        for (index in 0 until 16) {
            val code = if (index < 8) 30 + index else 90 + index - 8
            assertParsed("${esc}[${code}mX", "X", span(fg(palette[index]), 0, 1))
        }
    }

    @Test
    fun sixteenBackgroundCodesMapToMochaPalette() {
        for (index in 0 until 16) {
            val code = if (index < 8) 40 + index else 100 + index - 8
            assertParsed("${esc}[${code}mX", "X", span(bg(palette[index]), 0, 1))
        }
    }

    @Test
    fun paletteIndexColourUses256ColourForm() {
        assertParsed("${esc}[38;5;9mX", "X", span(fg(0xFFF38BA8), 0, 1))
        assertParsed("${esc}[48;5;12mX", "X", span(bg(0xFF89B4FA), 0, 1))
    }

    @Test
    fun cubeIndexColourUsesSixLevelCube() {
        assertParsed("${esc}[38;5;16mX", "X", span(fg(0xFF000000), 0, 1))
        assertParsed("${esc}[38;5;110mX", "X", span(fg(0xFF87AFD7), 0, 1))
        assertParsed("${esc}[38;5;231mX", "X", span(fg(0xFFFFFFFF), 0, 1))
        assertParsed("${esc}[48;5;110mX", "X", span(bg(0xFF87AFD7), 0, 1))
    }

    @Test
    fun greyIndexColourUsesGreyRamp() {
        assertParsed("${esc}[38;5;232mX", "X", span(fg(0xFF080808), 0, 1))
        assertParsed("${esc}[48;5;255mX", "X", span(bg(0xFFEEEEEE), 0, 1))
    }

    @Test
    fun twentyFourBitForegroundAndBackground() {
        assertParsed("${esc}[38;2;255;128;0mX", "X", span(fg(0xFFFF8000), 0, 1))
        assertParsed("${esc}[48;2;1;2;3mX", "X", span(bg(0xFF010203), 0, 1))
    }

    @Test
    fun boldAndItalicResetIndependently() {
        assertParsed(
            "${esc}[1mA${esc}[22mB${esc}[3mC${esc}[23mD",
            "ABCD",
            span(bold, 0, 1),
            span(italic, 2, 3),
        )
    }

    @Test
    fun combinedAttributesShareOneSpan() {
        assertParsed(
            "${esc}[1;3;31mX",
            "X",
            span(SpanStyle(color = Color(0xFFF38BA8), fontWeight = FontWeight.Bold, fontStyle = FontStyle.Italic), 0, 1),
        )
    }

    @Test
    fun zeroResetsEverythingAndEmptyParameterResets() {
        assertParsed("${esc}[1;31mA${esc}[0mB", "AB", span(SpanStyle(color = Color(0xFFF38BA8), fontWeight = FontWeight.Bold), 0, 1))
        assertParsed("${esc}[31mA${esc}[mB", "AB", span(fg(0xFFF38BA8), 0, 1))
    }

    @Test
    fun defaultForegroundAndBackgroundCodesClearColour() {
        assertParsed(
            "${esc}[31mA${esc}[39mB${esc}[41mC${esc}[49mD",
            "ABCD",
            span(fg(0xFFF38BA8), 0, 1),
            span(bg(0xFFF38BA8), 2, 3),
        )
    }

    @Test
    fun unsupportedSgrParametersCarryNoStyle() {
        assertParsed("${esc}[4mA${esc}[7mB", "AB")
        assertParsed("${esc}[4:3mA", "A")
    }

    @Test
    fun colonSubParameterIsSkippedAndSequenceContinues() {
        assertParsed("${esc}[4:3;31mB", "B", span(fg(0xFFF38BA8), 0, 1))
    }

    @Test
    fun nonSgrControlSequencesAreRemovedAndTextAroundThemKept() {
        assertParsed("a${esc}[?25hb${esc}[Kc", "abc")
        assertParsed("${esc}[31mA${esc}[?25hB", "AB", span(fg(0xFFF38BA8), 0, 2))
    }

    @Test
    fun oscSequencesAreRemovedWhetherBelOrStTerminated() {
        assertParsed("a${esc}]0;window title\u0007b", "ab")
        assertParsed("a${esc}]0;window title${esc}\\b", "ab")
    }

    @Test
    fun otherEscapesAreRemoved() {
        assertParsed("a${esc}(Bb${esc}7c", "abc")
    }

    @Test
    fun truncatedSequenceAtEndOfInputIsDropped() {
        assertParsed("abc${esc}[31", "abc")
        assertParsed("abc$esc", "abc")
        assertParsed("abc${esc}]0;tit", "abc")
        assertParsed("${esc}[31mA${esc}[38;5", "A", span(fg(0xFFF38BA8), 0, 1))
    }

    @Test
    fun sequenceBrokenByControlCharacterKeepsFollowingText() {
        assertParsed("a${esc}[31\nb", "a\nb")
    }

    @Test
    fun malformedNumbersDoNotThrowAndCarryNoStyle() {
        assertParsed("${esc}[38;5;?mA", "A")
        assertParsed("${esc}[38;5;zzmA", "zmA")
        assertParsed("${esc}[38;2;300;0;0mA", "A")
        assertParsed("${esc}[38;5mA", "A")
        assertParsed("${esc}[99999999999mA", "A")
        assertParsed("${esc}[1.5mA", "A")
    }
}
