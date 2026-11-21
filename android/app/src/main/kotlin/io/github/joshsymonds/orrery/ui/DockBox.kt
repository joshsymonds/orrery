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
import androidx.compose.runtime.DisposableEffect
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.res.ResourcesCompat
import androidx.core.view.OneShotPreDrawListener
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.push.KeptReplies
import io.github.joshsymonds.orrery.push.KeptReply
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private const val COMPOSE_INPUT_TYPE =
    InputType.TYPE_CLASS_TEXT or
        InputType.TYPE_TEXT_FLAG_MULTI_LINE or
        InputType.TYPE_TEXT_FLAG_AUTO_CORRECT or
        InputType.TYPE_TEXT_FLAG_CAP_SENTENCES

/** The blank line that sets one appended text off from the draft before it. */
private const val DRAFT_BREAK = "\n\n"

/** The line break that sets a kept shade reply on its own line after the draft text before it. */
private const val KEPT_BREAK = "\n"

/** The field grows with its draft up to this many lines, then scrolls inside itself. */
const val COMPOSE_MAX_LINES = 5

private val FIELD_TEXT_SP = OrreryTokens.Type.body.fontSize.value
private val FIELD_LINE_SPACING = OrreryTokens.Type.body.lineHeight.value / OrreryTokens.Type.body.fontSize.value
private val FIELD_MIN_HEIGHT = OrreryTokens.Size.controlTouch
private val FIELD_PAD_H = OrreryTokens.Space.twelve
private val FIELD_PAD_V = OrreryTokens.Space.eight + OrreryTokens.Space.two
private val COMPOSER_SHAPE = RoundedCornerShape(OrreryTokens.Radius.three)
private val COMPOSER_PAD_V = OrreryTokens.Space.eight + OrreryTokens.Space.two

/**
 * Why a send left its draft in the field. [NOT_SENT] is a failed connection or an error answer, so
 * the server did not take the message. [NO_ANSWER] means the request was written and no answer came
 * in time, so the server may have taken it.
 */
private enum class SendFailure { NOT_SENT, NO_ANSWER }

/** The state of the conversation a composer speaks to, as its field's outline shows it. */
enum class ComposeEdge { IDLE, WORKING, WAITING, HELD }

/** The edge a pane or session state shows: working and waiting have their own, any other state is idle. */
fun composeEdgeOf(state: String): ComposeEdge = when (state) {
    "working" -> ComposeEdge.WORKING
    "waiting" -> ComposeEdge.WAITING
    else -> ComposeEdge.IDLE
}

/**
 * The text a dock box edits and sends. The dock holds one per conversation and adds texts to it with
 * [append]; the box clears what a successful send took. It also takes the conversation's kept shade replies
 * with [joinKept], and holds each one until a send that takes it lands or the draft is cleared.
 */
class DockDraft {
    var text: String by mutableStateOf("")
        private set

    // The kept replies this draft holds, and the store and conversation they are kept for.
    private val held = mutableListOf<KeptReply>()
    private var keptFor: KeptFor? = null

    internal fun change(text: String) {
        this.text = text
        if (text.isEmpty()) letGo(held.toList())
    }

    /** Puts [texts] after the draft, joined by blank lines. No texts leave the draft as it is. */
    fun append(texts: List<String>) {
        if (texts.isEmpty()) return
        val added = texts.joinToString(DRAFT_BREAK)
        text = if (text.isEmpty()) added else "$text$DRAFT_BREAK$added"
    }

    /** Joins the replies this draft does not hold yet, each on a new line after the draft, and holds them. */
    internal fun joinKept(from: KeptFor, replies: List<KeptReply>) {
        val fresh = replies.filter { reply -> held.none { it.id == reply.id } }
        if (fresh.isEmpty()) return
        keptFor = from
        held += fresh
        val added = fresh.joinToString(KEPT_BREAK) { it.text }
        text = if (text.isEmpty()) added else "$text$KEPT_BREAK$added"
    }

    /** The kept replies the draft holds now, which a send of the draft as it is now takes. */
    internal fun heldNow(): List<KeptReply> = held.toList()

