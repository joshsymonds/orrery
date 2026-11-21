package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.layout.SubcomposeLayout
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.Constraints
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** Search's name: its button's content description, its menu line, and the search field's placeholder. */
internal const val SEARCH_LABEL = "Search sessions and epics"

/** One action of the head that can fold into More: its [name], its [glyph], its [tag], and what a tap does. */
private class InboxAction(val name: String, val glyph: Glyph, val tag: String, val onClick: () -> Unit)

/**
 * How much of the head folds into More, least first. Each step says how many of Search, Pools and Settings it folds,
 * counted from Settings back, how many icon buttons the bar then draws (the bell and More among them) and whether New
 * keeps its word. Folding Settings alone would trade it for
 * More at the same width, so Pools and Settings fold together.
 */
private enum class InboxFold(val folds: Int, val icons: Int, val newWords: Boolean) {
    NONE(folds = 0, icons = 4, newWords = true),
    POOLS_SETTINGS(folds = 2, icons = 3, newWords = true),
    SEARCH_TOO(folds = 3, icons = 2, newWords = true),
    NEW_BARE(folds = 3, icons = 2, newWords = false),
}

private enum class InboxSlot { LEAD, END }

/** New's word, the type it is set in, its padding either side and the gap between the plus and the word. */
private const val NEW_WORD = "New"
private val NEW_STYLE = OrreryTokens.Type.small
private val NEW_PAD = OrreryTokens.Space.sixteen
private val NEW_GAP = OrreryTokens.Space.eight

/**
 * The inbox head, after round 6's phone head: the Orrery mark and the name, then Search, the Notices bell with its
 * [unread] count, Pools, Settings and New, the last a button with its word, the rest icon buttons named for the reader.
 * The bar is one line at any width: when the actions do not fit, Pools and Settings fold into More, then Search, and at
 * the last New gives up its word. The bell always stays in the bar.
 */
@Composable
internal fun InboxHead(
    unread: Int,
    onSearch: () -> Unit,
    onNotices: () -> Unit,
    onPools: () -> Unit,
    onSettings: () -> Unit,
    onNew: () -> Unit,
    modifier: Modifier = Modifier,
) {
    // Least folded last: Search folds after Pools and Settings, so it comes first in the list of what folds.
    val foldable = listOf(
        InboxAction(SEARCH_LABEL, Glyph.SEARCH, "open-search", onSearch),
        InboxAction("Pools", Glyph.POOLS, "open-pools", onPools),
        InboxAction("Settings", Glyph.GEAR, "open-settings", onSettings),
    )
    val measurer = rememberTextMeasurer()
    SubcomposeLayout(
        modifier = modifier
            .fillMaxWidth()
            .testTag("inbox-head")
            .padding(
                start = OrreryTokens.Space.sixteen,
                end = OrreryTokens.Space.eight,
                top = OrreryTokens.Space.twelve,
                bottom = OrreryTokens.Space.eight,
            ),
    ) { constraints ->
        val loose = Constraints(maxWidth = constraints.maxWidth)
        val lead = subcompose(InboxSlot.LEAD) { InboxLead() }.single().measure(loose)
        val icon = OrreryTokens.Size.controlTouch.roundToPx()
        // New with its word: its padding, the plus, the gap and the word as the text measures it.
        val word = measurer.measure(NEW_WORD, NEW_STYLE, maxLines = 1, softWrap = false, density = this).size.width
        val newWords = (NEW_PAD * 2 + OrreryTokens.Size.icon + NEW_GAP).roundToPx() + word
        val gap = OrreryTokens.Space.four.roundToPx()
        val room = constraints.maxWidth - lead.width - OrreryTokens.Space.eight.roundToPx()
        val fold = InboxFold.entries.firstOrNull { step ->
            val new = if (step.newWords) newWords else icon
            step.icons * (icon + gap) + new <= room
        } ?: InboxFold.NEW_BARE
        val shown = foldable.dropLast(fold.folds)
        val folded = foldable.takeLast(fold.folds)
        val end = subcompose(InboxSlot.END) {
            InboxEnd(
                shown = shown,
                folded = folded,
                unread = unread,
                onNotices = onNotices,
                newWords = fold.newWords,
                onNew = onNew,
            )
        }.single().measure(Constraints(maxWidth = room.coerceAtLeast(0)))
        val height = maxOf(lead.height, end.height)
        layout(constraints.maxWidth, height) {
            lead.place(0, (height - lead.height) / 2)
            end.place(constraints.maxWidth - end.width, (height - end.height) / 2)
        }
    }
}

