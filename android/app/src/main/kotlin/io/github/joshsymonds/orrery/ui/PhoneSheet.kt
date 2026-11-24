package io.github.joshsymonds.orrery.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.key
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.SemanticsPropertyReceiver
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.DesignRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

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
 * The phone sheet round 8 draws over the chat on touch: nothing for a null [subject], otherwise the bracket that begins at
 * the lit name in the title. Under the head, the band of the raised ground runs the full width, [OrreryTokens.Space.eight]
 * tall; below it, the sheet is the column on the side's ground, rounded at its left corners and tagged [PHONE_SHEET_TAG],
 * with the bracket's spine down its left side and its foot under it, each [OrreryTokens.Space.eight] across. The sheet
 * carries its pose at rest, and its head row holds the face, the kind word, the [SheetHead.up] link when [onUp] is given,
 * the name, and the close control at its right end, over the state line when the head has one. The [body] of the subject
 * fills the rest of the sheet. The close control and the back gesture call [onClose] while the sheet is open.
 * [animated] is the motion the next task draws behind this signature.
 */
@Composable
internal fun PhoneSheet(
    subject: SheetSubject?,
    head: SheetHead,
    animated: Boolean,
    onClose: () -> Unit,
    onUp: (() -> Unit)?,
    modifier: Modifier = Modifier,
    body: @Composable (SheetSubject) -> Unit,
) {
    if (subject == null) return
    BackHandler(onBack = onClose)
    Column(modifier = modifier) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(OrreryTokens.Space.eight)
                .background(OrreryTokens.Surface.raised)
                .testTag(SHEET_BAND_TAG)
                .semantics { sheetFade = 1f },
        ) {}
        Row(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                .background(OrreryTokens.Surface.raised),
        ) {
            Box(
                modifier = Modifier
                    .fillMaxHeight()
                    .width(OrreryTokens.Space.eight)
                    .background(OrreryTokens.Surface.raised)
                    .testTag(SHEET_SPINE_TAG),
            ) {}
            Column(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxHeight()
                    .background(
                        OrreryTokens.Surface.side,
                        RoundedCornerShape(topStart = OrreryTokens.Radius.three, bottomStart = OrreryTokens.Radius.three),
                    )
                    .testTag(PHONE_SHEET_TAG)
                    .semantics { sheetPose = SheetPose.REST },
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
                        if (up != null && onUp != null) {
                            Text(
                                up,
                                modifier = Modifier.testTag(SHEET_UP_TAG).clickable(onClick = onUp),
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
                Box(
                    modifier = Modifier
                        .weight(1f)
                        .fillMaxWidth()
                        .testTag(SHEET_BODY_TAG_PREFIX + subject.key)
                        .semantics { sheetFade = 1f },
                ) {
                    body(subject)
                }
            }
        }
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(OrreryTokens.Space.eight)
                .background(OrreryTokens.Surface.raised)
                .testTag(SHEET_FOOT_TAG),
        ) {}
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
 * [contracts] listed.
 */
internal class SheetRecord(
    val client: OrreryClient,
    val conversation: String,
    val work: List<WorkLink>,
    val designs: List<DesignRow>,
    val held: HeadEpics,
    val contracts: List<ContractRow>,
    val now: Long,
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
        WorkKind.CONTRACT -> {
            val aboutAndFold: (RowAbout) -> Unit = { about ->
                actions.onAbout(about)
                actions.fold()
            }
            ContractScreen(
                client = record.client,
                epicId = link.id,
                onBack = actions.fold,
                onHome = actions.fold,
                live = actions.live,
                onAbout = if (actions.live) aboutAndFold else null,
                onTalk = if (actions.live) actions.fold else null,
                titled = false,
            )
        }
    }
}

// A design's block alone, scrolled when it is taller than the sheet. A design no longer drawn draws nothing, as its
// sheet is not open (see sheetViewOf).
@Composable
private fun DesignBody(link: WorkLink, record: SheetRecord, actions: SheetActions) {
    val block = designBlocksOf(record.conversation, record.designs, record.held, record.contracts)
        .firstOrNull { it.key == link.key } ?: return
    Column(modifier = Modifier.fillMaxWidth().verticalScroll(rememberScrollState())) {
        DesignBlockItem(
            block,
            record.now,
            onOpen = { kidKey -> openedKid(block.kids, kidKey, actions.show) },
            onLongPress = {},
        )
    }
}

// Switches the sheet to the kid [kidKey] among [hungs], an epic or a contract, as its link.
private fun openedKid(hungs: List<Hung>, kidKey: String, show: (SheetSubject) -> Unit) {
    hungs.firstOrNull { it.key == kidKey }?.let { show(SheetSubject.Thing(linkOfKid(it))) }
}

// A kid as the work link it opens: an epic's, or a contract's, named as the work links name them.
private fun linkOfKid(hung: Hung): WorkLink = when (hung) {
    is EpicNode -> WorkLink(hung.key, WorkKind.EPIC, hung.epic.id, hung.epic.slug)
    is ContractNode -> WorkLink(hung.key, WorkKind.CONTRACT, hung.contract.id, hung.contract.id.substringAfter('/'))
}
