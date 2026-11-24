package io.github.joshsymonds.orrery.ui

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.EnterExitState
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.togetherWith
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.Transition
import androidx.compose.animation.core.animateFloat
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.SemanticsPropertyReceiver
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.ContractCallResult
import io.github.joshsymonds.orrery.api.ContractRecordRow
import io.github.joshsymonds.orrery.api.ContractRequirementWorkRow
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.DesignContractRow
import io.github.joshsymonds.orrery.api.DesignQuestionRow
import io.github.joshsymonds.orrery.api.DesignRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.spec
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

/**
 * What the round 8 phone sheet is open on: a [Thing], a design, contract or epic opened from a work link, or [All], the
 * list of every design's block and the loose epics that "+N" opens. Its [key] is the link's key, or [WORK_ALL] for All,
 * the value the work links light when that list is open.
 */
internal sealed interface SheetSubject {
    val key: String

    /** A design, contract or epic the sheet is open on, opened from [link] in the conversation's title row. */
    data class Thing(val link: WorkLink) : SheetSubject {
        override val key: String get() = link.key
    }

    /** The list round 8 opens from "+N": every design's block and the loose epics under them. */
    data object All : SheetSubject {
        override val key: String get() = WORK_ALL
    }
}

/** The kind word the phone sheet's head draws before the thing's name, as round 8's recordPage draws "working on". */
internal const val SHEET_KIND_WORDS = "working on"

/**
 * The content of the round 8 phone sheet's head, as round 8's recordPage draws it on touch: the [face] of the work link
 * (null for All, which draws the design glyph), the [faceKey] that names the face, the [up] name of the design the
 * thing belongs to (or null), the thing's [name] in bold under the kind word, and the [state] words on the second line,
 * empty for none.
 */
internal data class SheetHead(
    val face: WorkKind?,
    val faceKey: String,
    val up: String?,
    val name: String,
    val state: String,
)

/** The phone sheet's root node: the test tag, and the pose its node carries as [SheetPoseKey]. */
internal const val PHONE_SHEET_TAG = "phone-sheet"

/** The head row of the phone sheet: the face, the kind word, the up link, the name and the state words. */
internal const val SHEET_HEAD_TAG = "sheet-head"

/** The kind word of the phone sheet's head: "working on", in round 8's head row. */
internal const val SHEET_KIND_TAG = "sheet-kind"

/** The up link of the phone sheet's head: the design the thing belongs to, when it has one. */
internal const val SHEET_UP_TAG = "sheet-up"

/** The thing's name in bold on the phone sheet's head. */
internal const val SHEET_NAME_TAG = "sheet-name"

/** The second line of the phone sheet's head: the state words of the thing, empty for none. */
internal const val SHEET_STATE_TAG = "sheet-state"

/** The close control at the end of the phone sheet's head, which dismisses the sheet. */
internal const val SHEET_CLOSE_TAG = "sheet-close"

/** The prefix of a phone sheet body's test tag; each body is tagged with the prefix and its subject's key. */
internal const val SHEET_BODY_TAG_PREFIX = "sheet-body:"

/**
 * The band of the phone sheet: the bracket's band that the sheet sits directly under, which carries [SheetFadeKey].
 */
internal const val SHEET_BAND_TAG = "sheet-band"

/** The spine of the phone sheet: the bracket's band as it runs down the sheet's left side. */
internal const val SHEET_SPINE_TAG = "sheet-spine"

/** The foot of the phone sheet: the bracket's band as it runs under the sheet. */
internal const val SHEET_FOOT_TAG = "sheet-foot"

/**
 * The pose of the round 8 phone sheet as it moves: its vertical [scaleY], its [offsetY] from its rest place, and its
 * [alpha]. The sheet's node carries the current pose as [SheetPoseKey].
 */
internal data class SheetPose(val scaleY: Float, val offsetY: Dp, val alpha: Float) {
    companion object {
        /** Where round 8's sheet rests on touch: full height, in its place, and fully opaque. */
        val REST = SheetPose(1f, 0.dp, 1f)

        /** Where round 8's sheet folds when it leaves: half height, lifted by one drill distance, and transparent. */
        val FOLDED = SheetPose(0.5f, -OrreryTokens.Motion.Distance.drill, 0f)
    }
}

