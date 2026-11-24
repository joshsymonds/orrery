package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.DesignQuestionRow

/** The tag of a question thread's root node, followed by the question's key, Q and its id (O.qthread in design.js). */
internal const val QUESTION_THREAD_TAG_PREFIX = "question-thread:"

/** The tag prefix of one option of a question, followed by the option's key (O.qthread's options in design.js). */
internal const val QUESTION_OPTION_TAG_PREFIX = "question-option:"

/** The tag of a question thread's answer box, where the owner types an answer or asks about the question. */
internal const val QUESTION_BOX_TAG = "question-box"

/** The tag of a question thread's Send control, which sends what is typed as the answer. */
internal const val QUESTION_SEND_TAG = "question-send"

/** The tag of a question thread's Accept control, which takes the recommendation and is the same as typing it. */
internal const val QUESTION_ACCEPT_TAG = "question-accept"

/** The tag of a question thread's settled answer line, drawn once the question is answered. */
internal const val QUESTION_ANSWER_TAG = "question-answer"

/**
 * One design question as its own small conversation, the thread round 8 draws for a question wherever it is asked (O.qthread
 * in design.js). It lists the [question]'s options with the recommended one marked, then the thread of what was typed about
 * it and what the Director said back, then an answer box, Send and Accept. [place] is the conversation or the design the
 * thread is drawn in, and [send] is what the last send is doing. [onAnswer] receives the answer's words, and [onAccept]
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
    Box(modifier.testTag(QUESTION_THREAD_TAG_PREFIX + question.key)) {}
}