    /** A send that landed took [sent] from the draft, and the kept replies [sentHeld] it held: they are let go. */
    internal fun landed(sent: String, sentHeld: List<KeptReply>) {
        letGo(sentHeld)
        change(text.removePrefix(sent).removePrefix(DRAFT_BREAK))
    }

    private fun letGo(replies: List<KeptReply>) {
        val from = keptFor ?: return
        if (replies.isEmpty()) return
        held.removeAll { reply -> replies.any { it.id == reply.id } }
        from.store.release(from.host, from.kind, from.id, replies.mapTo(HashSet()) { it.id })
    }
}

/** The store that keeps a conversation's shade replies, and the host, kind and id the replies are kept for. */
internal class KeptFor(val store: KeptReplies, val host: String, val kind: String, val id: String)

/** The draft for [key]; it starts empty again when [key] changes. */
@Composable
fun rememberDockDraft(key: Any): DockDraft = remember(key) { DockDraft() }

/**
 * Joins the shade replies kept for one conversation into [draft]: when the page opens, and each time the app returns to
 * it (ON_START), also while the page is already on screen. Nothing is joined until [host] is known. A reply joins once,
 * and stays kept until the draft that holds it is sent or cleared.
 */
@Composable
fun JoinKeptReplies(draft: DockDraft, host: String, kind: String, id: String) {
    val context = LocalContext.current
    val store = remember(context) { KeptReplies(context) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val scope = rememberCoroutineScope()
    DisposableEffect(draft, store, host, kind, id, lifecycle) {
        if (host.isEmpty()) return@DisposableEffect onDispose {}
        val from = KeptFor(store, host, kind, id)
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_START) {
                scope.launch {
                    val replies = withContext(Dispatchers.IO) { store.kept(host, kind, id) }
                    draft.joinKept(from, replies)
                }
            }
        }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }
}

/**
 * The composer for one pane, laid out like a text message: the draft field takes the row and
 * Send sits at its trailing edge, level with the field's bottom as the field grows. Send posts
 * the whole draft in one call and takes from [draft] only what that call sent, once the server
 * answers with a success. The field is locked while a send is in flight, so the text the send
 * took cannot change under the request; texts appended meanwhile stay in the draft.
 *
 * The composer is its own band: a hairline above, a mantle bar, and a filled, outlined field whose
 * outline turns mauve while it has focus; a tap anywhere in the field focuses it. [edge] colours that
 * outline by the conversation's state: yellow while working, peach while waiting or held. [status],
 * when given, sits at the top of the band, above the field.
 *
 * The field grows with the draft up to [COMPOSE_MAX_LINES] lines and never past [maxFieldHeight],
 * then scrolls inside itself, keeping the cursor's line in view from the first character of a new
 * line. [hint] shows in the empty field. [onDraftPresent] hears whether the draft has any text,
 * each time that changes. [onSend] hears each tap on Send, before the request goes out. [send]
 * takes the whole draft off the main thread; it defaults to posting the draft to [paneId]. A
 * throw of [IOException] keeps the draft and shows the failure label, as a failed post does. A landed send also lets go
 * of the kept shade replies the draft held when Send was tapped.
 */
@Composable
fun DockBox(
    draft: DockDraft,
    client: OrreryClient,
    paneId: String,
    modifier: Modifier = Modifier,
    edge: ComposeEdge = ComposeEdge.IDLE,
    maxFieldHeight: Dp = Dp.Unspecified,
    hint: String = "",
    onDraftPresent: (Boolean) -> Unit = {},
    status: (@Composable () -> Unit)? = null,
    onSend: () -> Unit = {},
    send: (String) -> Unit = { text -> client.sendComposed(paneId, text) },
) {
    key(paneId) {
        DockField(draft, edge, modifier, maxFieldHeight, hint, onDraftPresent, status, onSend, send)
    }
}

