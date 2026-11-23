package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
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
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

private const val EPIC_KEY_PREFIX = "epic:"
private const val CONTRACT_KEY_PREFIX = "contract:"
private const val STATE_WAITING = "waiting"

/**
 * One project of the inbox tree as a page: its name in the title row with Back, its path and host, its counts, then its
 * Conversations, each with its state, title, what it last said or asks, and age, and under each the epics and contracts
 * it steers as WorkCards, with its ended epics folded under a line. The ended line unfolds the ended conversations,
 * shared with the inbox's own ended line. A foot bar starts a session in the project's directory, and is absent when the
 * project has none.
 *
 * The page shows the tree [state] holds at once, reads the tree once at launch and keeps the answer in [state]. A tap on
 * a conversation opens its pane, and a WorkCard opens what the inbox opens for its item key: an epic through
 * [onOpenEpic], a contract through [onOpenContract] with the conversation it hangs under.
 */
@Composable
fun ProjectPageScreen(
    client: OrreryClient,
    projectKey: String,
    state: PaneListState,
    onOpenPane: (Pane) -> Unit,
    onOpenEpic: (String) -> Unit,
    onNewHere: (String) -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    onOpenContract: (id: String, conversation: Pane?) -> Unit = { _, _ -> },
    now: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    var data by remember(client) { mutableStateOf(state.held) }
    var failed by remember(client) { mutableStateOf(false) }

    LaunchedEffect(client) {
        val read = readTree(client, state.held)
        if (read == null) {
            failed = true
        } else {
            data = read
            state.held = read
            failed = false
        }
    }

    val loaded = data
    val projects = remember(loaded, state.view) {
        loaded?.let { inboxTree(it.panes, it.archived, it.epics, it.details, it.contracts, it.host, state.view) }.orEmpty()
    }
    val project = projects.firstOrNull { it.key == projectKey }
    val paneOf = remember(projects) {
        projects.flatMap { it.convos + it.ended }.mapNotNull { convo -> convo.pane?.let { convo.key to it } }.toMap()
    }
    val conversationOfContract = remember(projects) {
        projects.flatMap { it.convos + it.ended }.flatMap { convo ->
            convo.hung.filterIsInstance<ContractNode>().map { it.key to convo.pane }
        }.toMap()
    }
    val onOpen: (String) -> Unit = { key ->
        when {
            key.startsWith(EPIC_KEY_PREFIX) -> onOpenEpic(key.removePrefix(EPIC_KEY_PREFIX))
            key.startsWith(CONTRACT_KEY_PREFIX) ->
                onOpenContract(key.removePrefix(CONTRACT_KEY_PREFIX), conversationOfContract[key])
            else -> paneOf[key]?.let(onOpenPane)
        }
    }

    Column(modifier = modifier.fillMaxSize().testTag("project-page")) {
        TitleRow(crumbs = project?.let { listOf(Crumb(it.name)) }.orEmpty(), onBack = onBack)
        when {
            project != null -> ProjectBody(project, state, now(), onOpen, onNewHere)
            loaded == null -> Text(
                if (failed) "The server did not answer." else "loading…",
                modifier = Modifier.padding(OrreryTokens.Space.sixteen),
                style = OrreryTokens.Type.body,
                color = if (failed) OrreryTokens.Role.bad else OrreryTokens.Ink.three,
            )
            else -> Text(
                "Nothing in the inbox is in this project.",
                modifier = Modifier.padding(OrreryTokens.Space.sixteen),
                style = OrreryTokens.Type.body,
                color = OrreryTokens.Ink.three,
            )
        }
    }
}

@Composable
private fun ColumnScope.ProjectBody(
    project: Project,
    state: PaneListState,
    now: Long,
    onOpen: (String) -> Unit,
    onNewHere: (String) -> Unit,
) {
    Column(
        modifier = Modifier
            .weight(1f)
            .fillMaxWidth()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.sixteen),
    ) {
        ProjectHead(project, now)
        Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            SectionHead("Conversations", count = conversationsOf(project).toString())
            for (convo in project.convos) {
                ConvoBlock(convo, now, onOpen, state.foldOpen) { state.toggleFold(it) }
            }
        }
        if (project.ended.isNotEmpty()) {
            val unfolded = project.key in state.endedOpen
            EndedLine(project, unfolded, onClick = { state.toggleEnded(project.key) })
            if (unfolded) {
                for (convo in project.ended) {
                    ConvoBlock(convo, now, onOpen, state.foldOpen) { state.toggleFold(it) }
                }
            }
        }
    }
    if (project.dir.isNotEmpty()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
        ) {
            TextButton(onClick = { onNewHere(project.dir) }, modifier = Modifier.testTag("project-new-here")) {
                Text("New session here")
            }
        }
    }
}