/** Semantics key: the [SheetPose] the phone sheet's node is at now, read by tests from the sheet's node. */
internal val SheetPoseKey = SemanticsPropertyKey<SheetPose>("SheetPose")

/** Semantics key: the alpha the phone sheet's band and each of its bodies are at now, read by tests from them. */
internal val SheetFadeKey = SemanticsPropertyKey<Float>("SheetFade")

/**
 * The [SheetPose] a phone sheet node carries, set as [SheetPoseKey]. It is write-only, as every semantics property is
 * in Compose: a test reads the key from the node's configuration.
 */
internal var SemanticsPropertyReceiver.sheetPose: SheetPose
    get() = throw UnsupportedOperationException("sheetPose is write-only; read SheetPoseKey from the node")
    set(value) {
        this[SheetPoseKey] = value
    }

/**
 * The alpha a phone sheet's band or body carries, set as [SheetFadeKey]. It is write-only, as every semantics property
 * is in Compose: a test reads the key from the node's configuration.
 */
internal var SemanticsPropertyReceiver.sheetFade: Float
    get() = throw UnsupportedOperationException("sheetFade is write-only; read SheetFadeKey from the node")
    set(value) {
        this[SheetFadeKey] = value
    }

/** The head's inset from the sides of the sheet, as the conversation's title row insets its own. */
private val HEAD_INSET: Dp = OrreryTokens.Space.twelve + OrreryTokens.Space.two

/**
 * The phone sheet round 8 draws over the chat on touch: nothing while no sheet has been open, otherwise the bracket that
 * begins at the lit name in the title. Under the head, the band of the raised ground runs the full width, [OrreryTokens.Space.eight]
 * tall; below it, the sheet is the column on the side's ground, rounded at its left corners and tagged [PHONE_SHEET_TAG],
 * with the bracket's spine down its left side and its foot under it, each [OrreryTokens.Space.eight] across. The sheet
 * carries its pose, and its head row holds the face, the kind word, the [SheetHead.up] link (which [onUp] opens) when the
 * thing has one,
 * the name, and the close control at its right end, over the state line when the head has one. The [body] of the subject
 * fills the rest of the sheet. The close control and the back gesture call [onClose] while the sheet is open.
 *
 * [view] is what the sheet is open on, null while it is closed. Opening unfolds the sheet down from the band, from
 * [SheetPose.FOLDED] to [SheetPose.REST] over [Move.DRILL], and fades the band and the bracket in over [Move.STATE].
 * Closing folds it back into the band over [Move.LEAVE] and fades them out, and the sheet leaves the composition only once
 * that is done. A switch of subject keeps the sheet and its band at rest: the old body fades out over [Move.LEAVE] and the
 * new one in over [Move.STATE], where it stands. With [animated] false, which is the system's animator duration scale at
 * 0, each of these is a plain swap to the state after. [onDrawn] is told whether the sheet is drawn, which stays true while a
 * close folds it away, so a host can keep what the sheet covers out of the composition until it has gone.
 */
