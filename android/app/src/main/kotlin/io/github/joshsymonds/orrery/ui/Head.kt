package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import io.github.joshsymonds.orrery.api.ConversationContext
import io.github.joshsymonds.orrery.api.HostStat

/**
 * The conversation's header on a phone. The title row always draws. When the header is open it adds the machine band
 * and the band; when folded (reading or hidden) it adds the compact line, with the words that ask the reader.
 */
@Composable
internal fun Head(
    dir: String,
    host: String,
    title: String,
    context: ConversationContext?,
    note: String,
    stats: List<HostStat>?,
    otherHost: String?,
    goal: String,
    lines: List<AskLine>,
    asking: String,
    badges: List<HeadBadge>,
    fold: HeadFold,
    onBack: () -> Unit,
    onOpenEpic: (String) -> Unit,
    onHide: () -> Unit,
    onShow: () -> Unit,
    modifier: Modifier = Modifier,
    contracts: List<ContractBandLine> = emptyList(),
    hereContract: String = "",
    onOpenContract: (String) -> Unit = {},
    onFind: () -> Unit = {},
    work: List<WorkLink> = emptyList(),
    workLit: String? = null,
    onOpenWork: (WorkLink) -> Unit = {},
    onAllWork: () -> Unit = {},
) {
    var moreOpen by remember { mutableStateOf(false) }
    Column(
        modifier = modifier
            .testTag("head")
            .semantics { stateDescription = fold.name.lowercase() },
    ) {
        HeadTitleRow(
            dir = dir,
            title = title,
            context = context,
            note = note,
            onBack = onBack,
            onMore = { moreOpen = true },
            moreOpen = moreOpen,
            onDismissMore = { moreOpen = false },
            onFind = onFind,
            onHideHeader = onHide,
            work = work,
            workLit = workLit,
            onOpenWork = onOpenWork,
            onAllWork = onAllWork,
        )
        if (fold == HeadFold.OPEN) {
            MachineBand(host = host, stats = stats, otherHost = otherHost)
            Band(
                goal = goal,
                lines = lines,
                onOpen = onOpenEpic,
                onHide = onHide,
                contracts = contracts,
                hereContract = hereContract,
                onOpenContract = onOpenContract,
            )
        } else {
            CompactHead(asking = asking, badges = badges, onOpen = onShow)
        }
    }
}
