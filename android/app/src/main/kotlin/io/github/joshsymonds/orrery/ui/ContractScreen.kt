package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.ContractDecisionRow
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException
import java.time.Instant
import java.time.ZoneId

/** The row an About concerns: its [id] and the [text] it shows. */
data class RowAbout(val id: String, val text: String)

/**
 * The marks the newest version of a contract wears: the rows that open decisions [concerned], whether the irreversible
 * section is [unread], the rows and sections the version changed, and the [version] number.
 */
data class DocMarks(
    val concerned: Set<String>,
    val unread: Boolean,
    val changedRows: Set<String>,
    val changedSections: Set<String>,
    val version: Long,
)

/** Semantics key: the id kind of an id chip, which is the id's first letter. */
val IdKindKey = SemanticsPropertyKey<String>("IdKind")

/** Semantics key: true on a row or section head that wears an attention mark. */
val AttentionKey = SemanticsPropertyKey<Boolean>("Attention")

private val NoMarks = DocMarks(emptySet(), false, emptySet(), emptySet(), 0L)

// Column weights shared by a table's head and its rows, so the two line up.
private const val ID_COLUMN = 0.7f
private const val WIDE_COLUMN = 2f
private const val NARROW_COLUMN = 1f

/**
 * A contract as Gambit's document: its decision line, then its ten sections in order, each under its name with its
 * column names, rating chips, id chips, attention marks and changed chips. [decisions] are the contract's decisions;
 * absent, the document is an Epics.Get contract's and its Decisions section reads None. [record] is the decision
 * line's record words, absent for the accepted clock. [marks] are the marks the newest version wears. [onAbout], when
 * given, draws each row's control and each addition's Keep and Strike. [onIrreversibleShown] is called once the
 * irreversible section has been composed on screen. [facts] is drawn last inside the decision line. [attending] is whether
 * the version is the newest of a proposed contract, the one the viewer is deciding: only while it is do its open decisions
 * count, draw attention, and mark their rows, sections and the marks. Absent, it is.
 */
