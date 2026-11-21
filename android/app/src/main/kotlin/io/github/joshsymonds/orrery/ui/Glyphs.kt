package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Fill
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.withTransform
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The grid every glyph is drawn on, and the stroke it is drawn with, in the grid's units. */
private const val GLYPH_GRID = 16f
private const val GLYPH_STROKE = 1.5f

/**
 * Round 6's icons, the ones this client draws: each its path data on the 16 grid, as web/src/glyphs.tsx and the round-6
 * wireframe's icons.js hold them. A [filled] path is filled, the rest are stroked.
 */
enum class Glyph(val path: String, val filled: Boolean = false) {
    SEARCH("M7 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM10 10l3.5 3.5"),
    BELL("M4 11V7.5a4 4 0 0 1 8 0V11l1 1.5H3zM6.5 13.5a1.5 1.5 0 0 0 3 0"),
    POOLS("M8 2l6 3-6 3-6-3zM2 8l6 3 6-3M2 11l6 3 6-3"),
    GEAR(
        "M8 5.8a2.2 2.2 0 1 0 0 4.4 2.2 2.2 0 0 0 0-4.4zM8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4" +
            "M11.2 11.2l1.4 1.4M3.4 12.6l1.4-1.4M11.2 4.8l1.4-1.4",
    ),
    PLUS("M8 3v10M3 8h10"),
    MORE(
        "M3.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM8 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2z" +
            "M12.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2z",
        filled = true,
    ),
}

/** [glyph] at the icon size, in [color]. Decorative: whatever holds it names it. */
@Composable
fun GlyphIcon(glyph: Glyph, color: Color, modifier: Modifier = Modifier) {
    val path = remember(glyph) { PathParser().parsePathString(glyph.path).toPath() }
    Canvas(modifier = modifier.size(OrreryTokens.Size.icon)) {
        withTransform({ scale(size.width / GLYPH_GRID, size.height / GLYPH_GRID, pivot = Offset.Zero) }) {
            val style = if (glyph.filled) Fill else Stroke(GLYPH_STROKE, cap = StrokeCap.Round, join = StrokeJoin.Round)
            drawPath(path, color, style = style)
        }
    }
}

/**
 * Round 6's IconButton on touch: a quiet square a touch control wide, its [glyph] in the second ink, named [description]
 * for the reader since it shows no words. A [badge] draws over the glyph inside the button, so the reader hears it with
 * the name.
 */
@Composable
fun IconButton(
    glyph: Glyph,
    description: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    badge: @Composable BoxScope.() -> Unit = {},
) {
    Box(
        modifier = modifier
            .size(OrreryTokens.Size.controlTouch)
            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
            .clickable(role = Role.Button, onClick = onClick)
            .semantics { contentDescription = description },
        contentAlignment = Alignment.Center,
    ) {
        GlyphIcon(glyph, OrreryTokens.Ink.two)
        badge()
    }
}

/** Round 6's count on a control's corner: [count] on a round pill in the new role, at least a glyph square. */
@Composable
fun CountBadge(count: Int, modifier: Modifier = Modifier) {
    Text(
        text = count.toString(),
        modifier = modifier
            .defaultMinSize(minWidth = OrreryTokens.Space.sixteen, minHeight = OrreryTokens.Space.sixteen)
            .background(OrreryTokens.Role.new, RoundedCornerShape(OrreryTokens.Radius.round))
            .padding(horizontal = OrreryTokens.Space.four),
        color = OrreryTokens.Ink.onAccent,
        style = OrreryTokens.Type.label,
        textAlign = TextAlign.Center,
        maxLines = 1,
    )
}