@Composable
internal fun PhoneSheet(
    view: SheetView?,
    animated: Boolean,
    onClose: () -> Unit,
    onUp: (WorkLink) -> Unit,
    modifier: Modifier = Modifier,
    onDrawn: (Boolean) -> Unit = {},
    body: @Composable (SheetSubject) -> Unit,
) {
    val open = view != null
    // The view the sheet was last open on, which it draws while a close folds it away.
    val hold = remember { SheetHold() }
    if (view != null) hold.view = view
    // The unfold runs from folded (0) to rest (1), and the fade of the band and the bracket with it. Both start folded,
    // so the first open unfolds.
    val unfoldAnim = remember { Animatable(0f) }
    val fadeAnim = remember { Animatable(0f) }
    // Whether the sheet is drawn: a close keeps it until it has folded away.
    var present by remember { mutableStateOf(open) }
    BackHandler(enabled = open, onBack = onClose)
    LaunchedEffect(open, animated) {
        if (!animated) return@LaunchedEffect
        if (open) {
            present = true
            launch { fadeAnim.animateTo(1f, Move.STATE.spec(animated)) }
            unfoldAnim.animateTo(1f, Move.DRILL.spec(animated))
        } else if (present) {
            coroutineScope {
                launch { fadeAnim.animateTo(0f, Move.LEAVE.spec(animated)) }
                unfoldAnim.animateTo(0f, Move.LEAVE.spec(animated))
            }
            present = false
        }
    }
    // Drawn while it is open, and while a close with animations on has not yet folded it away.
    val drawn = hold.view != null && (open || (animated && present))
    SideEffect { onDrawn(drawn) }
    val shown = hold.view ?: return
    // With animations off a close removes the sheet at once; with them on it stays until it has folded away.
    if (!drawn) return
    // Read here, so each frame of the motion draws the values the sheet is at; with animations off, the target itself.
    val progress = if (animated) unfoldAnim.value else if (open) 1f else 0f
    val fading = if (animated) fadeAnim.value else if (open) 1f else 0f
    val pose = poseAt(progress)
    val head = shown.head
    val upClick: (() -> Unit)? = shown.up?.let { link -> { onUp(link) } }
    Column(modifier = modifier) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(OrreryTokens.Space.eight)
                .graphicsLayer { alpha = fading }
                .background(OrreryTokens.Surface.raised)
                .testTag(SHEET_BAND_TAG)
                .semantics { sheetFade = fading },
        ) {}
        Row(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                // The bracket's ground, which shows at the sheet's rounded corners, fades with the band.
                .drawBehind { drawRect(OrreryTokens.Surface.raised, alpha = fading) },
        ) {
            Box(
                modifier = Modifier
                    .fillMaxHeight()
                    .width(OrreryTokens.Space.eight)
                    .testTag(SHEET_SPINE_TAG),
            ) {}
            // The node the sheet's semantics read is laid out where the sheet rests; the column in it is drawn at the pose.
            Box(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxHeight()
                    .testTag(PHONE_SHEET_TAG)
                    .semantics { sheetPose = pose },
            ) {
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .graphicsLayer {
                            scaleY = pose.scaleY
                            translationY = pose.offsetY.toPx()
                            alpha = pose.alpha
                            transformOrigin = TransformOrigin(0.5f, 0f)
                        }
                        .background(
                            OrreryTokens.Surface.side,
                            RoundedCornerShape(topStart = OrreryTokens.Radius.three, bottomStart = OrreryTokens.Radius.three),
                        ),
                ) {
                    Column(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(start = HEAD_INSET, end = HEAD_INSET, top = OrreryTokens.Space.eight),
                    ) {
                        Row(
                            modifier = Modifier.fillMaxWidth().testTag(SHEET_HEAD_TAG),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                        ) {
                            SheetFace(head)
                            Text(
                                SHEET_KIND_WORDS,
                                modifier = Modifier.testTag(SHEET_KIND_TAG),
                                style = OrreryTokens.Type.label,
                                color = OrreryTokens.Ink.three,
                            )
                            val up = head.up
                            if (up != null && upClick != null) {
                                Text(
                                    up,
                                    modifier = Modifier.testTag(SHEET_UP_TAG).clickable(onClick = upClick),
                                    style = OrreryTokens.Type.body,
                                    color = OrreryTokens.Ink.two,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                                Text("/", style = OrreryTokens.Type.body, color = OrreryTokens.Ink.three)
                            }
                            Text(
                                head.name,
                                modifier = Modifier.testTag(SHEET_NAME_TAG).weight(1f),
                                style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                                color = OrreryTokens.Ink.one,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            SheetCloseButton(onClose)
                        }
                        if (head.state.isNotEmpty()) {
                            Text(
                                head.state,
                                modifier = Modifier.padding(top = OrreryTokens.Space.two).testTag(SHEET_STATE_TAG),
                                style = OrreryTokens.Type.small,
                                color = OrreryTokens.Ink.three,
                            )
                        }
                    }
                    if (animated) {
                        AnimatedContent(
                            targetState = shown.subject,
                            modifier = Modifier.weight(1f).fillMaxWidth(),
                            contentKey = { it.key },
                            transitionSpec = { EnterTransition.None togetherWith ExitTransition.None },
                            label = "sheet-body",
                        ) { current ->
                            val bodyFade = sheetBodyFade(transition)
                            SheetBodyBox(current, bodyFade, Modifier.fillMaxSize(), body)
                        }
                    } else {
                        SheetBodyBox(shown.subject, 1f, Modifier.weight(1f).fillMaxWidth(), body)
                    }
                }
            }
        }
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(OrreryTokens.Space.eight)
                .graphicsLayer { alpha = fading }
                .background(OrreryTokens.Surface.raised)
                .testTag(SHEET_FOOT_TAG),
        ) {}
    }
}

