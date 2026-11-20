package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.CoroutineScope

private const val NO_ANSWER = "The server did not answer, so the move may have gone through. Reading the epic again…"
private const val CONTINUE_LABEL = "Continue in a new conversation"
private const val STEER_LABEL = "Steer from another…"
private const val NEXT_CLAUSE = "Continue it in a new conversation, which starts with the contract and the epic’s log, " +
    "or move it to one that is running."

/** The lead of an epic whose steering conversation has ended, and the epic still runs. */
fun endedLead(slug: String): String =
    "This conversation has ended, and $slug still runs. $NEXT_CLAUSE"

/** The lead of an epic no conversation steers. */
fun unsteeredLead(slug: String): String = "No conversation steers $slug. $NEXT_CLAUSE"

/**
 * The bar that stands in for the composer when the conversation that steered [epic] has ended: [lead] says so, then
 * two full-width buttons. Continue in a new conversation sends one Epics.Continue, and Steer from another opens the
 * steering menu, a sheet. Both go through one [SteerSend], so one call is out at a time, and [onSteered] hears each
 * steering the epic settles on. A refused or unanswered move shows its note in the bar while the menu is shut. Its calls
 * run in [scope]: a host that drops the bar once the epic moves passes its own, so the re-read still reaches [onSteered].
 */
@Composable
fun EndedBar(
    client: OrreryClient,
    epic: EpicRow,
    repository: String,
    lead: String,
    onSteered: (String) -> Unit,
    modifier: Modifier = Modifier,
    scope: CoroutineScope = rememberCoroutineScope(),
) {
    val send = rememberSteerSend(client, epic.id, scope, onSteered)
    var menuOpen by remember { mutableStateOf(false) }
    val busy = send.state == SteerState.Sending || send.state == SteerState.Unanswered
    val style = OrreryTokens.Type.small
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight)
            .testTag("ended-bar"),
    ) {
        Text(lead, style = style, color = OrreryTokens.Ink.one)
        Button(
            onClick = { send.choose(null) },
            enabled = !busy,
            modifier = Modifier.fillMaxWidth().padding(top = OrreryTokens.Space.eight).testTag("ended-continue"),
            shape = RoundedCornerShape(OrreryTokens.Radius.two),
            colors = ButtonDefaults.buttonColors(
                containerColor = OrreryTokens.Surface.raised,
                contentColor = OrreryTokens.Ink.one,
            ),
            border = BorderStroke(OrreryTokens.Size.hairline, OrreryTokens.Line.firm),
            elevation = null,
        ) {
            Text(CONTINUE_LABEL, style = style)
        }
        Button(
            onClick = { menuOpen = true },
            enabled = !busy,
            modifier = Modifier.fillMaxWidth().padding(top = OrreryTokens.Space.eight).testTag("ended-steer"),
            shape = RoundedCornerShape(OrreryTokens.Radius.two),
            colors = ButtonDefaults.buttonColors(
                containerColor = OrreryTokens.Surface.raised,
                contentColor = OrreryTokens.Ink.one,
            ),
            border = BorderStroke(OrreryTokens.Size.hairline, OrreryTokens.Line.firm),
            elevation = null,
        ) {
            Text(STEER_LABEL, style = style)
        }
        if (!menuOpen) {
            when (val state = send.state) {
                is SteerState.Refused -> BarNote(state.message)
                SteerState.Unanswered -> BarNote(NO_ANSWER)
                SteerState.Ready, SteerState.Sending -> Unit
            }
        }
    }
    if (menuOpen) {
        SteerMenuSheet(
            client = client,
            epicId = epic.id,
            slug = epic.slug,
            repository = repository,
            steering = epic.steering,
            send = send,
            onDismiss = { menuOpen = false },
        )
    }
}

@Composable
private fun BarNote(message: String) {
    Text(
        text = message,
        style = OrreryTokens.Type.small,
        color = OrreryTokens.Role.bad,
        modifier = Modifier.padding(top = OrreryTokens.Space.eight).testTag("ended-note"),
    )
}
