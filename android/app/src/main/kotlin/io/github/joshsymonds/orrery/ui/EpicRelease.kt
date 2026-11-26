package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** Semantics key: the tone of a release step's state cell, on its text node. */
val ReleaseToneKey = SemanticsPropertyKey<ReleaseTone>("ReleaseTone")

/**
 * The Release section: its heading with its count words, one row per release step with the reason, evidence and runs
 * under it, then the Then line.
 */
@Composable
internal fun EpicReleaseSection(view: ReleaseView) {
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
        ReleaseStepRow(line = line)
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
private fun ReleaseStepRow(line: ReleaseLine) {
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