/** The view a phone sheet was last open on, which it keeps drawing while a close folds it away. */
private class SheetHold {
    var view: SheetView? = null
}

// The pose the sheet is at [progress] along its unfold: folded at 0, at rest at 1, and between the two as it moves.
private fun poseAt(progress: Float): SheetPose {
    val folded = SheetPose.FOLDED
    val rest = SheetPose.REST
    return SheetPose(
        scaleY = folded.scaleY + (rest.scaleY - folded.scaleY) * progress,
        offsetY = folded.offsetY + (rest.offsetY - folded.offsetY) * progress,
        alpha = folded.alpha + (rest.alpha - folded.alpha) * progress,
    )
}

// The alpha of a body within its content's own [transition]: it fades in over Move.STATE as it enters, and out over
// Move.LEAVE as it leaves. AnimatedContent keeps a leaving body until this fade has finished.
@Composable
private fun sheetBodyFade(transition: Transition<EnterExitState>): Float {
    val fade by transition.animateFloat(
        transitionSpec = {
            if (EnterExitState.PreEnter isTransitioningTo EnterExitState.Visible) {
                Move.STATE.spec(animated = true)
            } else {
                Move.LEAVE.spec(animated = true)
            }
        },
        label = "sheet-body-fade",
    ) { state -> if (state == EnterExitState.Visible) 1f else 0f }
    return fade
}

// A phone sheet body's box, drawn at [fade] and tagged with its subject's key, which it carries as its sheetFade.
@Composable
private fun SheetBodyBox(
    subject: SheetSubject,
    fade: Float,
    modifier: Modifier,
    body: @Composable (SheetSubject) -> Unit,
) {
    Box(
        modifier = modifier
            .graphicsLayer { alpha = fade }
            .testTag(SHEET_BODY_TAG_PREFIX + subject.key)
            .semantics { sheetFade = fade },
    ) {
        body(subject)
    }
}

// The face of the head: the design's glyph for a design and for All, the contract's mark for an epic or a contract.
@Composable
private fun SheetFace(head: SheetHead) {
    when (head.face) {
        null, WorkKind.DESIGN -> GlyphIcon(Glyph.DESIGN, OrreryTokens.Ink.one)
        WorkKind.EPIC, WorkKind.CONTRACT -> ContractMark(head.faceKey)
    }
}

// The X arms of the close control, on the sixteen grid the round 8 glyphs are drawn on: from four to twelve, one and a
// half wide.
private const val CLOSE_INSET = 4f / 16f
private const val CLOSE_STROKE = 1.5f / 16f

// The close control at the end of the head: a touch square with an X in the second ink, named "Close" for the reader since
// it shows no words. It calls [onClose].
@Composable
private fun SheetCloseButton(onClose: () -> Unit) {
    val ink = OrreryTokens.Ink.two
    Box(
        modifier = Modifier
            .testTag(SHEET_CLOSE_TAG)
            .size(OrreryTokens.Size.controlTouch)
            .clickable(role = Role.Button, onClick = onClose)
            .semantics { contentDescription = "Close" },
        contentAlignment = Alignment.Center,
    ) {
        Canvas(modifier = Modifier.size(OrreryTokens.Size.icon)) {
            val inset = size.width * CLOSE_INSET
            val stroke = size.width * CLOSE_STROKE
            drawLine(ink, Offset(inset, inset), Offset(size.width - inset, size.height - inset), stroke, StrokeCap.Round)
            drawLine(ink, Offset(size.width - inset, inset), Offset(inset, size.height - inset), stroke, StrokeCap.Round)
        }
    }
}

/**
 * The record a phone sheet draws its subjects from, as the conversation's screen holds it at [now] in unix seconds: the
 * conversation's [conversation] id, its [work] links, its [designs], the epics it steers with their details, and the
 * [contracts] listed. The designs are read, and their questions answered, through [stages].
 */
internal class SheetRecord(
    val client: OrreryClient,
    val conversation: String,
    val work: List<WorkLink>,
    val designs: List<DesignRow>,
    val held: HeadEpics,
    val contracts: List<ContractRow>,
    val now: Long,
    val stages: DesignStages,
)

/**
 * What the sheet shows of an open subject: the [subject], its [head], and the design link its head's up names, which is
 * [up] (null when the thing belongs to no design of the conversation).
 */
