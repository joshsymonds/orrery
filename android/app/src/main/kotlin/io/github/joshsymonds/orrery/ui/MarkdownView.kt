package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextIndent
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.isSpecified
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * Draws parsed markdown in the mono font and the token palette. [color], [fontSize] and
 * [fontWeight] style body text; left unspecified they come from the body type token. A
 * tapped link opens in the browser; one that no app can open is ignored.
 */
@Composable
fun MarkdownBody(
    blocks: List<MdBlock>,
    modifier: Modifier = Modifier,
    color: Color = Color.Unspecified,
    fontSize: TextUnit = TextUnit.Unspecified,
    fontWeight: FontWeight? = null,
    firstIndent: TextUnit = TextUnit.Unspecified,
) {
    val size = if (fontSize.isSpecified) fontSize else OrreryTokens.Type.body.fontSize
    Blocks(blocks, modifier, BodyStyle(color, size, fontWeight), firstIndent)
}

private class BodyStyle(val color: Color, val size: TextUnit, val weight: FontWeight?)

// firstIndent, when specified, indents the first line of the first block when that block is a paragraph, so what the
// caller draws at its start leads the paragraph's first line.
@Composable
private fun Blocks(blocks: List<MdBlock>, modifier: Modifier, style: BodyStyle, firstIndent: TextUnit = TextUnit.Unspecified) {
    Column(modifier = modifier.fillMaxWidth()) {
        blocks.forEachIndexed { index, block ->
            val gap = if (index == 0) 0.dp else gapBetween(blocks[index - 1], block)
            val indent = if (index == 0) firstIndent else TextUnit.Unspecified
            Leading(block.lead, style, Modifier.padding(top = gap)) { Block(block, style, indent) }
        }
    }
}

private fun gapBetween(previous: MdBlock, block: MdBlock): Dp = when {
    block is MdBlock.Item && previous is MdBlock.Item -> OrreryTokens.Space.two
    block.lead > 0 -> OrreryTokens.Space.four
    previous is MdBlock.Heading -> OrreryTokens.Space.four
    block is MdBlock.Heading -> OrreryTokens.Space.twelve + OrreryTokens.Space.two
    else -> OrreryTokens.Space.eight + OrreryTokens.Space.two
}

/** Puts [lead] monospace cells of space before [content], so it lines up under a list item's text. */
@Composable
private fun Leading(lead: Int, style: BodyStyle, modifier: Modifier, content: @Composable () -> Unit) {
    if (lead == 0) {
        Box(modifier = modifier.fillMaxWidth()) { content() }
        return
    }
    Row(modifier = modifier.fillMaxWidth()) {
        Text(text = " ".repeat(lead), fontSize = style.size, softWrap = false)
        Box(modifier = Modifier.weight(1f)) { content() }
    }
}

@Composable
private fun Block(block: MdBlock, style: BodyStyle, firstIndent: TextUnit) {
    when (block) {
        is MdBlock.Paragraph -> FindText(
            text = block.text,
            color = style.color,
            fontSize = style.size,
            fontWeight = style.weight,
            style = if (firstIndent.isSpecified) {
                LocalTextStyle.current.copy(textIndent = TextIndent(firstLine = firstIndent))
            } else {
                LocalTextStyle.current
            },
        )
        is MdBlock.Heading -> FindText(
            text = block.text,
            color = OrreryTokens.Prose.heading,
            style = headingStyle(block.level),
        )
        is MdBlock.Item -> Row(modifier = Modifier.fillMaxWidth()) {
            Text(
                text = block.marker + " ",
                color = OrreryTokens.Prose.marker,
                fontSize = style.size,
                fontWeight = style.weight,
                softWrap = false,
            )
            FindText(
                text = block.text,
                modifier = Modifier.weight(1f),
                color = style.color,
                fontSize = style.size,
                fontWeight = style.weight,
            )
        }
        is MdBlock.Code -> CodeBlock(block)
        is MdBlock.Table -> TableBlock(block)
        is MdBlock.Quote -> Box(
            modifier = Modifier
                .fillMaxWidth()
                .drawBehind {
                    drawRect(OrreryTokens.Prose.marker, size = Size(OrreryTokens.Size.rail.toPx(), size.height))
                }
                .padding(start = OrreryTokens.Space.twelve),
        ) {
            Blocks(block.blocks, Modifier, BodyStyle(OrreryTokens.Ink.two, style.size, style.weight))
        }
        is MdBlock.Rule -> Box(
            modifier = Modifier
                .fillMaxWidth()
                .padding(vertical = OrreryTokens.Space.four + OrreryTokens.Space.two)
                .height(OrreryTokens.Size.hairline)
                .background(OrreryTokens.Line.firm),
        )
    }
}

