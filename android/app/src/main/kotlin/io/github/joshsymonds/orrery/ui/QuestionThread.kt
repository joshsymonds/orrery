package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.DesignQuestionRow
import io.github.joshsymonds.orrery.api.RecordMakerRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.time.Instant
import java.time.ZoneId

/** The tag of a question thread's root node, followed by the question's key, Q and its id (O.qthread in design.js). */
internal const val QUESTION_THREAD_TAG_PREFIX = "question-thread:"

/** The tag prefix of one option of a question, followed by the option's index, counted from 0 (O.qthread's options in design.js). */
internal const val QUESTION_OPTION_TAG_PREFIX = "question-option:"

/** The tag of a question thread's answer box, where the owner types an answer or asks about the question. */
internal const val QUESTION_BOX_TAG = "question-box"

/** The tag of a question thread's Send control, which sends what is typed as the answer. */
internal const val QUESTION_SEND_TAG = "question-send"

/** The tag of a question thread's Accept control, which takes the recommendation and is the same as typing it. */
internal const val QUESTION_ACCEPT_TAG = "question-accept"

/** The tag of a question thread's settled answer line, drawn once the question is answered. */
internal const val QUESTION_ANSWER_TAG = "question-answer"

/** The tag of the line that says who asked the question and when, the Director's ask in the thread. */
internal const val QUESTION_ASKED_TAG = "question-asked"

/** The tag of the line that names who made a settled answer, with its words: the answer's Maker mark. */
internal const val QUESTION_MAKER_TAG = "question-maker"

/** The tag of the line that says a send is on its way to the server. */
internal const val QUESTION_SENDING_TAG = "question-sending"

/** The tag of the reason a send did not land, drawn under the answer box. */
internal const val QUESTION_ERROR_TAG = "question-error"

/**
 * One design question as its own small conversation, the thread round 8 draws for a question wherever it is asked (O.qthread
 * in design.js). It lists the [question]'s options with the recommended one marked, then the thread of who asked it and when,
 * and once it is settled the answer with who made it. While it is open it has its own answer box, Send, and Accept, which
 * takes the recommendation. [place] is the conversation or the design the thread is drawn in, and only changes where it says
 * the Director asked. [send] is what the last send is doing. [onAnswer] receives the answer's words, trimmed, and [onAccept]
 * takes the recommendation. The root node carries [QUESTION_THREAD_TAG_PREFIX] and the question's key.
 */
@Composable
internal fun QuestionThread(
    question: DesignQuestionRow,
    place: ThreadPlace,
    send: ThreadSend,
    onAnswer: (String) -> Unit,
    onAccept: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val settled = question.answer.isNotEmpty()
    Column(
        modifier = modifier.fillMaxWidth().testTag(QUESTION_THREAD_TAG_PREFIX + question.key),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text(
                (if (settled) "Settled" else "Asks you").inLabelCase(),
                style = OrreryTokens.Type.label,
                color = if (settled) OrreryTokens.Ink.three else OrreryTokens.Role.attention,
            )
            Text(question.key, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
        }
        Text(question.text, style = OrreryTokens.Type.body, color = OrreryTokens.Ink.one)
        Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            question.options.forEachIndexed { index, option ->
                QuestionOptionRow(
                    tag = QUESTION_OPTION_TAG_PREFIX + index,
                    text = option,
                    recommended = question.recommendation.isNotEmpty() && option == question.recommendation,
                )
            }
        }
        AskedLine(place = place, asked = question.asked)
        if (settled) {
            SettledAnswer(question)
        } else {
            OpenAnswer(question = question, send = send, onAnswer = onAnswer, onAccept = onAccept)
        }
    }
}

/** One option of a question as its own node, with the word recommended beside the one the planner recommends. */
@Composable
private fun QuestionOptionRow(tag: String, text: String, recommended: Boolean) {
    val shape = RoundedCornerShape(OrreryTokens.Radius.two)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(tag)
            .semantics(mergeDescendants = true) {}
            .clip(shape)
            .background(OrreryTokens.Surface.raised)
            .border(OrreryTokens.Size.hairline, OrreryTokens.Line.firm, shape)
            .heightIn(min = OrreryTokens.Size.controlTouch)
            .padding(horizontal = OrreryTokens.Space.twelve, vertical = OrreryTokens.Space.eight),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(text, modifier = Modifier.weight(1f), style = OrreryTokens.Type.small, color = OrreryTokens.Ink.one)
        if (recommended) {
            Chip(text = "recommended", tone = ChipTone.Neutral)
        }
    }
}