internal class SheetView(val subject: SheetSubject, val head: SheetHead, val up: WorkLink?)

/**
 * The view of the open [subject] over [record], or null when no sheet is open. A design the conversation no longer draws
 * opens no sheet. "+N" opens the list, whose head names the number of work links.
 */
internal fun sheetViewOf(subject: SheetSubject?, record: SheetRecord): SheetView? = when (subject) {
    null -> null
    SheetSubject.All -> SheetView(
        SheetSubject.All,
        SheetHead(face = null, faceKey = WORK_ALL, up = null, name = "${record.work.size} things", state = ""),
        up = null,
    )
    is SheetSubject.Thing -> thingViewOf(subject, record)
}

// The view of a design, contract or epic: a design's name and state line from its block, an epic's slug and state from
// its detail when it is loaded, a contract's id past the slash and the state words its inbox row draws.
private fun thingViewOf(subject: SheetSubject.Thing, record: SheetRecord): SheetView? {
    val link = subject.link
    val up = upLinkOf(link, record)
    val head = when (link.kind) {
        WorkKind.DESIGN -> {
            val design = designBlocksOf(record.conversation, record.designs, record.held, record.contracts)
                .firstOrNull { it.key == link.key }?.design ?: return null
            SheetHead(WorkKind.DESIGN, link.key, null, designNameOf(design), designStateLineOf(design))
        }
        WorkKind.EPIC -> {
            val detail = record.held.details[link.id]
            val epic = detail?.epic ?: record.held.steered.firstOrNull { it.id == link.id }
            SheetHead(
                face = WorkKind.EPIC,
                faceKey = link.key,
                up = up?.name,
                name = epic?.slug ?: link.name,
                state = detail?.let { statePillOf(it, record.now).word }.orEmpty(),
            )
        }
        WorkKind.CONTRACT -> SheetHead(
            face = WorkKind.CONTRACT,
            faceKey = link.key,
            up = up?.name,
            name = link.name,
            state = record.contracts.firstOrNull { it.id == link.id }
                ?.let { epicRowWords(ContractNode(it), record.now).words }.orEmpty(),
        )
    }
    return SheetView(subject, head, up)
}

// The design link of the design a contract or epic belongs to: the link of a design the conversation works, which is not
// a design of one. Null for a design, which has no design above it, and for a contract or epic of none.
private fun upLinkOf(link: WorkLink, record: SheetRecord): WorkLink? {
    val owner = owningDesignOf(link, record) ?: return null
    return record.work.firstOrNull { it.kind == WorkKind.DESIGN && it.id == owner.id }
}

// The design of the conversation, not a design of one, that holds the contract or epic [link] names.
private fun owningDesignOf(link: WorkLink, record: SheetRecord): DesignRow? {
    if (link.kind == WorkKind.DESIGN) return null
    return record.designs.firstOrNull { design ->
        design.conversation == record.conversation && !design.ofOne && design.contracts.any { contract ->
            if (link.kind == WorkKind.EPIC) contract.epic == link.id else contract.id == link.id
        }
    }
}

/**
 * The work link the sheet's [view] lights, as the work links take it: the first of [work] while its own thing is open or
 * the design it names is, [WORK_ALL] while the list is open or the open thing is reached through a later link, and null
 * while no sheet is open.
 */
internal fun workLitOf(view: SheetView?, work: List<WorkLink>): String? {
    if (view == null) return null
    val subject = view.subject
    if (subject == SheetSubject.All) return WORK_ALL
    val reached = work.firstOrNull { it.key == subject.key } ?: view.up
    return when {
        reached == null -> null
        reached.key == work.firstOrNull()?.key -> reached.key
        else -> WORK_ALL
    }
}

/**
 * What a phone sheet's bodies act on: [show] switches the sheet to a subject, [fold] folds it back to the conversation,
 * [live] is whether the conversation is live, and the rest are the screens an epic or contract body opens from the
 * conversation, as MainActivity opens them full-screen from it.
 */
internal class SheetActions(
    val show: (SheetSubject) -> Unit,
    val fold: () -> Unit,
    val live: Boolean,
    val onOpenStep: (epic: String, step: StepRow) -> Unit,
    val onOpenPhase: (epic: String, task: String, phase: Phase) -> Unit,
    val onOpenContract: (id: String, live: Boolean) -> Unit,
    val listedSteerer: (steering: String) -> Pane?,
    val onSteered: (steering: String) -> Unit,
    val onAbout: (RowAbout) -> Unit,
)

