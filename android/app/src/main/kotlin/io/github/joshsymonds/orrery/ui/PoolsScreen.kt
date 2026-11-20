package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.Hold
import io.github.joshsymonds.orrery.api.Holder
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pool
import io.github.joshsymonds.orrery.api.PoolMember
import io.github.joshsymonds.orrery.api.PoolsView
import io.github.joshsymonds.orrery.api.Waiter
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private sealed interface PoolsLoad {
    data object Loading : PoolsLoad
    data object Failed : PoolsLoad
    data class Loaded(val view: PoolsView) : PoolsLoad
}

/** The member states a chip shows in the Live tone: the ones a member is up or on its way up in. */
private val POOL_LIVE_STATES = setOf("awake", "waking", "admitting", "admitted")

/**
 * The pools in use, each member with its state, its holder and its hold, the requests waiting for each pool, and the
 * holds on members of no pool in use. Each member row has a Hold action, or a Free action when the member is held.
 * An action reloads the view; a failed action shows a message and keeps the screen.
 */
@Composable
fun PoolsScreen(
    client: OrreryClient,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    now: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    var load by remember { mutableStateOf<PoolsLoad>(PoolsLoad.Loading) }
    var generation by remember { mutableIntStateOf(0) }
    var notice by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()

    LaunchedEffect(client, generation) {
        load = PoolsLoad.Loading
        load = try {
            PoolsLoad.Loaded(withContext(Dispatchers.IO) { client.pools() })
        } catch (error: IOException) {
            PoolsLoad.Failed
        }
    }

    fun act(member: String, freeing: Boolean) {
        scope.launch {
            notice = try {
                withContext(Dispatchers.IO) {
                    if (freeing) client.freeMember(member) else client.holdMember(member)
                }
                null
            } catch (error: IOException) {
                "Could not ${if (freeing) "free" else "hold"} $member."
            }
            generation++
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        TitleRow(crumbs = listOf(Crumb("Pools")), onBack = onBack)
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve),
        ) {
            notice?.let { InlineNote(it, NoteTone.Error) }
            when (val current = load) {
                PoolsLoad.Loading -> Text("Loading...")
                PoolsLoad.Failed -> Column {
                    InlineNote("Could not load the pools.", NoteTone.Error)
                    TextButton(onClick = { generation++ }) {
                        Text("Retry")
                    }
                }
                is PoolsLoad.Loaded -> PoolsContent(current.view, now(), ::act)
            }
        }
    }
}

@Composable
private fun PoolsContent(view: PoolsView, now: Long, onAct: (String, Boolean) -> Unit) {
    val pooled = view.pools.flatMap { pool -> pool.members.map { it.name } }.toSet()
    val outside = view.holds.filter { it.member !in pooled }
    if (view.pools.isEmpty() && outside.isEmpty()) {
        Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four)) {
            Text("No pools are in use.", style = OrreryTokens.Type.body)
            Text(
                "A pool shows here when a step asks for a machine from it.",
                color = OrreryTokens.Ink.three,
                style = OrreryTokens.Type.small,
            )
        }
    }
    view.pools.forEach { pool -> PoolCard(pool, now, onAct) }
    if (outside.isNotEmpty()) {
        OutsideHolds(outside, now, onAct)
    }
}

@Composable
private fun PoolCard(pool: Pool, now: Long, onAct: (String, Boolean) -> Unit) {
    Column(
        modifier = Modifier.fillMaxWidth().testTag("pool:${pool.name}"),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        SectionHead(pool.name, count = poolCount(pool))
        pool.members.forEach { member -> MemberRow(member, now, onAct) }
        pool.waiters.forEach { waiter -> WaiterRow(pool.name, waiter, now) }
    }
}

/** A pool's member count, then its waiting count when anything waits: "2 members · 1 waiting", "1 member". */
private fun poolCount(pool: Pool): String {
    val members = if (pool.members.size == 1) "1 member" else "${pool.members.size} members"
    return if (pool.waiters.isEmpty()) members else "$members · ${pool.waiters.size} waiting"
}

@Composable
private fun MemberRow(member: PoolMember, now: Long, onAct: (String, Boolean) -> Unit) {
    val held = member.heldSince != 0L
    ListItem(
        title = member.name,
        modifier = Modifier.testTag("member:${member.name}"),
        supporting = memberLine(member, now),
        titleExtra = { Chip(member.state, memberTone(member)) },
        end = {
            TextButton(
                onClick = { onAct(member.name, held) },
                modifier = Modifier.testTag("member-action:${member.name}"),
            ) {
                Text(if (held) "Free" else "Hold")
            }
        },
    )
}

/** A member's grant and hold, one line each: "granted to <dir> · <command> · <age>" and "held by you · <age>". */
private fun memberLine(member: PoolMember, now: Long): String {
    val lines = listOfNotNull(
        member.holder?.let { grantLine(it, now) },
        if (member.heldSince != 0L) "held by you · ${ageOf(now - member.heldSince)}" else null,
    )
    return lines.joinToString("\n").ifEmpty { "free" }
}

private fun grantLine(holder: Holder, now: Long): String = joinDot(
    "granted to ${dirOf(holder.project)}",
    holder.command.joinToString(" "),
    ageOf(now - holder.since),
    if (holder.stopping) "stopping" else "",
)

/** A member's chip tone: Attention when held by you, else by its state. */
private fun memberTone(member: PoolMember): ChipTone = when {
    member.heldSince != 0L -> ChipTone.Attention
    member.state in POOL_LIVE_STATES -> ChipTone.Live
    member.state == "unavailable" -> ChipTone.Bad
    else -> ChipTone.Neutral
}

@Composable
private fun WaiterRow(poolName: String, waiter: Waiter, now: Long) {
    ListItem(
        title = "#${waiter.position} ${dirOf(waiter.project)}",
        modifier = Modifier.testTag("waiter:$poolName:${waiter.position}"),
        supporting = joinDot(waiter.command.joinToString(" "), "needs ${waiter.count}"),
        end = { Text("waiting ${ageOf(now - waiter.since)}", style = OrreryTokens.Type.small) },
    )
}

@Composable
private fun OutsideHolds(holds: List<Hold>, now: Long, onAct: (String, Boolean) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        SectionHead("Holds outside any pool")
        holds.forEach { hold ->
            ListItem(
                title = hold.member,
                modifier = Modifier.testTag("outside-hold:${hold.member}"),
                supporting = "held by you · ${ageOf(now - hold.since)}",
                end = {
                    TextButton(
                        onClick = { onAct(hold.member, true) },
                        modifier = Modifier.testTag("outside-hold-action:${hold.member}"),
                    ) {
                        Text("Free")
                    }
                },
            )
        }
    }
}

/** The last element of a project path: the directory a grant or a waiter names. */
private fun dirOf(project: String): String = project.substringAfterLast('/')

/** The parts that are not empty, joined by a middle dot. */
private fun joinDot(vararg parts: String): String = parts.filter { it.isNotEmpty() }.joinToString(" · ")

private fun ageOf(seconds: Long): String = when {
    seconds < 60 -> "${seconds}s"
    seconds < 3_600 -> "${seconds / 60}m"
    seconds < 86_400 -> "${seconds / 3_600}h"
    else -> "${seconds / 86_400}d"
}
