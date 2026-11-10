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
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.isSpecified
import androidx.compose.ui.unit.sp
import io.github.joshsymonds.orrery.ui.theme.Mocha

/**
 * Draws parsed markdown in the mono font and the Mocha palette. [color], [fontSize] and
 * [fontWeight] style body text; left unspecified they come from the surrounding text style. A
 * tapped link opens in the browser; one that no app can open is ignored.
 */
@Composable
fun MarkdownBody(
    blocks: List<MdBlock>,
    modifier: Modifier = Modifier,
    color: Color = Color.Unspecified,
    fontSize: TextUnit = TextUnit.Unspecified,
    fontWeight: FontWeight? = null,
) {
    val size = if (fontSize.isSpecified) fontSize else LocalTextStyle.current.fontSize
    val platform = LocalUriHandler.current
    val safe = remember(platform) { SafeUriHandler(platform) }
    CompositionLocalProvider(LocalUriHandler provides safe) {
        Blocks(blocks, modifier, BodyStyle(color, size, fontWeight))
    }
}

private class BodyStyle(val color: Color, val size: TextUnit, val weight: FontWeight?)

private class SafeUriHandler(private val inner: UriHandler) : UriHandler {
    override fun openUri(uri: String) {
        try {
            inner.openUri(uri)
        } catch (e: IllegalArgumentException) {
            // No app on the phone handles this link; the tap does nothing.
        }
    }
}

@Composable
private fun Blocks(blocks: List<MdBlock>, modifier: Modifier, style: BodyStyle) {
    Column(modifier = modifier.fillMaxWidth()) {
        blocks.forEachIndexed { index, block ->
            val gap = if (index == 0) 0.dp else gapBetween(blocks[index - 1], block)
            Leading(block.lead, style, Modifier.padding(top = gap)) { Block(block, style) }
        }
    }
}

private fun gapBetween(previous: MdBlock, block: MdBlock): Dp = when {
    block is MdBlock.Item && previous is MdBlock.Item -> 2.dp
    block.lead > 0 -> 4.dp
    previous is MdBlock.Heading -> 4.dp
    block is MdBlock.Heading -> 14.dp
    else -> 10.dp
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
private fun Block(block: MdBlock, style: BodyStyle) {
    when (block) {
        is MdBlock.Paragraph -> Text(
            text = block.text,
            color = style.color,
            fontSize = style.size,
            fontWeight = style.weight,
        )
        is MdBlock.Heading -> Text(
            text = block.text,
            color = Mocha.Lavender,
            fontSize = style.size * headingScale(block.level),
            fontWeight = FontWeight.Bold,
        )
        is MdBlock.Item -> Row(modifier = Modifier.fillMaxWidth()) {
            Text(
                text = block.marker + " ",
                color = Mocha.Overlay1,
                fontSize = style.size,
                fontWeight = style.weight,
                softWrap = false,
            )
            Text(
                text = block.text,
                modifier = Modifier.weight(1f),
                color = style.color,
                fontSize = style.size,
                fontWeight = style.weight,
            )
        }
        is MdBlock.Code -> CodeBlock(block, style)
        is MdBlock.Table -> TableBlock(block, style)
        is MdBlock.Quote -> Box(
            modifier = Modifier
                .fillMaxWidth()
                .drawBehind { drawRect(Mocha.Surface1, size = Size(3.dp.toPx(), size.height)) }
                .padding(start = 13.dp),
        ) {
            Blocks(block.blocks, Modifier, BodyStyle(Mocha.Subtext0, style.size, style.weight))
        }
        is MdBlock.Rule -> Box(
            modifier = Modifier
                .fillMaxWidth()
                .padding(vertical = 6.dp)
                .height(1.dp)
                .background(Mocha.Surface1),
        )
    }
}

private fun headingScale(level: Int): Float = when (level) {
    1 -> 1.18f
    2 -> 1.1f
    3 -> 1.04f
    else -> 1f
}

/** Unwrapped code in a crust box that pans sideways on its own; the language sits small in the corner. */
@Composable
private fun CodeBlock(block: MdBlock.Code, style: BodyStyle) {
    val size = style.size * 0.9f
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .background(Mocha.Crust),
    ) {
        Text(
            text = block.code,
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState())
                .padding(
                    start = 12.dp,
                    end = 12.dp,
                    top = if (block.language.isEmpty()) 10.dp else 20.dp,
                    bottom = 10.dp,
                ),
            color = Mocha.Text,
            fontSize = size,
            lineHeight = size * 1.45f,
            softWrap = false,
        )
        if (block.language.isNotEmpty()) {
            Text(
                text = block.language,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(top = 4.dp, end = 10.dp),
                color = Mocha.Overlay0,
                fontSize = 10.sp,
                maxLines = 1,
            )
        }
    }
}

/** An aligned grid, each column as wide as its widest cell, that pans sideways on its own when wide. */
@Composable
private fun TableBlock(table: MdBlock.Table, style: BodyStyle) {
    Box(modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState())) {
        TableGrid(table, style.size * 0.93f)
    }
}

@Composable
private fun TableGrid(table: MdBlock.Table, size: TextUnit) {
    val columns = table.alignments.size
    val rowCount = table.rows.size
    Layout(
        content = {
            table.rows.forEachIndexed { r, row ->
                row.forEach { cell ->
                    Text(
                        text = cell,
                        color = Mocha.Text,
                        fontSize = size,
                        lineHeight = size * 1.45f,
                        fontWeight = if (r == 0) FontWeight.Bold else null,
                        softWrap = false,
                    )
                }
            }
            repeat(rowCount - 1) { r ->
                Box(modifier = Modifier.background(if (r == 0) Mocha.Surface1 else Mocha.Surface0))
            }
        },
    ) { measurables, _ ->
        val cellCount = rowCount * columns
        val cells = measurables.take(cellCount).map { it.measure(Constraints()) }
        val gap = 16.dp.roundToPx()
        val padY = 4.dp.roundToPx()
        val line = 1.dp.roundToPx().coerceAtLeast(1)
        val widths = IntArray(columns) { c -> (0 until rowCount).maxOf { r -> cells[r * columns + c].width } }
        val heights = IntArray(rowCount) { r -> (0 until columns).maxOf { c -> cells[r * columns + c].height } }
        val totalWidth = widths.sum() + gap * (columns - 1)
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
