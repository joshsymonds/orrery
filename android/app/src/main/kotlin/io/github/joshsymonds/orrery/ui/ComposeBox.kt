package io.github.joshsymonds.orrery.ui

import android.text.Editable
import android.text.InputType
import android.text.TextWatcher
import android.util.TypedValue
import android.widget.EditText
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.res.ResourcesCompat
import androidx.core.view.OneShotPreDrawListener
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private const val COMPOSE_INPUT_TYPE =
    InputType.TYPE_CLASS_TEXT or
        InputType.TYPE_TEXT_FLAG_MULTI_LINE or
        InputType.TYPE_TEXT_FLAG_AUTO_CORRECT or
        InputType.TYPE_TEXT_FLAG_CAP_SENTENCES

/** The field grows with its draft up to this many lines, then scrolls inside itself. */
const val COMPOSE_MAX_LINES = 5

private const val FIELD_TEXT_SP = 13.5f
private const val FIELD_LINE_SPACING = 1.4f
private val FIELD_MIN_HEIGHT = 42.dp
private val FIELD_PAD_H = 12.dp
private val FIELD_PAD_V = 10.dp
private val COMPOSER_SHAPE = RoundedCornerShape(14.dp)
private val COMPOSER_PAD_V = 10.dp
private const val FOCUSED_OUTLINE_ALPHA = 0.7f

/**
 * Why a send left its draft in the field. [NOT_SENT] is a failed connection or an error answer, so
 * the server did not take the message. [NO_ANSWER] means the request was written and no answer came
 * in time, so the server may have taken it.
 */
private enum class SendFailure { NOT_SENT, NO_ANSWER }

/**
 * The composer for one pane, laid out like a text message: the draft field takes the row and
 * Send sits at its trailing edge, level with the field's bottom as the field grows. Send posts
 * the whole draft in one call and clears it only after the server answers with a success. The
 * field is locked while a send is in flight, so the draft cannot change under the request.
 *
 * The composer is its own band: a hairline above, a mantle bar, and a filled, outlined field whose
 * outline turns mauve while it has focus; a tap anywhere in the field focuses it. [status], when
 * given, sits at the top of the band, above the field.
 *
 * The field grows with the draft up to [COMPOSE_MAX_LINES] lines and never past [maxFieldHeight],
 * then scrolls inside itself, keeping the cursor's line in view from the first character of a new
 * line. [hint] shows in the empty field. [onDraftPresent] hears whether the draft has any text,
 * each time that changes.
 */
@Composable
fun ComposeBox(
    paneId: String,
    client: OrreryClient,
    modifier: Modifier = Modifier,
    maxFieldHeight: Dp = Dp.Unspecified,
    hint: String = "",
    onDraftPresent: (Boolean) -> Unit = {},
    status: (@Composable () -> Unit)? = null,
) {
    key(paneId) {
        ComposeDraft(paneId, client, modifier, maxFieldHeight, hint, onDraftPresent, status)
    }
}