/** The mark and the name, in bold body type, the mark as high as the name's line. */
@Composable
private fun InboxLead() {
    Row(modifier = Modifier.height(IntrinsicSize.Min), verticalAlignment = Alignment.CenterVertically) {
        TitleMark(modifier = Modifier.fillMaxHeight())
        Spacer(modifier = Modifier.width(OrreryTokens.Space.eight))
        Text(
            "orrery",
            color = OrreryTokens.Ink.one,
            style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
            softWrap = false,
        )
    }
}

/**
 * The actions drawn in the bar: the [shown] ones that come before the bell, the bell, the ones after it, More holding
 * the [folded] ones when there are any, and New.
 */
@Composable
private fun InboxEnd(
    shown: List<InboxAction>,
    folded: List<InboxAction>,
    unread: Int,
    onNotices: () -> Unit,
    newWords: Boolean,
    onNew: () -> Unit,
) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        // Round 6's order: Search, the bell, Pools, Settings.
        val search = shown.filter { it.glyph == Glyph.SEARCH }
        search.forEach { InboxButton(it) }
        IconButton(Glyph.BELL, "Notices", onNotices, Modifier.testTag("open-notices")) {
            if (unread > 0) CountBadge(unread, Modifier.align(Alignment.TopEnd).testTag("notices-unread"))
        }
        (shown - search.toSet()).forEach { InboxButton(it) }
        if (folded.isNotEmpty()) InboxMore(folded)
        NewButton(words = newWords, onClick = onNew)
    }
}

@Composable
private fun InboxButton(action: InboxAction) {
    IconButton(action.glyph, action.name, action.onClick, Modifier.testTag(action.tag))
}

/** More: the folded [actions] in a menu, each its glyph and its name on one line, tagged as its button would be. */
@Composable
private fun InboxMore(actions: List<InboxAction>) {
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton(Glyph.MORE, "More", { open = true }, Modifier.testTag("inbox-more"))
        DropdownMenu(expanded = open, onDismissRequest = { open = false }, modifier = Modifier.testTag("inbox-more-menu")) {
            actions.forEach { action ->
                DropdownMenuItem(
                    text = { Text(action.name, maxLines = 1) },
                    leadingIcon = { GlyphIcon(action.glyph, OrreryTokens.Ink.two) },
                    onClick = {
                        open = false
                        action.onClick()
                    },
                    modifier = Modifier.testTag(action.tag),
                )
            }
        }
    }
}

/**
 * New, as round 6's button: raised, a touch control high, the plus and the word. Without its [words] it is a square of
 * the plus alone, named New for the reader.
 */
@Composable
private fun NewButton(words: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .testTag("picker-new")
            .height(OrreryTokens.Size.controlTouch)
            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
            .background(OrreryTokens.Surface.raised)
            .clickable(role = Role.Button, onClick = onClick)
            .then(
                if (words) {
                    Modifier.padding(horizontal = NEW_PAD)
                } else {
                    Modifier.width(OrreryTokens.Size.controlTouch).semantics { contentDescription = NEW_WORD }
                },
            ),
        horizontalArrangement = Arrangement.spacedBy(NEW_GAP, Alignment.CenterHorizontally),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        GlyphIcon(Glyph.PLUS, OrreryTokens.Ink.one)
        if (words) Text(NEW_WORD, color = OrreryTokens.Ink.one, style = NEW_STYLE, maxLines = 1, softWrap = false)
    }
}

/**
 * The app's colour icon as a disc: the launcher's background and foreground layers, clipped to a
 * circle. The 72-unit centre of the 108 canvas fills the circle, so each layer is scaled by 1.5
 * about the disc's centre. Decorative, so it has no content description.
 */
@Composable
private fun TitleMark(modifier: Modifier) {
    Box(modifier = modifier.aspectRatio(1f).clip(CircleShape).testTag("title-mark")) {
        Image(
            painter = painterResource(R.drawable.ic_launcher_background),
            contentDescription = null,
            modifier = Modifier.matchParentSize().scale(1.5f),
        )
        Image(
            painter = painterResource(R.drawable.ic_launcher_foreground),
            contentDescription = null,
            modifier = Modifier.matchParentSize().scale(1.5f),
        )
    }
}