/**
 * The body of [subject] under the head: round 8's list for All, which switches the sheet to a kid it taps; a design's
 * block, which does the same; or an epic's or a contract's screen drawn without its title row. Each body is kept by its
 * subject's key, so a switch of subject starts the body afresh.
 */
@Composable
internal fun SheetBody(subject: SheetSubject, record: SheetRecord, actions: SheetActions) {
    key(subject.key) {
        when (subject) {
            SheetSubject.All -> AllBody(record, actions)
            is SheetSubject.Thing -> ThingBody(subject.link, record, actions)
        }
    }
}

@Composable
private fun AllBody(record: SheetRecord, actions: SheetActions) {
    val blocks = designBlocksOf(record.conversation, record.designs, record.held, record.contracts)
    val loose = looseEpicsOf(record.conversation, record.designs, record.held, record.contracts)
    WorkSheetBody(
        designs = blocks,
        loose = loose,
        now = record.now,
        onOpen = { kidKey -> openedKid(blocks.flatMap { it.kids } + loose, kidKey, actions.show) },
    )
}

@Composable
private fun ThingBody(link: WorkLink, record: SheetRecord, actions: SheetActions) {
    when (link.kind) {
        WorkKind.DESIGN -> DesignBody(link, record, actions)
        WorkKind.EPIC -> EpicScreen(
            client = record.client,
            epicId = link.id,
            onBack = actions.fold,
            onOpenStep = { step -> actions.onOpenStep(link.id, step) },
            onOpenContract = { actions.onOpenContract(link.id, false) },
            onOpenPhase = { task, phase -> actions.onOpenPhase(link.id, task, phase) },
            onTalk = { _ -> actions.fold() },
            listedSteerer = actions.listedSteerer,
            onSteered = actions.onSteered,
            now = { record.now },
            titled = false,
        )
        WorkKind.CONTRACT -> SheetContract(link, record, actions)
    }
}

// A design's page, drawn from its read: the record once Designs.Get has answered, else a note of the read. Its questions
// answer through the design stages, and its contract rows open their contracts. A design no longer drawn draws nothing, as
// its sheet is not open (see sheetViewOf).
@Composable
private fun DesignBody(link: WorkLink, record: SheetRecord, actions: SheetActions) {
    val drawn = designBlocksOf(record.conversation, record.designs, record.held, record.contracts).any { it.key == link.key }
    if (!drawn) return
    val stages = record.stages
    LaunchedEffect(link.id) { stages.start(link.id) }
    when (val load = stages.loadOf(link.id)) {
        is DesignLoad.Loaded -> DesignPage(
            record = load.record,
            now = record.now,
            thread = { question -> DesignQuestionThread(stages, link.id, question) },
            onOpenContract = { contract -> actions.show(SheetSubject.Thing(contractOpenedFrom(contract))) },
        )
        DesignLoad.Loading -> InlineNote(LOADING_WORDS, NoteTone.Quiet, Modifier.padding(OrreryTokens.Space.sixteen))
        DesignLoad.Missing -> InlineNote(DESIGN_MISSING, NoteTone.Quiet, Modifier.padding(OrreryTokens.Space.sixteen))
        DesignLoad.Failed -> InlineNote(DESIGN_UNAVAILABLE, NoteTone.Error, Modifier.padding(OrreryTokens.Space.sixteen))
    }
}

// A design question's thread, which answers through the design stages: the owner's words, or the recommendation, sent for
// the question of [design].
@Composable
private fun DesignQuestionThread(stages: DesignStages, design: String, question: DesignQuestionRow) {
    QuestionThread(
        question = question,
        place = ThreadPlace.DESIGN,
        send = stages.sendOf(design, question),
        onAnswer = { words -> stages.answer(design, question, words, accept = false) },
        onAccept = { stages.answer(design, question, words = "", accept = true) },
    )
}

// The link of a contract a design page opens: the contract itself, named as the inbox names a contract, by its id past the
// slash. An accepted contract opens on its work.
private fun contractOpenedFrom(contract: DesignContractRow): WorkLink =
    WorkLink("contract:${contract.id}", WorkKind.CONTRACT, contract.id, contract.id.substringAfter('/'))

