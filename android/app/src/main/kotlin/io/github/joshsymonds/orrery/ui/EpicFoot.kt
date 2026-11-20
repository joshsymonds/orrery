package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.OrreryClient
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * The foot of an epic's screen. An epic orreryd runs that no conversation steers gets the ended bar with its unsteered
 * lead. An epic whose steering conversation has stopped, failed or is no longer listed gets the ended bar with its
 * ended lead, once the conversations are read. Otherwise, and until that read answers or fails, the foot is the talk
 * bar that opens the steering conversation. [onSteered] hears each steering a continue or a move settles on.
 */
@Composable
fun EpicFoot(
    client: OrreryClient,
    detail: EpicDetail,
    onOpenSteerer: () -> Unit,
    onSteered: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val epic = detail.epic
    when {
        !steersLive(epic) -> EpicTalkBar(epic.steering, onOpenSteerer, modifier)
        epic.steering.isEmpty() -> EndedBar(
            client = client,
            epic = epic,
            repository = detail.repository,
            lead = unsteeredLead(epic.slug),
            onSteered = onSteered,
            modifier = modifier,
        )
        else -> SteeredFoot(client, detail, onOpenSteerer, onSteered, modifier)
    }
}

/** The foot of a live epic with a steerer: the ended bar once that steerer is read as ended, the talk bar otherwise. */
@Composable
private fun SteeredFoot(
    client: OrreryClient,
    detail: EpicDetail,
    onOpenSteerer: () -> Unit,
    onSteered: (String) -> Unit,
    modifier: Modifier,
) {
    val epic = detail.epic
    var ended by remember(client, epic.steering) { mutableStateOf<Boolean?>(null) }
    LaunchedEffect(client, epic.steering) {
        ended = try {
            withContext(Dispatchers.IO) { conversationEnded(client.panes() + client.headless(), epic.steering) }
        } catch (error: IOException) {
            null
        }
    }
    if (ended == true) {
        EndedBar(
            client = client,
            epic = epic,
            repository = detail.repository,
            lead = endedLead(epic.slug),
            onSteered = onSteered,
            modifier = modifier,
        )
    } else {
        EpicTalkBar(epic.steering, onOpenSteerer, modifier)
    }
}