// A level one or two heading is the page's title; a deeper one is body text in bold.
private fun headingStyle(level: Int): TextStyle =
    if (level <= 2) OrreryTokens.Type.title else OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold)

/** Unwrapped code in a surface box that pans sideways on its own; the language sits small in the corner. */
@Composable
private fun CodeBlock(block: MdBlock.Code) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
            .background(OrreryTokens.Surface.side),
    ) {
        FindText(
            text = block.drawn,
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState())
                .padding(
                    start = OrreryTokens.Space.twelve,
                    end = OrreryTokens.Space.twelve,
                    top = if (block.language.isEmpty()) {
                        OrreryTokens.Space.eight + OrreryTokens.Space.two
                    } else {
                        OrreryTokens.Space.sixteen + OrreryTokens.Space.four
                    },
                    bottom = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                ),
            color = OrreryTokens.Ink.one,
            style = OrreryTokens.Type.code,
            softWrap = false,
        )
        if (block.language.isNotEmpty()) {
            Text(
                text = block.language,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(top = OrreryTokens.Space.four, end = OrreryTokens.Space.eight + OrreryTokens.Space.two),
                color = OrreryTokens.Ink.four,
                style = OrreryTokens.Type.small,
                maxLines = 1,
            )
        }
    }
}

/** An aligned grid, each column as wide as its widest cell, that pans sideways on its own when wide. */
@Composable
private fun TableBlock(table: MdBlock.Table) {
    Box(modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState())) {
        TableGrid(table, OrreryTokens.Type.body)
    }
}

@Composable
private fun TableGrid(table: MdBlock.Table, style: TextStyle) {
    val columns = table.alignments.size
    val rowCount = table.rows.size
    Layout(
        content = {
            table.rows.forEachIndexed { r, row ->
                row.forEach { cell ->
                    FindText(
                        text = cell,
                        color = OrreryTokens.Ink.one,
                        style = style,
                        fontWeight = if (r == 0) OrreryTokens.Font.Weight.bold else null,
                        softWrap = false,
                    )
                }
            }
            repeat(rowCount - 1) { r ->
                Box(modifier = Modifier.background(if (r == 0) OrreryTokens.Line.firm else OrreryTokens.Line.soft))
            }
        },
    ) { measurables, _ ->
        val cellCount = rowCount * columns
        val cells = measurables.take(cellCount).map { it.measure(Constraints()) }
        val gap = OrreryTokens.Space.sixteen.roundToPx()
        val padY = OrreryTokens.Space.four.roundToPx()
        val line = OrreryTokens.Size.hairline.roundToPx().coerceAtLeast(1)
        val widths = IntArray(columns) { c -> (0 until rowCount).maxOf { r -> cells[r * columns + c].width } }
        val heights = IntArray(rowCount) { r -> (0 until columns).maxOf { c -> cells[r * columns + c].height } }
        // Constraints cannot represent a width above 262142 px, so a wider table is clamped to it.
        val maxWidth = 262142L
        val naturalWidth = widths.sumOf { it.toLong() } + gap.toLong() * (columns - 1)
        val totalWidth = minOf(naturalWidth, maxWidth).toInt()
        val totalHeight = heights.sum() + padY * 2 * rowCount + line * (rowCount - 1)
        val lines = measurables.drop(cellCount).map { it.measure(Constraints.fixed(totalWidth, line)) }
        layout(totalWidth, totalHeight) {
            var y = 0
            for (r in 0 until rowCount) {
                var x = 0
                for (c in 0 until columns) {
                    val cell = cells[r * columns + c]
                    val offset = when (table.alignments[c]) {
                        ColumnAlign.Start -> 0
                        ColumnAlign.Center -> (widths[c] - cell.width) / 2
                        ColumnAlign.End -> widths[c] - cell.width
                    }
                    cell.place(x + offset, y + padY)
                    x += widths[c] + gap
                }
                y += heights[r] + padY * 2
                if (r < rowCount - 1) {
                    lines[r].place(0, y)
                    y += line
                }
            }
        }
    }
}