/** A contract's read on the sheet: nothing yet, the record with the epic it became once accepted, no contract, or a failure. */
private sealed interface ContractRead {
    data object Loading : ContractRead

    data class Read(val record: ContractRecordRow, val epic: EpicDetail?) : ContractRead

    data object Missing : ContractRead

    data object Failed : ContractRead
}

// A contract's page on the sheet, as the contract opened from its link reads it: the contract as Contracts.Get serves it,
// with the design's questions about it, its accept bar at the foot and, once accepted, its work, which the page opens on.
// The body is tagged as the sheet's contract body has always been, and reads the contract again after each write.
@Composable
private fun SheetContract(link: WorkLink, record: SheetRecord, actions: SheetActions) {
    val client = record.client
    val stages = record.stages
    val id = link.id
    val scope = rememberCoroutineScope()
    var read by remember(id) { mutableStateOf<ContractRead>(ContractRead.Loading) }
    var bar by remember(id) { mutableStateOf<BarSend>(BarSend.Idle) }
    var note by remember(id) { mutableStateOf("") }
    val owner = owningDesignOf(link, record)
    LaunchedEffect(id) { read = readContract(client, id) }
    LaunchedEffect(owner?.id) { owner?.let { stages.start(it.id) } }

    // reload reads the contract again, once a write has changed it.
    suspend fun reload() {
        read = readContract(client, id)
    }

    // writeRow makes one keep or strike of an addition: a landed write reads the contract again, and a refusal or a
    // failure is the note under the page.
    fun writeRow(call: () -> ContractCallResult) {
        scope.launch {
            note = try {
                when (val result = withContext(Dispatchers.IO) { call() }) {
                    is ContractCallResult.Refused -> failureWordsOf(result.reason)
                    is ContractCallResult.Done -> {
                        reload()
                        ""
                    }
                }
            } catch (error: IOException) {
                failureWordsOf(error.message)
            }
        }
    }

    // settle makes one accept, set aside or reopen, which the bar shows while it is sent: a landed one reads the contract
    // again, and a refusal or a failure is the bar's reason.
    fun settle(call: () -> ContractCallResult) {
        if (bar == BarSend.Sending) return
        bar = BarSend.Sending
        scope.launch {
            bar = try {
                when (val result = withContext(Dispatchers.IO) { call() }) {
                    is ContractCallResult.Refused -> BarSend.Failed(failureWordsOf(result.reason))
                    is ContractCallResult.Done -> {
                        reload()
                        BarSend.Idle
                    }
                }
            } catch (error: IOException) {
                BarSend.Failed(failureWordsOf(error.message))
            }
        }
    }

    // shown tells the server the owner was shown rows of a version. A mark that landed reads the contract again, so its
    // unread rows drop from the page.
    fun shown(version: Long, rows: List<String>) {
        scope.launch {
            try {
                if (withContext(Dispatchers.IO) { client.markShown(id, version, rows) }.isNotEmpty()) reload()
            } catch (error: IOException) {
                note = failureWordsOf(error.message)
            }
        }
    }

    val aboutAndFold: (RowAbout) -> Unit = { about ->
        actions.onAbout(about)
        actions.fold()
    }
    val openEpic: () -> Unit = {
        val epic = (read as? ContractRead.Read)?.epic
        actions.show(SheetSubject.Thing(WorkLink("epic:$id", WorkKind.EPIC, id, epic?.epic?.slug ?: link.name)))
    }
    Column(modifier = Modifier.fillMaxSize().testTag(SHEET_CONTRACT_TAG)) {
        when (val current = read) {
            ContractRead.Loading -> InlineNote(LOADING_WORDS, NoteTone.Quiet, Modifier.padding(OrreryTokens.Space.sixteen))
            ContractRead.Missing -> InlineNote(CONTRACT_MISSING, NoteTone.Quiet, Modifier.padding(OrreryTokens.Space.sixteen))
            ContractRead.Failed -> InlineNote(CONTRACT_UNAVAILABLE, NoteTone.Error, Modifier.padding(OrreryTokens.Space.sixteen))
            is ContractRead.Read -> {
                val contract = current.record
                val epic = current.epic
                val work: (@Composable () -> Unit)? = if (contract.contract.state == "accepted" && epic != null) {
                    { SheetContractWork(contract.stage.work, epic, openEpic) }
                } else {
                    null
                }
                ContractPage(
                    record = contract,
                    stage = contract.stage,
                    questions = owner?.let { questionsOf(stages.loadOf(it.id), id) }.orEmpty(),
                    onKeep = { addition -> writeRow { client.keepAddition(id, addition) } },
                    onStrike = { addition -> writeRow { client.strikeAddition(id, addition) } },
                    onShown = { version, rows -> shown(version, rows) },
                    onAbout = if (actions.live) aboutAndFold else null,
                    thread = { question -> owner?.let { DesignQuestionThread(stages, it.id, question) } },
                    work = work,
                    foot = {
                        ContractAcceptBar(
                            contract = contract.contract,
                            left = contract.stage.left,
                            states = contract.states,
                            send = bar,
                            onAccept = { settle { client.acceptContract(id) } },
                            onSetAside = { words -> settle { client.setAsideContract(id, words) } },
                            onReopen = { settle { client.reopenContract(id) } },
                            onTalk = if (actions.live) actions.fold else null,
                            onOpenEpic = openEpic,
                        )
                        if (note.isNotEmpty()) InlineNote(note, NoteTone.Error, Modifier.testTag(SHEET_CONTRACT_NOTE_TAG))
                    },
                    modifier = Modifier.weight(1f),
                )
            }
        }
    }
}