/** The path with the host when it is another host, then the counts: conversations, epics, what waits, the last change. */
@Composable
private fun ProjectHead(project: Project, now: Long) {
    val place = listOf(project.path, if (project.host.isEmpty()) "" else "on ${project.host}")
        .filter { it.isNotEmpty() }
        .joinToString(" · ")
    val epics = project.convos.sumOf { convo -> convo.hung.count { it is EpicNode } }
    val waits = projectWaiting(project)
    val newest = newestChangeOf(project)
    Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four)) {
        if (place.isNotEmpty()) {
            Text(place, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
        }
        FlowRow(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.sixteen)) {
            Text(
                countWords(conversationsOf(project), "conversation", "conversations"),
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.two,
            )
            Text(countWords(epics, "epic", "epics"), style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
            if (waits > 0) {
                Text(
                    waitsWords(waits),
                    style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                    color = OrreryTokens.Role.attention,
                )
            }
            if (newest != 0L) {
                Text(
                    "last change ${ageOf((now - newest).coerceAtLeast(0L))} ago",
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Ink.three,
                )
            }
        }
    }
}

/**
 * One conversation and the epics and contracts hung under it, each a WorkCard, then its folded ended epics under a fold
 * line, which opens to them as WorkCards while [foldOpen] holds its key. A row with no pane is an own row: its one thing
 * draws as its WorkCard alone. A tap on the fold line calls [onFold] with the conversation's key.
 */
@Composable
private fun ConvoBlock(
    convo: Convo,
    now: Long,
    onOpen: (String) -> Unit,
    foldOpen: Set<String>,
    onFold: (String) -> Unit,
) {
    val pane = convo.pane
    Column(modifier = Modifier.fillMaxWidth()) {
        if (pane != null) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("project-convo:${convo.key}")
                    .clickable { onOpen(convo.key) }
                    .padding(vertical = OrreryTokens.Space.eight),
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                verticalAlignment = Alignment.Top,
            ) {
                StateGlyph(pane.state, Modifier.padding(top = OrreryTokens.Space.four))
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        pane.label,
                        style = OrreryTokens.Type.body,
                        color = OrreryTokens.Ink.one,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        convoLineOf(convo, now),
                        style = OrreryTokens.Type.small,
                        color = if (pane.state == STATE_WAITING) OrreryTokens.Role.attention else OrreryTokens.Ink.three,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                if (pane.changed != 0L) {
                    Text(
                        ageOf((now - pane.changed).coerceAtLeast(0L)),
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Ink.three,
                    )
                }
            }
        }
        if (convo.hung.isNotEmpty() || convo.folded.isNotEmpty()) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(start = OrreryTokens.Space.sixteen),
                verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                for (hung in convo.hung) {
                    WorkCard(card = workCardOf(hung, now), onOpen = onOpen)
                }
                if (convo.folded.isNotEmpty()) {
                    val open = convo.key in foldOpen
                    FoldLine(convo, open, onFold = { onFold(convo.key) })
                    if (open) {
                        for (epic in convo.folded) {
                            WorkCard(card = workCardOf(epic, now), onOpen = onOpen)
                        }
                    }
                }
            }
        }
    }
}

/** The ended line: its count, and whether its conversations are unfolded below it. */
@Composable
private fun EndedLine(project: Project, unfolded: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .testTag("project-ended")
            .fillMaxWidth()
            .clickable(role = Role.Button, onClick = onClick)
            .padding(vertical = OrreryTokens.Space.eight),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(if (unfolded) "−" else "+", color = OrreryTokens.Ink.four)
        Text(
            countWords(project.ended.size, "ended conversation", "ended conversations"),
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.three,
        )
    }
}

private fun countWords(count: Int, one: String, many: String): String = "$count ${if (count == 1) one else many}"

/** A project's conversations as the page counts them: the rows with a pane, not the own rows of things with none. */
private fun conversationsOf(project: Project): Int = project.convos.count { it.pane != null }

private fun waitsWords(count: Int): String = if (count == 1) "1 thing waits on you" else "$count things wait on you"

/** The newest change among the project's conversations and what hangs under them, unix seconds; zero when none has one. */
private fun newestChangeOf(project: Project): Long =
    (project.convos + project.ended).maxOfOrNull { convo ->
        convo.pane?.changed ?: convo.hung.maxOfOrNull { stampOf(it) } ?: 0L
    } ?: 0L

private fun stampOf(hung: Hung): Long = when (hung) {
    is EpicNode -> hung.epic.changed
    is ContractNode -> hung.contract.changed
}

// The age of a stamp in seconds: "30s", "12m", "3h", "2d".
private fun ageOf(seconds: Long): String = when {
    seconds < 60 -> "${seconds}s"
    seconds < 3_600 -> "${seconds / 60}m"
    seconds < 86_400 -> "${seconds / 3_600}h"
    else -> "${seconds / 86_400}d"
}