/** Who asked the question and when: the Director, asked here or in the conversation, then the clock time it was asked. */
@Composable
private fun AskedLine(place: ThreadPlace, asked: Long) {
    val where = when (place) {
        ThreadPlace.CONVERSATION -> "asked here"
        ThreadPlace.DESIGN -> "asked in the conversation"
    }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(QUESTION_ASKED_TAG)
            .semantics(mergeDescendants = true) {},
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Chip(text = "the Director", tone = ChipTone.Engine)
        Text(where, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
        if (asked > 0L) {
            Text("· ${clockTimeOf(asked)}", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
        }
    }
}

/** The answer's line and the maker that settled it, once the question is answered. */
@Composable
private fun SettledAnswer(question: DesignQuestionRow) {
    val who = whoOf(question.maker)
    Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four)) {
        Text(
            if (question.accepted) "accepted the recommendation" else question.answer,
            modifier = Modifier.testTag(QUESTION_ANSWER_TAG),
            style = OrreryTokens.Type.body,
            color = OrreryTokens.Ink.one,
        )
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .testTag(QUESTION_MAKER_TAG)
                .semantics(mergeDescendants = true) { maker = who },
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Chip(
                text = whoName(who),
                tone = if (who == "director") ChipTone.Engine else ChipTone.Neutral,
            )
            val words = makerWords(question.maker)
            if (words.isNotEmpty()) {
                Text(words, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
            }
        }
    }
}

/**
 * The answer box, Send and Accept of an open question, with the reason a send failed under them. The box, Send and Accept
 * are disabled while the send is in flight, and enable again once it fails. Accept is drawn only when there is a
 * recommendation. The words typed stay in the box across a send, so a failed one can be sent again.
 */
@Composable
private fun OpenAnswer(
    question: DesignQuestionRow,
    send: ThreadSend,
    onAnswer: (String) -> Unit,
    onAccept: () -> Unit,
) {
    var typed by remember(question.design, question.id) { mutableStateOf("") }
    val words = typed.trim()
    val sending = send == ThreadSend.Sending
    val failure = (send as? ThreadSend.Failed)?.reason.orEmpty()
    Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        OutlinedTextField(
            value = typed,
            onValueChange = { typed = it },
            enabled = !sending,
            placeholder = { Text("Answer ${question.key}, or ask about it") },
            modifier = Modifier.fillMaxWidth().testTag(QUESTION_BOX_TAG),
        )
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Button(
                onClick = { onAnswer(words) },
                enabled = words.isNotEmpty() && !sending,
                modifier = Modifier.heightIn(min = OrreryTokens.Size.controlTouch).testTag(QUESTION_SEND_TAG),
            ) {
                Text("Send")
            }
            if (question.recommendation.isNotEmpty()) {
                Button(
                    onClick = onAccept,
                    enabled = !sending,
                    modifier = Modifier.heightIn(min = OrreryTokens.Size.controlTouch).testTag(QUESTION_ACCEPT_TAG),
                ) {
                    Text("Accept: ${question.recommendation}")
                }
            }
            if (sending) {
                Text("sending…", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three, modifier = Modifier.testTag(QUESTION_SENDING_TAG))
            }
        }
        if (failure.isNotEmpty()) {
            Text(failure, style = OrreryTokens.Type.small, color = OrreryTokens.Role.bad, modifier = Modifier.testTag(QUESTION_ERROR_TAG))
        }
    }
}

// whoName is a maker's name in the words round 8 gives the three makers (MAKER in design.js): you, the Director, orreryd.
private fun whoName(who: String): String = when (who) {
    "director" -> "the Director"
    else -> who
}

// makerWords says how a settled answer was made, after its maker's name: here for an answer given through serve, in the
// conversation with its quote for one given there, and for the Director's decision its reason and the delegation it rests on.
private fun makerWords(maker: RecordMakerRow?): String {
    if (maker == null) return ""
    return when (maker.kind) {
        "owner_serve" -> "here"
        "owner_conversation" -> joined("in the conversation", quoted(maker.quote))
        "conversation" -> joined("for you", maker.reason, if (maker.delegation.isEmpty()) "" else "under your words ${quoted(maker.delegation)}")
        else -> ""
    }
}

private fun joined(vararg parts: String): String = parts.filter { it.isNotEmpty() }.joinToString(" · ")

private fun quoted(text: String): String = if (text.isEmpty()) "" else "“$text”"

private fun clockTimeOf(time: Long): String = Instant.ofEpochSecond(time).atZone(ZoneId.systemDefault()).format(CLOCK_TIME)
