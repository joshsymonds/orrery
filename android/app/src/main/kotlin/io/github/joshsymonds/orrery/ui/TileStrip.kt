package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

private val TILE_SHAPE = RoundedCornerShape(OrreryTokens.Radius.two)

/**
 * The right rail's groups on a phone, as one horizontally scrolling strip. Each group is a column: its name over its
 * tiles. An epic is a tile with its small orrery when its detail is held and its short name otherwise, with a ? when
 * it asks. A list is a tile with its count. A group of more than three is one dots tile, its count or ? when one asks,
 * and opens [onOpenPicker]. A tile opens its thing through [onOpen]. [end] is drawn last, after the groups.
 */
@Composable
fun TileStrip(
    groups: List<RailGroup>,
    onOpen: (RailThing) -> Unit,
    onOpenPicker: (RailGroup) -> Unit,
    modifier: Modifier = Modifier,
    end: @Composable () -> Unit = {},
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .testTag("tile-strip")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.sixteen),
        verticalAlignment = Alignment.Top,
    ) {
        for (group in groups) {
            GroupColumn(group, onOpen, onOpenPicker)
        }
        Box(Modifier.testTag("strip-end")) { end() }
    }
}

@Composable
private fun GroupColumn(group: RailGroup, onOpen: (RailThing) -> Unit, onOpenPicker: (RailGroup) -> Unit) {
    val kind = kindTag(group.kind)
    Column(
        modifier = Modifier.testTag("rail-group:$kind"),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        Text(
            group.label.inLabelCase(),
            style = OrreryTokens.Type.label,
            color = OrreryTokens.Ink.two,
            modifier = Modifier.testTag("rail-label:$kind"),
        )
        if (group.many) {
            DotsTile(group, onOpenPicker)
        } else {
            Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
                for (thing in group.things) {
                    when (thing) {
                        is EpicThing -> EpicTile(thing, onOpen)
                        is ListThing -> ListTile(thing, onOpen)
                    }
                }
            }
        }
    }
}

@Composable
private fun EpicTile(epic: EpicThing, onOpen: (RailThing) -> Unit) {
    Box {
        Tile("tile:${epic.key}", epic.tone, onClick = { onOpen(epic) }) {
            val detail = epic.detail
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                if (detail != null) {
                    val wedges = remember(detail) { wedgesOf(detail) }
                    val arcs = remember(detail) { requirementArcs(wedges, detail.requirements) }
                    MiniOrrery(wedges, arcs, Modifier.size(OrreryTokens.Size.controlDesk).testTag("mini-orrery:${epic.key}"))
                }
                Text(
                    epic.short,
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Ink.one,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (epic.asks) {
            Text(
                "?",
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Role.attention,
                modifier = Modifier.align(Alignment.TopEnd).testTag("tile-ask:${epic.key}"),
            )
        }
    }
}

@Composable
private fun ListTile(list: ListThing, onOpen: (RailThing) -> Unit) {
    Tile("tile:${list.key}", list.tone, onClick = { onOpen(list) }) {
        Text(list.count.toString(), style = OrreryTokens.Type.body, color = OrreryTokens.Ink.one)
    }
}

@Composable
private fun DotsTile(group: RailGroup, onOpenPicker: (RailGroup) -> Unit) {
    val asks = group.things.any { it is EpicThing && it.asks }
    val tone = if (asks) ThingTone.ATTENTION else ThingTone.QUIET
    Tile("tile-dots:${kindTag(group.kind)}", tone, onClick = { onOpenPicker(group) }) {
        Text(
            if (asks) "?" else group.things.size.toString(),
            style = OrreryTokens.Type.body,
            color = OrreryTokens.Ink.one,
        )
    }
}

@Composable
private fun Tile(tag: String, tone: ThingTone, onClick: () -> Unit, content: @Composable BoxScope.() -> Unit) {
    Box(
        modifier = Modifier
            .size(OrreryTokens.Layout.railWidth)
            .testTag(tag)
            .clickable(onClick = onClick)
            .background(OrreryTokens.Surface.raised, TILE_SHAPE)
            .border(OrreryTokens.Size.hairline, lineOf(tone), TILE_SHAPE)
            .padding(OrreryTokens.Space.four),
        contentAlignment = Alignment.Center,
        content = content,
    )
}

private fun lineOf(tone: ThingTone): Color = when (tone) {
    ThingTone.ATTENTION -> OrreryTokens.Role.attention
    ThingTone.LIVE -> OrreryTokens.Role.live
    ThingTone.QUIET -> OrreryTokens.Line.soft
}

private fun kindTag(kind: GroupKind): String = kind.name.lowercase()