@Composable
fun ContractBody(
    contract: EpicContractRow,
    requirements: List<EpicRequirementRow>,
    modifier: Modifier = Modifier,
    decisions: List<ContractDecisionRow>? = null,
    record: String? = null,
    marks: DocMarks? = null,
    attending: Boolean = true,
    onAbout: ((RowAbout) -> Unit)? = null,
    onIrreversibleShown: (() -> Unit)? = null,
    facts: (LazyListScope.() -> Unit)? = null,
) {
    val decided = decisions ?: emptyList()
    val open = decided.filter { it.answer.isEmpty() }
    val counted = if (attending) open else emptyList()
    val settled = decided.filter { it.answer.isNotEmpty() }
    val docMarks = marks ?: NoMarks
    val unread = attending && docMarks.unread
    val accepted = Instant.ofEpochSecond(contract.accepted).atZone(ZoneId.systemDefault()).format(CLOCK_TIME)
    val recordWords = record ?: "accepted $accepted"
    val setter = contract.failures.firstOrNull { it.rating == contract.care }
    val careLine = if (setter == null) {
        "Level of care: ${contract.care}"
    } else {
        "Level of care: ${contract.care}, set by ${setter.id}"
    }

    fun attends(id: String): Boolean = attending && id in docMarks.concerned
    fun changedRow(id: String): Long? = if (id in docMarks.changedRows) docMarks.version else null
    fun changedIn(vararg sections: String): Long? =
        if (sections.any { it in docMarks.changedSections }) docMarks.version else null

    LazyColumn(
        modifier = modifier.testTag("contract-body"),
        contentPadding = PaddingValues(OrreryTokens.Space.sixteen),
    ) {
        item {
            Column(
                modifier = Modifier.fillMaxWidth().testTag("contract-dline"),
                verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
            ) {
                Text(contract.title, style = OrreryTokens.Type.display)
                if (contract.approved.isNotEmpty()) {
                    Text(contract.approved, style = OrreryTokens.Type.small)
                }
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    Text("Level of care", style = OrreryTokens.Type.small)
                    Chip(contract.care, ratingTone(contract.care))
                }
                Text(
                    if (counted.isEmpty()) "Decisions needed: none" else "Decisions needed: ${counted.size}",
                    style = OrreryTokens.Type.small,
                )
                Text(recordWords, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                if (contract.base.isNotEmpty()) {
                    Text(
                        "⎇",
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Ink.three,
                        modifier = Modifier.testTag("contract-base").semantics { contentDescription = contract.base },
                    )
                }
            }
        }
        facts?.invoke(this)

        sectionHead(0, "What you asked for", changed = changedIn("intent"))
        item { Text(contract.intent, style = OrreryTokens.Type.body) }

        sectionHead(
            1,
            "What could go wrong, and how much we care",
            changed = changedIn("failures", "care"),
        )
        item {
            TableHead(listOf("" to ID_COLUMN, "if this happened" to WIDE_COLUMN, "how bad" to NARROW_COLUMN, "what we do" to WIDE_COLUMN))
        }
        items(contract.failures) { failure ->
            DocRow(tag = "contract-row:${failure.id}", attention = attends(failure.id)) {
                Cell(ID_COLUMN) { IdChip(failure.id) }
                Cell(WIDE_COLUMN) {
                    Column {
                        Text(failure.text, style = OrreryTokens.Type.body)
                        ChangedChip(changedRow(failure.id))
                    }
                }
                Cell(NARROW_COLUMN) { Chip(failure.rating, ratingTone(failure.rating)) }
                Cell(WIDE_COLUMN) {
                    Column {
                        Text(failure.response, style = OrreryTokens.Type.small)
                        Talk(RowAbout(failure.id, failure.text), onAbout)
                    }
                }
            }
        }
        item { Text(careLine, style = OrreryTokens.Type.small, modifier = Modifier.padding(top = OrreryTokens.Space.eight)) }

        sectionHead(
            2,
            "Things you did not ask for",
            count = contract.additions.size.takeIf { it > 0 },
            changed = changedIn("additions"),
        )
        if (contract.additions.isEmpty()) {
            item { Text("None.", style = OrreryTokens.Type.small) }
        } else {
            item {
                TableHead(listOf("where" to WIDE_COLUMN, "what" to WIDE_COLUMN, "why" to WIDE_COLUMN, "cost" to WIDE_COLUMN, "" to NARROW_COLUMN))
            }
            itemsIndexed(contract.additions) { index, addition ->
                DocRow(tag = "contract-addition:$index") {
                    Cell(WIDE_COLUMN) { Text(addition.location, style = OrreryTokens.Type.small) }
                    Cell(WIDE_COLUMN) { Text(addition.what, style = OrreryTokens.Type.body) }
                    Cell(WIDE_COLUMN) { Text(addition.why, style = OrreryTokens.Type.small) }
                    Cell(WIDE_COLUMN) { Text(addition.cost, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three) }
                    Cell(NARROW_COLUMN) {
                        onAbout?.let { ask ->
                            Column {
                                Button(
                                    onClick = { ask(RowAbout(addition.location, "Keep: ${addition.what}")) },
                                    modifier = Modifier.testTag("contract-keep:$index"),
                                ) { Text("Keep", style = OrreryTokens.Type.small) }
                                OutlinedButton(
                                    onClick = { ask(RowAbout(addition.location, "Strike: ${addition.what}")) },
                                    modifier = Modifier.testTag("contract-strike:$index"),
                                ) { Text("Strike", style = OrreryTokens.Type.small) }
                            }
                        }
                    }
                }
            }
        }

        sectionHead(
            3,
            "What will be true when done",
            count = requirements.size.takeIf { it > 0 },
            changed = changedIn("requirements"),
        )
        item {
            TableHead(listOf("" to ID_COLUMN, "must be true" to WIDE_COLUMN, "how we’ll know" to WIDE_COLUMN))
        }
        items(requirements) { requirement ->
            DocRow(tag = "contract-row:${requirement.id}", attention = attends(requirement.id)) {
                Cell(ID_COLUMN) { IdChip(requirement.id) }
                Cell(WIDE_COLUMN) {
                    Column {
                        Text(requirement.text, style = OrreryTokens.Type.body)
                        ChangedChip(changedRow(requirement.id))
                    }
                }
                Cell(WIDE_COLUMN) {
                    Column {
                        Text(requirement.evidence, style = OrreryTokens.Type.small)
                        Talk(RowAbout(requirement.id, requirement.text), onAbout)
                    }
                }
            }
        }

        sectionHead(
            4,
            "What I’m assuming",
            count = contract.premises.size.takeIf { it > 0 },
            changed = changedIn("premises"),
        )
        item {
            TableHead(listOf("" to ID_COLUMN, "assumption" to WIDE_COLUMN, "if wrong" to WIDE_COLUMN))
        }
        items(contract.premises) { premise ->
            DocRow(tag = "contract-row:${premise.id}", attention = attends(premise.id)) {
                Cell(ID_COLUMN) { IdChip(premise.id) }
                Cell(WIDE_COLUMN) {
                    Column {
                        Text(premise.text, style = OrreryTokens.Type.body)
                        ChangedChip(changedRow(premise.id))
                    }
                }
                Cell(WIDE_COLUMN) {
                    Column {
                        Text(premise.ifWrong, style = OrreryTokens.Type.small)
                        Talk(RowAbout(premise.id, premise.text), onAbout)
                    }
                }
            }
        }

        sectionHead(5, "What we won’t do", changed = changedIn("exclusions"))
        items(contract.exclusions) { exclusion ->
            Text(exclusion, style = OrreryTokens.Type.body, modifier = Modifier.padding(vertical = OrreryTokens.Space.four))
        }

        sectionHead(6, "How, and why not the other ways", changed = changedIn("approach", "alternatives"))
        item { Text(contract.approach, style = OrreryTokens.Type.body) }
        item {
            TableHead(listOf("alternative" to WIDE_COLUMN, "why not" to WIDE_COLUMN, "reconsider when" to WIDE_COLUMN))
        }
        itemsIndexed(contract.alternatives) { index, alternative ->
            DocRow(tag = "contract-alternative:$index") {
                Cell(WIDE_COLUMN) { Text(alternative.alternative, style = OrreryTokens.Type.body) }
                Cell(WIDE_COLUMN) { Text(alternative.whyNot, style = OrreryTokens.Type.small) }
                Cell(WIDE_COLUMN) { Text(alternative.reconsider, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three) }
            }
        }

        sectionHead(
            7,
            "What leaves this machine or can’t be undone",
            attention = unread,
            rest = if (unread) "read this before Accept" else null,
            changed = changedIn("release_steps", "release_then"),
            onShown = onIrreversibleShown,
        )
        item {
            TableHead(listOf("" to ID_COLUMN, "step" to WIDE_COLUMN, "target" to WIDE_COLUMN, "undo" to NARROW_COLUMN))
        }
        itemsIndexed(contract.releaseSteps) { index, step ->
            DocRow(tag = "contract-step:$index") {
                Cell(ID_COLUMN) { Text("${index + 1}", style = OrreryTokens.Type.small) }
                Cell(WIDE_COLUMN) { Text(step.step, style = OrreryTokens.Type.body) }
                Cell(WIDE_COLUMN) { Text(step.target, style = OrreryTokens.Type.small) }
                Cell(NARROW_COLUMN) { Text(step.undo, style = OrreryTokens.Type.small) }
            }
        }
        item { Text(contract.releaseThen, style = OrreryTokens.Type.small, modifier = Modifier.padding(top = OrreryTokens.Space.eight)) }

        sectionHead(
            8,
            "Decisions I need from you",
            count = counted.size.takeIf { it > 0 },
            attention = counted.isNotEmpty(),
            changed = changedIn("decisions"),
        )
        if (decided.isEmpty()) {
            item { Text("None.", style = OrreryTokens.Type.small) }
        }
        if (open.isNotEmpty()) {
            item {
                TableHead(listOf("" to ID_COLUMN, "question" to WIDE_COLUMN, "recommended" to WIDE_COLUMN, "why" to WIDE_COLUMN))
            }
            items(open) { decision ->
                DocRow(tag = "contract-row:${decision.id}", attention = attending) {
                    Cell(ID_COLUMN) { IdChip(decision.id) }
                    Cell(WIDE_COLUMN) {
                        Column {
                            Text(decision.question, style = OrreryTokens.Type.body)
                            Text(decision.options.joinToString(" · "), style = OrreryTokens.Type.small, color = OrreryTokens.Ink.two)
                            ChangedChip(changedRow(decision.id))
                        }
                    }
                    Cell(WIDE_COLUMN) { Text(decision.recommendation, style = OrreryTokens.Type.small) }
                    Cell(WIDE_COLUMN) {
                        Column {
                            Text(decision.reason, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                            Talk(RowAbout(decision.id, decision.question), onAbout)
                        }
                    }
                }
            }
        }
        settled.forEach { decision ->
            item {
                val lead = if (open.isEmpty()) "None. " else ""
                Text("${lead}Settled: ${decision.id} ${decision.answer}", style = OrreryTokens.Type.small)
            }
        }

        sectionHead(9, "Checks the machines run", changed = changedIn("checks"))
        item { Text(contract.checks, style = OrreryTokens.Type.body) }
    }
}

/** A rating as a chip in its tone: limited is plain, serious is live, severe is bad. */
private fun ratingTone(rating: String): ChipTone = when (rating) {
    "serious" -> ChipTone.Live
    "severe" -> ChipTone.Bad
    else -> ChipTone.Neutral
}

@Composable
internal fun IdChip(id: String) {
    Chip(id, ChipTone.Neutral, modifier = Modifier.testTag("idc:$id").semantics { set(IdKindKey, id.take(1)) })
}

@Composable
private fun ChangedChip(version: Long?) {
    if (version != null) Chip("changed in v$version", ChipTone.New)
}

/** The row's control that talks about it, drawn only when the body is given an [onAbout]. */
@Composable
private fun Talk(about: RowAbout, onAbout: ((RowAbout) -> Unit)?) {
    if (onAbout != null) TalkControl(about, onAbout)
}

@Composable
private fun TalkControl(about: RowAbout, onAbout: (RowAbout) -> Unit) {
    Canvas(
        modifier = Modifier
            .size(OrreryTokens.Size.glyph)
            .testTag("contract-talk:${about.id}")
            .clickable { onAbout(about) }
            .semantics { contentDescription = "Talk about this row" },
    ) {
        drawRoundRect(
            color = OrreryTokens.Ink.two,
            cornerRadius = CornerRadius(OrreryTokens.Radius.one.toPx()),
            style = Stroke(width = OrreryTokens.Size.hairline.toPx()),
        )
    }
}

@Composable
private fun TableHead(columns: List<Pair<String, Float>>) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(top = OrreryTokens.Space.eight),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        columns.forEach { (name, weight) ->
            Cell(weight) { Text(name, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three) }
        }
    }
}

