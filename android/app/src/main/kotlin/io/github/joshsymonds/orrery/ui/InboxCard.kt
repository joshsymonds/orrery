package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * A project's card: a head with the fold control, the folder, the name, the path with home as ~, and a host chip only
 * for another host. Folded, the head also says how many things wait on you (only when [waiting] is not zero) and how
 * many conversations the project holds, and nothing else is drawn. Open, its conversations hang under a line, then the
 * ended line, which shows the count of ended conversations and opens to them. Each conversation is drawn by [convo].
 * A tap on the folder, the name or the path calls [onOpenProject] with the project's key; the chevron only folds.
 */
@Composable
internal fun ProjectCard(
    project: Project,
    folded: Boolean,
    waiting: Int,
    endedOpen: Boolean,
    onFold: () -> Unit,
    onEnded: () -> Unit,
    convo: @Composable (Convo) -> Unit,
    modifier: Modifier = Modifier,
    onOpenProject: (String) -> Unit = {},
) {
    Column(
        modifier = modifier
            .testTag("project:${project.key}")
            .fillMaxWidth()
            .clip(RoundedCornerShape(OrreryTokens.Radius.three))
            .background(OrreryTokens.Surface.well),
    ) {
        ProjectHead(project, folded, waiting, onFold, onOpenProject)
        if (!folded) {
            ProjectConversations(project, endedOpen, onEnded, convo)
        }
    }
}

/**
 * A project's name as the inbox's project head draws it, in the place colour; a conversation's title row draws its
 * project in it too.
 */
val ProjectNameStyle: TextStyle = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold)

@Composable
private fun ProjectHead(
    project: Project,
    folded: Boolean,
    waiting: Int,
    onFold: () -> Unit,
    onOpenProject: (String) -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                start = OrreryTokens.Space.four,
                top = OrreryTokens.Space.four,
                end = OrreryTokens.Space.eight,
                bottom = OrreryTokens.Space.four,
            ),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        Box(
            modifier = Modifier
                .testTag("fold:${project.key}")
                .semantics { contentDescription = (if (folded) "Unfold " else "Fold ") + project.name }
                .width(OrreryTokens.Space.twentyFour)
                .height(OrreryTokens.Size.controlTouch)
                .clickable(role = Role.Button, onClick = onFold),
            contentAlignment = Alignment.Center,
        ) {
            Chevron(open = !folded, color = OrreryTokens.Ink.four)
        }
        Row(
            modifier = Modifier
                .weight(1f)
                .testTag("project-open:${project.key}")
                .clickable(role = Role.Button) { onOpenProject(project.key) },
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Folder(color = OrreryTokens.Role.place)
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    project.name,
                    color = OrreryTokens.Role.place,
                    style = ProjectNameStyle,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                if (project.path.isNotEmpty()) {
                    Text(
                        project.path,
                        color = OrreryTokens.Ink.three,
                        style = OrreryTokens.Type.small,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            if (project.host.isNotEmpty()) {
                Chip(project.host, ChipTone.Neutral)
            }
            if (folded && waiting > 0) {
                Text(
                    "$waiting waiting",
                    color = OrreryTokens.Role.attention,
                    style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                )
            }
            if (folded) {
                Text(
                    conversationCount(project).toString(),
                    color = OrreryTokens.Ink.three,
                    style = OrreryTokens.Type.small,
                )
            }
        }
    }
}

@Composable
private fun ProjectConversations(
    project: Project,
    endedOpen: Boolean,
    onEnded: () -> Unit,
    convo: @Composable (Convo) -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .height(IntrinsicSize.Min)
            .padding(
                start = OrreryTokens.Space.sixteen,
                end = OrreryTokens.Space.four,
                bottom = OrreryTokens.Space.eight,
            ),
    ) {
        Box(
            modifier = Modifier
                .width(OrreryTokens.Size.hairline)
                .fillMaxHeight()
                .background(OrreryTokens.Line.firm),
        )
        Column(modifier = Modifier.weight(1f).padding(start = OrreryTokens.Space.four)) {
            for (c in project.convos) {
                convo(c)
            }
            if (project.ended.isNotEmpty()) {
                EndedLine(project, endedOpen, onEnded)
                if (endedOpen) {
                    for (c in project.ended) {
                        convo(c)
                    }
                }
            }
        }
    }
}

