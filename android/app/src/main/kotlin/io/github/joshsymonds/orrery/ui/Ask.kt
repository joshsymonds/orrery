package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.IconButton
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.io.IOException
import kotlinx.coroutines.launch

/** Where a session's or pane's answer stands: open, sent and waiting for the stream, or refused. */
internal enum class AskPhase { OPEN, SENDING, ERROR }

private const val ANSWER_FAILED = "Not answered. Your answer is kept."
private const val UNANSWERABLE = "This question cannot be answered from here. Reply in words below."
private const val SOMETHING_ELSE = "Something else"
private const val SOMETHING_ELSE_DOES = "Type an answer for the planner."
private const val OTHER = "other"
private const val RECORD_LABEL = "Will be recorded"

/**
 * An Ask as its owner sees it: its head with the controls that answer, put off or decline it, its question and options
 * (drawn with their markup when the model's question is markdown), the foot that sends an epic's answer, and the notes.
 * A session's or pane's Ask holds no answer of its own: [picked]
 * is the option in flight or refused, [note] its refusal, and a tap calls [onAnswer] with the option's number. An epic's
 * or park's pick and text live here: a tap picks, and [onConfirm] is called with the choice and the text only on
 * confirm, showing the words it returns. A control whose callback is null is not drawn.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun Ask(
    model: AskModel,
    now: Long,
    phase: AskPhase,
    picked: Int?,
    note: String,
    onAnswer: ((Int) -> Unit)?,
    onNotNow: (() -> Unit)?,
    onBringBack: (() -> Unit)?,
    onDecline: (() -> Unit)?,
    onArchive: (() -> Unit)?,
    onConfirm: (suspend (choice: String, text: String) -> String)?,
    modifier: Modifier = Modifier,
) {
    val epic = model.kind == AskKind.EPIC || model.kind == AskKind.PARK
    val hasOther = model.kind == AskKind.EPIC
    val otherIndex = model.options.size
    val scope = rememberCoroutineScope()
    var epicPick by remember(model) { mutableStateOf(initialPick(model)) }
    var words by remember(model) { mutableStateOf("") }
    var answering by remember(model) { mutableStateOf(false) }
    var failure by remember(model) { mutableStateOf("") }
    var moreOpen by remember(model) { mutableStateOf(false) }

    val sending = if (epic) answering else phase == AskPhase.SENDING
    val pick = epicPick
    val isOther = hasOther && pick == otherIndex
    val pickedName = when {
        pick == null -> ""
        isOther -> SOMETHING_ELSE
        else -> model.options[pick].name
    }
    val recordText = when {
        pick == null -> ""
        isOther -> words.trim()
        else -> model.options[pick].does
    }
    val stale = if (model.putOff) null else staleWords(model.asked, now)
    val shownNote = if (epic) failure else note

    // confirm sends an epic's pick: its choice's id with what it does, or "other" with the trimmed text. Text typed
    // while the answer is in flight stays in the box.
    fun confirm() {
        val index = epicPick ?: return
        val send = onConfirm ?: return
        if (sending) return
        val sent = words
        val choice = if (isOther) OTHER else model.options[index].name
        val text = if (isOther) sent.trim() else model.options[index].does
        answering = true
        failure = ""
        scope.launch {
            val result = try {
                send(choice, text)
            } catch (error: IOException) {
                ANSWER_FAILED
            }
            answering = false
            failure = result
            if (result.isEmpty() && words == sent) {
                words = ""
            }
        }
    }

    Column(
        modifier = modifier.fillMaxWidth().testTag("ask"),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        val cardShape = RoundedCornerShape(OrreryTokens.Radius.three)
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .clip(cardShape)
                .background(if (model.putOff) OrreryTokens.Surface.well else OrreryTokens.Role.attentionFill)
                .border(
                    OrreryTokens.Size.hairline,
                    if (model.putOff) OrreryTokens.Line.soft else OrreryTokens.Role.attentionLine,
                    cardShape,
                )
                .padding(OrreryTokens.Space.twelve),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                Text(
                    (if (model.putOff) "Put off" else "Asks you").inLabelCase(),
                    style = OrreryTokens.Type.label,
                    color = if (model.putOff) OrreryTokens.Ink.three else OrreryTokens.Role.attention,
                    modifier = Modifier.testTag("ask-label"),
                )
                Text(
                    model.who,
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Ink.two,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f).testTag("ask-who"),
                )
                if (model.asked > 0L) {
                    Text(
                        "· ${agoText(model.asked, now)}",
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Ink.three,
                        modifier = Modifier.testTag("ask-age"),
                    )
                }
                if (model.kind == AskKind.PERMISSION) {
                    Chip(text = "permission", tone = ChipTone.Attention)
                }
                if (!epic && !sending) {
                    if (model.putOff) {
                        if (onBringBack != null) {
                            TextButton(
                                onClick = onBringBack,
                                modifier = Modifier.heightIn(min = OrreryTokens.Size.controlTouch).testTag("ask-bring-back"),
                            ) {
                                Text("Bring it back")
                            }
                        }
                    } else if (onNotNow != null) {
                        TextButton(
                            onClick = onNotNow,
                            modifier = Modifier.heightIn(min = OrreryTokens.Size.controlTouch).testTag("ask-not-now"),
                        ) {
                            Text("Not now")
                        }
                    }
                    if (onDecline != null) {
                        IconButton(
                            onClick = { moreOpen = true },
                            modifier = Modifier.testTag("ask-more").semantics {
                                contentDescription = "More for this question"
                            },
                        ) {
                            Text("⋯", color = OrreryTokens.Ink.two, style = OrreryTokens.Type.title)
                        }
                    }
                }
            }

            if (model.markdown) {
                MarkdownBody(
                    blocks = remember(model.question) { parseMarkdown(model.question) },
                    modifier = Modifier.testTag("ask-question"),
                )
            } else {
                Text(model.question, style = OrreryTokens.Type.body, modifier = Modifier.testTag("ask-question"))
            }

            model.command?.let { command ->
                Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.two)) {
                    Text(command.tool, style = OrreryTokens.Type.label, color = OrreryTokens.Ink.three)
                    Text(
                        command.text,
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Ink.one,
                        modifier = Modifier
                            .fillMaxWidth()
                            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
                            .background(OrreryTokens.Surface.well)
                            .padding(OrreryTokens.Space.eight),
                    )
                }
            }

            if (!epic && model.options.isEmpty()) {
                Text(UNANSWERABLE, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two, modifier = Modifier.testTag("ask-unanswerable"))
            }

            Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
                model.options.forEachIndexed { index, option ->
                    val pressable = option.answerable && (epic || onAnswer != null)
                    val select: () -> Unit = {
                        if (epic) {
                            epicPick = index
                        } else {
                            onAnswer?.invoke(option.n)
                        }
                    }
                    AskOptionRow(
                        tag = "ask-option:${option.n}",
                        name = option.name,
                        does = option.does,
                        why = option.why,
                        recommended = option.recommended,
                        recommendedTag = "ask-recommended:${option.n}",
                        held = if (epic) pick == index else picked == option.n,
                        markdown = model.markdown,
                        enabled = !sending,
                        onClick = if (pressable) select else null,
                    )
                }
                if (hasOther) {
                    AskOptionRow(
                        tag = "ask-other",
                        name = SOMETHING_ELSE,
                        does = SOMETHING_ELSE_DOES,
                        why = "",
                        recommended = false,
                        recommendedTag = "",
                        held = isOther,
                        markdown = false,
                        enabled = !sending,
                        onClick = { epicPick = otherIndex },
                    )
                    if (isOther) {
                        OutlinedTextField(
                            value = words,
                            onValueChange = { words = it },
                            placeholder = { Text("Your answer") },
                            modifier = Modifier.fillMaxWidth().testTag("ask-other-text"),
                        )
                    }
                }
            }

            if (epic && onConfirm != null) {
                Button(
                    onClick = { confirm() },
                    enabled = pick != null && !sending && !(isOther && words.trim().isEmpty()),
                    modifier = Modifier.heightIn(min = OrreryTokens.Size.controlTouch).testTag("ask-confirm"),
                ) {
                    Text(if (pickedName.isEmpty()) "Answer" else "Answer with $pickedName")
                }
            }
            if (sending) {
                Text("sending…", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three, modifier = Modifier.testTag("ask-sending"))
            }
            if (hasOther && pick != null && recordText.isNotEmpty()) {
                Column(
                    modifier = Modifier.fillMaxWidth().testTag("ask-record").semantics(mergeDescendants = true) {},
                    verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.two),
                ) {
                    Text(RECORD_LABEL.inLabelCase(), style = OrreryTokens.Type.label, color = OrreryTokens.Ink.three)
                    Text(
                        "owner: $recordText",
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Ink.one,
                        modifier = Modifier
                            .fillMaxWidth()
                            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
                            .background(OrreryTokens.Surface.well)
                            .padding(OrreryTokens.Space.eight),
                    )
                }
            }

            if (shownNote.isNotEmpty()) {
                Text(shownNote, style = OrreryTokens.Type.small, color = OrreryTokens.Role.bad, modifier = Modifier.testTag("ask-note"))
            }

            if (stale != null) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    Text(
                        "Asked $stale ago and still waiting.",
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Ink.three,
                        modifier = Modifier.weight(1f).testTag("ask-stale"),
                    )
                    if (model.archive != null && onArchive != null) {
                        TextButton(
                            onClick = onArchive,
                            modifier = Modifier.heightIn(min = OrreryTokens.Size.controlTouch).testTag("ask-archive"),
                        ) {
                            Text("Archive this session")
                        }
                    }
                }
            }
        }

        if (moreOpen && !epic && !sending && onDecline != null) {
            ModalBottomSheet(onDismissRequest = { moreOpen = false }) {
                Column(
                    modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
                ) {
                    TextButton(
                        onClick = {
                            moreOpen = false
                            onDecline()
                        },
                        modifier = Modifier.fillMaxWidth().heightIn(min = OrreryTokens.Size.controlTouch).testTag("ask-decline"),
                    ) {
                        Column(modifier = Modifier.fillMaxWidth()) {
                            Text("Decline and end the turn", color = OrreryTokens.Role.bad)
                            Text("denies the request", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
                        }
                    }
                }
            }
        }
    }
}

/** An option's text as the assistant wrote it: its inline markup styled, its links not tappable inside the row. */
private fun markedUp(text: String, markdown: Boolean): AnnotatedString =
    if (markdown) parseInline(text, links = false) else AnnotatedString(text)