/** A table row. An attention row wears a rail in the attention colour on its leading edge. */
@Composable
private fun DocRow(tag: String, attention: Boolean = false, content: @Composable RowScope.() -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = OrreryTokens.Space.four)
            .testTag(tag)
            .drawBehind {
                if (attention) drawRect(OrreryTokens.Role.attention, size = size.copy(width = OrreryTokens.Size.rail.toPx()))
            }
            .then(if (attention) Modifier.semantics { set(AttentionKey, true) } else Modifier),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        content = content,
    )
}

@Composable
private fun RowScope.Cell(weight: Float, content: @Composable () -> Unit) {
    Box(modifier = Modifier.weight(weight)) { content() }
}

private fun LazyListScope.sectionHead(
    index: Int,
    name: String,
    count: Int? = null,
    attention: Boolean = false,
    rest: String? = null,
    changed: Long? = null,
    onShown: (() -> Unit)? = null,
) {
    item {
        SectionHead(index, name, count, attention, rest, changed, onShown)
    }
}

/**
 * A section's head: its name, its count, its tag while attention, its changed chip. [onShown] is called once the head
 * is composed on screen.
 */
@Composable
private fun SectionHead(
    index: Int,
    name: String,
    count: Int?,
    attention: Boolean,
    rest: String?,
    changed: Long?,
    onShown: (() -> Unit)?,
) {
    val currentShown by rememberUpdatedState(onShown)
    LaunchedEffect(Unit) { currentShown?.invoke() }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = OrreryTokens.Space.sixteen)
            .testTag("contract-section:$index")
            .then(if (attention) Modifier.semantics { set(AttentionKey, true) } else Modifier),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            name.inLabelCase(),
            style = OrreryTokens.Type.label,
            color = if (attention) OrreryTokens.Role.attention else OrreryTokens.Ink.one,
            modifier = Modifier.testTag("contract-heading:$index"),
        )
        if (count != null) Text("$count", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
        if (rest != null) Text(rest, style = OrreryTokens.Type.small, color = OrreryTokens.Role.attention)
        ChangedChip(changed)
    }
}

