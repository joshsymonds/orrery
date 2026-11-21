package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** Semantics key: the tone of a release step's state cell, on its text node. */
val ReleaseToneKey = SemanticsPropertyKey<ReleaseTone>("ReleaseTone")

// The tag of the Release from step N control, and the stem of the name it asks under.
private const val RELEASE_FROM = "release-from"

/** The name the Release from step [position] control asks under in its owner call: the step it names. */
internal fun releaseAsk(position: Long): String = "$RELEASE_FROM:$position"

/**
 * The Release section: its heading with its count words, one row per release step with the reason, evidence and runs
 * under it, the control of the offered step, then the Then line.
 */
@Composable
internal fun EpicReleaseSection(view: ReleaseView, call: OwnerCall, onRelease: (Long) -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        SectionHeading("Release")
        Text(
            view.words,
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.three,
            modifier = Modifier.testTag("release-words"),
        )
    }
    view.rows.forEach { line ->
        ReleaseStepRow(line = line, offered = line.position == view.offer, call = call, onRelease = onRelease)
    }
    if (view.then.isNotEmpty()) {
        Text(
            view.then,
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.three,
            modifier = Modifier.testTag("release-then").padding(horizontal = OrreryTokens.Space.sixteen),
        )
    }
}

@Composable
private fun ReleaseStepRow(line: ReleaseLine, offered: Boolean, call: OwnerCall, onRelease: (Long) -> Unit) {
    val position = line.position
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("release-step:$position")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text("$position", style = OrreryTokens.Type.body)
            Column(modifier = Modifier.weight(1f)) {
                Text(stepText(line), style = OrreryTokens.Type.small)
                Text(line.undo, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
            }
            Chip(
                line.state,
                chipToneOf(line.tone),
                modifier = Modifier.testTag("release-state:$position").semantics { set(ReleaseToneKey, line.tone) },
            )
        }
        if (line.reason.isNotEmpty()) {
            Text(
                line.reason,
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Role.bad,
                modifier = Modifier.testTag("release-reason:$position"),
            )
        }
        if (line.evidence.isNotEmpty()) {
            Text(
                line.evidence,
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
                modifier = Modifier.testTag("release-evidence:$position"),
            )
        }
        line.runs.forEachIndexed { index, run ->
            Text(
                run,
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
                modifier = Modifier.testTag("release-run:$position:$index"),
            )
        }
        if (offered) {
            ReleaseFromControl(line = line, call = call, onRelease = onRelease)
        }
    }
}

/**
 * The Release from step N control, its call line under it, and the confirm sheet while the owner is asked. Only the
 * confirm sends, once, through [onRelease].
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ReleaseFromControl(line: ReleaseLine, call: OwnerCall, onRelease: (Long) -> Unit) {
    val position = line.position
    TextButton(
        onClick = { call.ask(releaseAsk(position)) },
        enabled = !call.disabled,
        modifier = Modifier.testTag(RELEASE_FROM),
    ) {
        Text("Release from step $position")
    }
    if (call.line.isNotEmpty()) {
        Text(
            call.line,
            style = OrreryTokens.Type.small,
            color = if (call.refused) OrreryTokens.Role.bad else OrreryTokens.Ink.three,
            modifier = Modifier
                .padding(horizontal = OrreryTokens.Space.sixteen)
                .testTag("release-call-line"),
        )
    }
    if (call.asking == releaseAsk(position)) {
        ModalBottomSheet(onDismissRequest = { call.keep() }) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            ) {
                Text(
                    "Release from step $position?",
                    style = OrreryTokens.Type.small,
                    modifier = Modifier.testTag("release-from-title"),
                )
                Text(
                    "Step $position, ${line.step}, cannot be undone.",
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Ink.three,
                    modifier = Modifier.testTag("release-from-line"),
                )
                TextButton(
                    onClick = { call.keep() },
                    modifier = Modifier.fillMaxWidth().testTag("release-from-keep"),
                ) {
                    Text("Keep it stopped")
                }
                TextButton(
                    onClick = {
                        if (call.send()) onRelease(position)
                    },
                    modifier = Modifier.fillMaxWidth().testTag("release-from-confirm"),
                ) {
                    Text("Release from step $position", color = OrreryTokens.Role.bad)
                }
            }
        }
    }
}

// stepText is the step's text, then its target when it has one.
private fun stepText(line: ReleaseLine): String =
    if (line.target.isEmpty()) line.step else "${line.step} → ${line.target}"

private fun chipToneOf(tone: ReleaseTone): ChipTone = when (tone) {
    ReleaseTone.None -> ChipTone.Neutral
    ReleaseTone.Live -> ChipTone.Live
    ReleaseTone.Ok -> ChipTone.Ok
    ReleaseTone.Bad -> ChipTone.Bad
}