@Composable
private fun EndedLine(project: Project, endedOpen: Boolean, onEnded: () -> Unit) {
    Row(
        modifier = Modifier
            .testTag("ended:${project.key}")
            .fillMaxWidth()
            .height(OrreryTokens.Size.controlTouch)
            .clickable(role = Role.Button, onClick = onEnded),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        Chevron(open = endedOpen, color = OrreryTokens.Ink.four)
        Text(
            "${project.ended.size} ended",
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.small,
        )
    }
}

/** A project's conversations as the card counts them: each row once, and no stand-in, which is not a conversation. */
private fun conversationCount(project: Project): Int =
    (project.convos + project.ended).filter { it.pane != null }.distinctBy { it.key }.size

/** The two chips under the head: what waits on you, and what is new. Each is drawn only when its count is not zero. */
@Composable
internal fun InboxChips(
    counts: Counts,
    narrow: Narrow,
    onNarrow: (Narrow) -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        if (counts.waiting != 0) {
            val label = if (counts.waiting == 1) "waits on you" else "wait on you"
            FilterChip(
                count = counts.waiting,
                label = label,
                tone = OrreryTokens.Role.attention,
                on = narrow == Narrow.WAITING,
                testTag = "chip-waiting",
                onPress = { onNarrow(if (narrow == Narrow.WAITING) Narrow.NONE else Narrow.WAITING) },
            )
        }
        if (counts.fresh != 0) {
            FilterChip(
                count = counts.fresh,
                label = "new",
                tone = OrreryTokens.Role.new,
                on = narrow == Narrow.NEW,
                testTag = "chip-new",
                onPress = { onNarrow(if (narrow == Narrow.NEW) Narrow.NONE else Narrow.NEW) },
            )
        }
    }
}

/** A round chip: its count in bold, then its label, in the tone's colour; pressed, it takes the on look. */
@Composable
private fun FilterChip(
    count: Int,
    label: String,
    tone: Color,
    on: Boolean,
    testTag: String,
    onPress: () -> Unit,
) {
    val shape = RoundedCornerShape(OrreryTokens.Radius.round)
    val text = buildAnnotatedString {
        withStyle(SpanStyle(color = OrreryTokens.Ink.one, fontWeight = OrreryTokens.Font.Weight.bold)) {
            append(count.toString())
        }
        append(" ")
        append(label)
    }
    Text(
        text = text,
        modifier = Modifier
            .testTag(testTag)
            .height(OrreryTokens.Size.controlTouch)
            .clip(shape)
            .border(OrreryTokens.Size.hairline, if (on) tone else OrreryTokens.Line.firm, shape)
            .then(if (on) Modifier.background(OrreryTokens.Surface.raised) else Modifier)
            .selectable(selected = on, role = Role.Button, onClick = onPress)
            .padding(horizontal = OrreryTokens.Space.twelve),
        color = tone,
        style = OrreryTokens.Type.small,
    )
}

/** The fold control's chevron: pointing down when open, right when folded or ended-closed. */
@Composable
private fun Chevron(open: Boolean, color: Color) {
    Canvas(Modifier.size(OrreryTokens.Size.icon)) {
        val w = size.width
        val h = size.height
        val path = Path().apply {
            if (open) {
                moveTo(w * 0.25f, h * 0.375f)
                lineTo(w * 0.5f, h * 0.625f)
                lineTo(w * 0.75f, h * 0.375f)
            } else {
                moveTo(w * 0.375f, h * 0.25f)
                lineTo(w * 0.625f, h * 0.5f)
                lineTo(w * 0.375f, h * 0.75f)
            }
        }
        drawPath(path, color, style = Stroke(width = OrreryTokens.Size.hairline.toPx()))
    }
}

/** The folder glyph a project's name is set against. */
@Composable
private fun Folder(color: Color) {
    Canvas(Modifier.size(OrreryTokens.Size.icon)) {
        val w = size.width
        val h = size.height
        val path = Path().apply {
            moveTo(w * 0.125f, h * 0.25f)
            lineTo(w * 0.4f, h * 0.25f)
            lineTo(w * 0.5f, h * 0.375f)
            lineTo(w * 0.875f, h * 0.375f)
            lineTo(w * 0.875f, h * 0.75f)
            lineTo(w * 0.125f, h * 0.75f)
            close()
        }
        drawPath(path, color, style = Stroke(width = OrreryTokens.Size.hairline.toPx()))
    }
}