/**
 * A contract as its own screen: the crumbs orrery, the contract's slug and contract, then the contract's record when
 * Contracts.Get has one, or else the epic's contract from Epics.Get as [ContractBody] draws it read-only. The orrery
 * crumb calls [onHome]; back and the slug crumb call [onBack]. The contract is read off the main thread; a read that
 * fails, or a contract that does not exist, says so in the contract's place. [live], [writer], [onAbout] and [onTalk]
 * are the record's (see [ContractRecord]). [titled] false leaves out the title row, as the phone sheet's body draws it.
 */
@Composable
fun ContractScreen(
    client: OrreryClient,
    epicId: String,
    onBack: () -> Unit,
    onHome: () -> Unit,
    modifier: Modifier = Modifier,
    live: Boolean = false,
    writer: (String) -> String = { it },
    onAbout: ((RowAbout) -> Unit)? = null,
    onTalk: (() -> Unit)? = null,
    titled: Boolean = true,
) {
    var load by remember(client, epicId) { mutableStateOf<ContractLoad>(ContractLoad.Loading) }
    LaunchedEffect(client, epicId) {
        load = try {
            withContext(Dispatchers.IO) { readContract(client, epicId) }
        } catch (error: IOException) {
            ContractLoad.Failed
        }
    }

    val current = load
    val slug = when (current) {
        is ContractLoad.Record -> contractSlug(epicId)
        is ContractLoad.Epic -> current.detail.epic.slug
        else -> null
    }
    Column(modifier = modifier.fillMaxSize().testTag("contract-screen")) {
        if (titled) {
            TitleRow(
                crumbs = listOfNotNull(
                    Crumb("orrery", onHome),
                    slug?.let { Crumb(it, onBack) },
                    Crumb("contract"),
                ),
                onBack = onBack,
            )
        }
        when (current) {
            ContractLoad.Loading -> Text("Loading...", modifier = Modifier.padding(OrreryTokens.Space.sixteen))
            ContractLoad.Failed -> Text(
                "contract unavailable",
                color = OrreryTokens.Role.bad,
                modifier = Modifier.padding(OrreryTokens.Space.sixteen),
            )
            ContractLoad.NotFound -> Text("epic not found", modifier = Modifier.padding(OrreryTokens.Space.sixteen))
            is ContractLoad.Record -> ContractRecord(
                client = client,
                id = epicId,
                loaded = current.loaded,
                onLoaded = { load = ContractLoad.Record(it) },
                live = live,
                writer = writer,
                onAbout = onAbout,
                onTalk = onTalk,
                modifier = Modifier.weight(1f),
            )
            is ContractLoad.Epic -> {
                val contract = current.detail.contract
                if (contract == null) {
                    Text("this epic has no contract", modifier = Modifier.padding(OrreryTokens.Space.sixteen))
                } else {
                    ContractBody(
                        contract = contract,
                        requirements = current.detail.requirements,
                        modifier = Modifier.weight(1f),
                    )
                }
            }
        }
    }
}

/** The contract [epicId] names: its record when Contracts.Get has one, else the epic's from Epics.Get. */
private fun readContract(client: OrreryClient, epicId: String): ContractLoad {
    readRecord(client, epicId)?.let { return ContractLoad.Record(it) }
    val detail = client.epic(epicId) ?: return ContractLoad.NotFound
    return ContractLoad.Epic(detail)
}

private sealed interface ContractLoad {
    data object Loading : ContractLoad
    data object Failed : ContractLoad
    data object NotFound : ContractLoad
    data class Record(val loaded: LoadedRecord) : ContractLoad
    data class Epic(val detail: EpicDetail) : ContractLoad
}