@Composable
private fun ComposeDraft(
    paneId: String,
    client: OrreryClient,
    modifier: Modifier,
    maxFieldHeight: Dp,
    hint: String,
    onDraftPresent: (Boolean) -> Unit,
    status: (@Composable () -> Unit)?,
) {
    var draft by remember { mutableStateOf("") }
    var sending by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<SendFailure?>(null) }
    var focused by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val density = LocalDensity.current
    val draftPresent by rememberUpdatedState(onDraftPresent)
    val present = draft.isNotEmpty()
    LaunchedEffect(present) { draftPresent(present) }

    Column(modifier = modifier.testTag("composer").fillMaxWidth().background(Mocha.Mantle)) {
        Box(modifier = Modifier.fillMaxWidth().height(1.dp).background(Mocha.Surface0))
        status?.let { content ->
            Box(modifier = Modifier.fillMaxWidth().padding(start = 10.dp, end = 10.dp, top = 8.dp)) { content() }
        }
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = COMPOSER_PAD_V),
            verticalAlignment = Alignment.Bottom,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            AndroidView(
                modifier = Modifier
                    .weight(1f)
                    .heightIn(max = maxFieldHeight)
                    .clip(COMPOSER_SHAPE)
                    .background(Mocha.Surface0)
                    .border(
                        width = 1.dp,
                        color = if (focused) Mocha.Mauve.copy(alpha = FOCUSED_OUTLINE_ALPHA) else Mocha.Surface1,
                        shape = COMPOSER_SHAPE,
                    ),
                factory = { context ->
                    EditText(context).apply {
                        inputType = COMPOSE_INPUT_TYPE
                        maxLines = COMPOSE_MAX_LINES
                        isVerticalScrollBarEnabled = true
                        background = null
                        typeface = ResourcesCompat.getFont(context, R.font.orrery_mono_regular)
                        setTextSize(TypedValue.COMPLEX_UNIT_SP, FIELD_TEXT_SP)
                        setLineSpacing(0f, FIELD_LINE_SPACING)
                        setTextColor(Mocha.Text.toArgb())
                        setHintTextColor(Mocha.Overlay0.toArgb())
                        setOnFocusChangeListener { _, hasFocus -> focused = hasFocus }
                        with(density) {
                            minHeight = FIELD_MIN_HEIGHT.roundToPx()
                            setPadding(
                                FIELD_PAD_H.roundToPx(),
                                FIELD_PAD_V.roundToPx(),
                                FIELD_PAD_H.roundToPx(),
                                FIELD_PAD_V.roundToPx(),
                            )
                        }
                        addTextChangedListener(
                            object : TextWatcher {
                                override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) = Unit

                                override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) = Unit

                                override fun afterTextChanged(s: Editable?) {
                                    draft = s.toString()
                                    val field = this@apply
                                    OneShotPreDrawListener.add(field) { field.revealCursor() }
                                }
                            },
                        )
                        addOnLayoutChangeListener { view, _, top, _, bottom, _, oldTop, _, oldBottom ->
                            if (bottom - top != oldBottom - oldTop) (view as EditText).revealCursor()
                        }
                    }
                },
                update = { view ->
                    view.isEnabled = !sending
                    if (view.hint?.toString().orEmpty() != hint) view.hint = hint
                    if (view.text.toString() != draft) {
                        view.setText(draft)
                    }
                },
            )
            Button(
                enabled = draft.isNotBlank() && !sending,
                modifier = Modifier.height(FIELD_MIN_HEIGHT),
                shape = COMPOSER_SHAPE,
                contentPadding = PaddingValues(horizontal = 16.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = Mocha.Mauve,
                    contentColor = Mocha.Crust,
                    disabledContainerColor = Mocha.Surface0,
                    disabledContentColor = Mocha.Overlay0,
                ),
                onClick = {
                    val text = draft
                    sending = true
                    failure = null
                    scope.launch {
                        val outcome = withContext(Dispatchers.IO) {
                            try {
                                client.sendComposed(paneId, text)
                                null
                            } catch (error: NoAnswerException) {
                                SendFailure.NO_ANSWER
                            } catch (error: IOException) {
                                SendFailure.NOT_SENT
                            }
                        }
                        sending = false
                        if (outcome == null) {
                            draft = ""
                        } else {
                            failure = outcome
                        }
                    }
                },
            ) {
                Text("Send", fontWeight = FontWeight.Bold)
            }
        }
        failure?.let { reason ->
            Text(
                when (reason) {
                    SendFailure.NOT_SENT -> "not sent"
                    SendFailure.NO_ANSWER -> "no answer — check before resending"
                },
                modifier = Modifier.padding(start = 14.dp, bottom = 6.dp),
                color = when (reason) {
                    SendFailure.NOT_SENT -> Mocha.Red
                    SendFailure.NO_ANSWER -> Mocha.Yellow
                },
                fontSize = 12.sp,
            )
        }
    }
}

/**
 * Scrolls the field so the cursor's whole line is inside its text area, and no further than the
 * text allows. Run before the draw that follows an edit and again whenever the field's height
 * changes, so a new line shows at once instead of when the field next happens to scroll.
 */
private fun EditText.revealCursor() {
    val textLayout = layout ?: return
    val visible = height - extendedPaddingTop - extendedPaddingBottom
    if (visible <= 0) return
    val line = textLayout.getLineForOffset(selectionEnd.coerceAtLeast(0))
    val top = textLayout.getLineTop(line)
    val bottom = textLayout.getLineBottom(line)
    val maxScroll = (textLayout.height - visible).coerceAtLeast(0)
    val target = when {
        bottom > scrollY + visible -> bottom - visible
        top < scrollY -> top
        else -> scrollY
    }.coerceIn(0, maxScroll)
    if (target != scrollY) scrollTo(scrollX, target)
}
