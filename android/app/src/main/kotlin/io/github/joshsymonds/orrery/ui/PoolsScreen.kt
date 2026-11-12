package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.Hold
import io.github.joshsymonds.orrery.api.Holder
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pool
import io.github.joshsymonds.orrery.api.PoolMember
import io.github.joshsymonds.orrery.api.PoolsView
import io.github.joshsymonds.orrery.api.Waiter
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private sealed interface PoolsLoad {
    data object Loading : PoolsLoad
    data object Failed : PoolsLoad
    data class Loaded(val view: PoolsView) : PoolsLoad
}

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
                "could not ${if (freeing) "free" else "hold"} $member"
            }
            generation++
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TextButton(onClick = onBack) {
                Text("Back")
            }
            Text("Pools", style = MaterialTheme.typography.titleLarge)
        }
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            notice?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            when (val current = load) {
                PoolsLoad.Loading -> Text("Loading...")
                PoolsLoad.Failed -> Column {
                    Text("could not load pools", color = MaterialTheme.colorScheme.error)
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
        Text("No pools in use")
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
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(pool.name, style = MaterialTheme.typography.titleSmall)
        pool.members.forEach { member -> MemberRow(member, now, onAct) }
        if (pool.waiters.isNotEmpty()) {
            Text("waiting", style = MaterialTheme.typography.labelMedium)
            pool.waiters.forEach { waiter -> WaiterRow(pool.name, waiter) }
        }
    }
}

@Composable
private fun MemberRow(member: PoolMember, now: Long, onAct: (String, Boolean) -> Unit) {
    val holder = member.holder
    val held = member.heldSince != 0L
    Row(
        modifier = Modifier.fillMaxWidth().testTag("member:${member.name}"),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(member.name, style = MaterialTheme.typography.bodyLarge)
            Text(member.state, style = MaterialTheme.typography.labelSmall)
            if (holder != null) {
                GrantLines(holder, now)
            }
            if (held) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("held", style = MaterialTheme.typography.labelMedium)
                    Text(ageOf(now - member.heldSince), style = MaterialTheme.typography.labelSmall)
                }
            }
            if (holder == null && !held) {
                Text("free", style = MaterialTheme.typography.labelMedium)
            }
        }
        TextButton(
            onClick = { onAct(member.name, held) },
            modifier = Modifier.testTag("member-action:${member.name}"),
        ) {
            Text(if (held) "Free" else "Hold")
        }
    }
}

@Composable
private fun GrantLines(holder: Holder, now: Long) {
    Text("granted", style = MaterialTheme.typography.labelMedium)
    Text(holder.project, style = MaterialTheme.typography.labelSmall)
    if (holder.command.isNotEmpty()) {
        Text(holder.command.joinToString(" "), style = MaterialTheme.typography.labelSmall)
    }
    Text(ageOf(now - holder.since), style = MaterialTheme.typography.labelSmall)
    if (holder.stopping) {
        Text("stopping", style = MaterialTheme.typography.labelSmall)
    }
}

@Composable
private fun WaiterRow(poolName: String, waiter: Waiter) {
    Column(modifier = Modifier.fillMaxWidth().testTag("waiter:$poolName:${waiter.position}")) {
        Text("#${waiter.position}", style = MaterialTheme.typography.labelMedium)
        Text(waiter.project, style = MaterialTheme.typography.labelSmall)
        if (waiter.command.isNotEmpty()) {
            Text(waiter.command.joinToString(" "), style = MaterialTheme.typography.labelSmall)
        }
        Text("needs ${waiter.count}", style = MaterialTheme.typography.labelSmall)
    }
}

@Composable
private fun OutsideHolds(holds: List<Hold>, now: Long, onAct: (String, Boolean) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("Holds outside any pool", style = MaterialTheme.typography.titleSmall)
        holds.forEach { hold ->
            Row(
                modifier = Modifier.fillMaxWidth().testTag("outside-hold:${hold.member}"),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(hold.member, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
                Text(ageOf(now - hold.since), style = MaterialTheme.typography.labelSmall)
                TextButton(
                    onClick = { onAct(hold.member, true) },
                    modifier = Modifier.testTag("outside-hold-action:${hold.member}"),
                ) {
                    Text("Free")
                }
            }
        }
    }
}

private fun ageOf(seconds: Long): String = when {
    seconds < 60 -> "${seconds}s"
    seconds < 3_600 -> "${seconds / 60}m"
    seconds < 86_400 -> "${seconds / 3_600}h"
    else -> "${seconds / 86_400}d"
}