// The questions a design's stage asked about the contract [id], once the design is read: none while it is not.
private fun questionsOf(load: DesignLoad, id: String): List<DesignQuestionRow> =
    (load as? DesignLoad.Loaded)?.record?.stage?.questions.orEmpty().filter { it.contract == id }

// readContract reads the contract [id] off the main thread: its record, and its epic once it is accepted, the epic it became.
private suspend fun readContract(client: OrreryClient, id: String): ContractRead = try {
    withContext(Dispatchers.IO) {
        val record = client.contract(id) ?: return@withContext ContractRead.Missing
        val epic = if (record.contract.state == "accepted") client.epic(id) else null
        ContractRead.Read(record, epic)
    }
} catch (_: IOException) {
    ContractRead.Failed
}

// The work of an accepted contract, as its page opens on it: each requirement's state, tasks and proof, the proof read from
// the runs of the epic the contract became. The page is first drawn at the work, so it is brought into view once.
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun SheetContractWork(work: List<ContractRequirementWorkRow>, epic: EpicDetail, onOpenEpic: () -> Unit) {
    val requester = remember { BringIntoViewRequester() }
    LaunchedEffect(Unit) { requester.bringIntoView() }
    ContractWork(
        work = work,
        runs = epic.requirements.associate { it.id to it.runs },
        epic = epic.epic.slug,
        onOpenEpic = onOpenEpic,
        modifier = Modifier.bringIntoViewRequester(requester),
    )
}

// failureWordsOf is the words a failed contract call shows: its own message, or that the contract is unavailable without one.
private fun failureWordsOf(message: String?): String = if (message.isNullOrEmpty()) CONTRACT_UNAVAILABLE else message

/** The tag of a contract's body on the sheet, which the sheet's contract body has always carried. */
private const val SHEET_CONTRACT_TAG = "contract-screen"

/** The tag of the note under a contract's page on the sheet, which says why a keep, strike or mark did not land. */
private const val SHEET_CONTRACT_NOTE_TAG = "contract-note"

private const val LOADING_WORDS = "Loading..."
private const val DESIGN_MISSING = "design not found"
private const val DESIGN_UNAVAILABLE = "design unavailable"
private const val CONTRACT_MISSING = "contract not found"
private const val CONTRACT_UNAVAILABLE = "contract unavailable"

// Switches the sheet to the kid [kidKey] among [hungs], an epic or a contract, as its link.
private fun openedKid(hungs: List<Hung>, kidKey: String, show: (SheetSubject) -> Unit) {
    hungs.firstOrNull { it.key == kidKey }?.let { show(SheetSubject.Thing(linkOfKid(it))) }
}

// A kid as the work link it opens: an epic's, or a contract's, named as the work links name them.
private fun linkOfKid(hung: Hung): WorkLink = when (hung) {
    is EpicNode -> WorkLink(hung.key, WorkKind.EPIC, hung.epic.id, hung.epic.slug)
    is ContractNode -> WorkLink(hung.key, WorkKind.CONTRACT, hung.contract.id, hung.contract.id.substringAfter('/'))
}
