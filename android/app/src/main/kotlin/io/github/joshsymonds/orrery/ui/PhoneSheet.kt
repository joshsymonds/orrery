package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.SemanticsPropertyReceiver
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
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

/**
 * The phone sheet round 8 draws over the chat on touch: nothing for a null [subject], otherwise the sheet's column,
 * tagged [PHONE_SHEET_TAG] and carrying its pose at rest, with the [head] row and then the [body] of the subject. The
 * head's name is drawn under [SHEET_NAME_TAG] and the body is tagged with its subject's key. [animated], [onClose] and
 * [onUp] are the motion and the controls the next tasks draw behind this signature.
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
    Column(
        modifier = modifier
            .testTag(PHONE_SHEET_TAG)
            .semantics { sheetPose = SheetPose.REST },
    ) {
        Row(modifier = Modifier.testTag(SHEET_HEAD_TAG)) {
            Text(head.name, modifier = Modifier.testTag(SHEET_NAME_TAG))
        }
        Box(
            modifier = Modifier
                .testTag(SHEET_BODY_TAG_PREFIX + subject.key)
                .semantics { sheetFade = 1f },
        ) {
            body(subject)
        }
    }
}