@Composable
private fun DockField(
    draft: DockDraft,
    edge: ComposeEdge,
    modifier: Modifier,
    maxFieldHeight: Dp,
    hint: String,
    onDraftPresent: (Boolean) -> Unit,
    status: (@Composable () -> Unit)?,
    onSend: () -> Unit,
    send: (String) -> Unit,
) {
    var sending by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<SendFailure?>(null) }
    var focused by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val density = LocalDensity.current
    val draftPresent by rememberUpdatedState(onDraftPresent)
    val present = draft.text.isNotEmpty()
    LaunchedEffect(present) { draftPresent(present) }
    val outline = when (edge) {
        ComposeEdge.IDLE -> if (focused) OrreryTokens.Role.focus else OrreryTokens.Line.firm
        ComposeEdge.WORKING -> OrreryTokens.State.working
        ComposeEdge.WAITING, ComposeEdge.HELD -> OrreryTokens.State.waiting
    }
    val edgeTag = "composer-edge:${edge.name.lowercase()}"

    Column(modifier = modifier.testTag("composer").fillMaxWidth().background(OrreryTokens.Surface.well)) {
        Box(modifier = Modifier.fillMaxWidth().height(OrreryTokens.Size.hairline).background(OrreryTokens.Line.soft))
        status?.let { content ->
            Box(
                modifier = Modifier.fillMaxWidth().padding(
                    start = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                    end = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                    top = OrreryTokens.Space.eight,
                ),
            ) { content() }
        }
        Row(
            modifier = Modifier.fillMaxWidth().padding(
                horizontal = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                vertical = COMPOSER_PAD_V,
            ),
            verticalAlignment = Alignment.Bottom,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            AndroidView(
                modifier = Modifier
                    .weight(1f)
                    .testTag(edgeTag)
                    .heightIn(max = maxFieldHeight)
                    .clip(COMPOSER_SHAPE)
                    .background(OrreryTokens.Surface.raised)
                    .border(
                        width = OrreryTokens.Size.rail,
                        color = outline,
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
                        setTextColor(OrreryTokens.Ink.one.toArgb())
                        setHintTextColor(OrreryTokens.Ink.four.toArgb())
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
                                    draft.change(s.toString())
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
                    if (view.text.toString() != draft.text) {
                        view.setText(draft.text)
                    }
                },
            )
            Button(
                enabled = draft.text.isNotBlank() && !sending,
                modifier = Modifier.height(FIELD_MIN_HEIGHT),
                shape = COMPOSER_SHAPE,
                contentPadding = PaddingValues(horizontal = OrreryTokens.Space.sixteen),
                colors = ButtonDefaults.buttonColors(
                    containerColor = OrreryTokens.Role.primary,
                    contentColor = OrreryTokens.Ink.onAccent,
                    disabledContainerColor = OrreryTokens.Surface.raised,
                    disabledContentColor = OrreryTokens.Ink.three,
                ),
                onClick = {
                    val text = draft.text
                    val sentHeld = draft.heldNow()
                    onSend()
                    sending = true
                    failure = null
                    scope.launch {
                        val outcome = withContext(Dispatchers.IO) {
                            try {
                                send(text)
                                null
                            } catch (error: NoAnswerException) {
                                SendFailure.NO_ANSWER
                            } catch (error: IOException) {
                                SendFailure.NOT_SENT
                            }
                        }
                        sending = false
                        if (outcome == null) {
                            draft.landed(text, sentHeld)
                        } else {
                            failure = outcome
                        }
                    }
                },
            ) {
                Text("Send", style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
            }
        }
        failure?.let { reason ->
            Text(
                when (reason) {
                    SendFailure.NOT_SENT -> DockLines.sendRefused
                    SendFailure.NO_ANSWER -> DockLines.sendUnknown
                },
                modifier = Modifier.padding(
                    start = OrreryTokens.Space.twelve + OrreryTokens.Space.two,
                    bottom = OrreryTokens.Space.four + OrreryTokens.Space.two,
                ),
                color = when (reason) {
                    SendFailure.NOT_SENT -> OrreryTokens.Role.bad
                    SendFailure.NO_ANSWER -> OrreryTokens.Ink.three
                },
                style = OrreryTokens.Type.small,
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
