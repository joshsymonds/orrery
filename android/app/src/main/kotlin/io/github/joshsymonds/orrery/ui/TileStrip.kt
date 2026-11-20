package io.github.joshsymonds.orrery.ui

import android.util.Log
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
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.BackgroundTask
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

private const val TAG = "ConversationStrip"
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

/** The sheet a strip holds open: a list's rows, or a dots group's picker. */
private sealed interface OpenSheet {
    data class Tasks(val thing: ListThing) : OpenSheet

    data class Picker(val group: RailGroup) : OpenSheet
}

/** The epics a conversation steers, with the detail of each one that is held. */
private class SteeredEpics(val epics: List<EpicRow>, val details: Map<String, EpicDetail>)

/**
 * The epics list, then the detail of each epic [conversation] steers that is not archived. Null when a call fails,
 * so the strip keeps what it held.
 */
private suspend fun loadSteeredEpics(client: OrreryClient, conversation: String): SteeredEpics? =
    withContext(Dispatchers.IO) {
        try {
            val epics = client.epics()
            val steered = if (conversation.isEmpty()) {
                emptyList()
            } else {
                epics.filter { it.steering == conversation && !it.archived }
            }
            val details = steered.mapNotNull { epic -> client.epic(epic.id)?.let { epic.id to it } }.toMap()
            SteeredEpics(epics, details)
        } catch (e: IOException) {
            Log.w(TAG, "could not load the epics $conversation steers", e)
            null
        }
    }

/**
 * A conversation's right rail as a strip under its header: the epics it steers and, unless [pane], its running
 * background [background] tasks. The epics load off the main thread for [conversation]. An epic tile, or a row of a
 * picker, calls [onOpenEpic] with the epic's id; a list tile opens its sheet, and a dots tile its picker. [now] is
 * unix seconds, the clock the sheets count from.
 */
@Composable
fun ConversationStrip(
    conversation: String,
    pane: Boolean,
    background: List<BackgroundTask>,
    now: Long,
    client: OrreryClient,
    onOpenEpic: (String) -> Unit,
    modifier: Modifier = Modifier,
    onTiles: (Boolean) -> Unit = {},
    end: @Composable () -> Unit = {},
) {
    var steered by remember(conversation) { mutableStateOf(SteeredEpics(emptyList(), emptyMap())) }
    var sheet by remember(conversation) { mutableStateOf<OpenSheet?>(null) }
    LaunchedEffect(conversation) {
        loadSteeredEpics(client, conversation)?.let { steered = it }
    }
    val groups = workGroupsOf(conversation, pane, steered.epics, steered.details, background)
    val tiles = groups.isNotEmpty()
    LaunchedEffect(tiles) { onTiles(tiles) }
    if (tiles) {
        TileStrip(
            groups = groups,
            onOpen = { thing ->
                when (thing) {
                    is EpicThing -> onOpenEpic(thing.id)
                    is ListThing -> sheet = OpenSheet.Tasks(thing)
                }
            },
            onOpenPicker = { sheet = OpenSheet.Picker(it) },
            modifier = modifier,
            end = end,
        )
    }
    when (val open = sheet) {
        is OpenSheet.Tasks -> WorkListSheet(
            agents = open.thing.kind == GroupKind.AGENTS,
            tasks = open.thing.tasks,
            now = now,
            onDismiss = { sheet = null },
        )
        is OpenSheet.Picker -> RailPickerSheet(
            label = open.group.label,
            rows = open.group.things.filterIsInstance<EpicThing>().map { PickerRow(it.id, it.slug, it.state, it.asks) },
            onOpen = onOpenEpic,
            onDismiss = { sheet = null },
        )
        null -> Unit
    }
}