/** An option's index to pick first: the recommended one, else none. */
private fun initialPick(model: AskModel): Int? = model.options.indexOfFirst { it.recommended }.takeIf { it >= 0 }

/**
 * One option of an Ask: its name, the recommended chip, what it does and why. It is a button when [onClick] is set, and
 * [held] marks the one the owner has picked. Its name and what it does are drawn with their inline markup when [markdown]
 * is set. Its texts merge into one node under [tag].
 */
@Composable
private fun AskOptionRow(
    tag: String,
    name: String,
    does: String,
    why: String,
    recommended: Boolean,
    recommendedTag: String,
    held: Boolean,
    markdown: Boolean,
    enabled: Boolean,
    onClick: (() -> Unit)?,
) {
    val shape = RoundedCornerShape(OrreryTokens.Radius.two)
    val tap = if (onClick != null) {
        Modifier.clickable(enabled = enabled, role = Role.Button, onClick = onClick)
    } else {
        Modifier
    }
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(tag)
            .semantics(mergeDescendants = true) { selected = held }
            .clip(shape)
            .background(OrreryTokens.Surface.raised)
            .border(OrreryTokens.Size.hairline, if (held) OrreryTokens.Role.focus else OrreryTokens.Line.firm, shape)
            .heightIn(min = OrreryTokens.Size.controlTouch)
            .then(tap)
            .padding(horizontal = OrreryTokens.Space.twelve, vertical = OrreryTokens.Space.eight),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.two),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text(
                markedUp(name, markdown),
                style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                color = OrreryTokens.Ink.one,
            )
            if (recommended) {
                Chip(text = "recommended", tone = ChipTone.Neutral, modifier = Modifier.testTag(recommendedTag))
            }
        }
        if (does.isNotEmpty()) {
            Text(markedUp(does, markdown), style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
        }
        if (why.isNotEmpty()) {
            Text("Why: $why.", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
        }
    }
}

/**
 * The line an Ask folds to once its question is answered: "You answered", the option's name, then "· question". Its
 * number has no key cap on the phone, so it is the row's description for assistive technology.
 */
@Composable
internal fun AskAnsweredLine(n: Int, name: String, question: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("ask-answered")
            .semantics { contentDescription = "Answered option $n" },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text("You answered".inLabelCase(), style = OrreryTokens.Type.label, color = OrreryTokens.Ink.three)
        Text(name, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.one)
        Text(
            "· $question",
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.three,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f, fill = false),
        )
    }
}

/** The line an epic's question folds to once it is no longer open: "Settled", then "· project · slug". */
@Composable
internal fun AskSettledLine(who: String) {
    Row(
        modifier = Modifier.fillMaxWidth().testTag("ask-settled"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text("Settled".inLabelCase(), style = OrreryTokens.Type.label, color = OrreryTokens.Ink.three)
        Text("This question is settled.", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.one)
        Text(
            "· $who",
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.three,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f, fill = false),
        )
    }
}
