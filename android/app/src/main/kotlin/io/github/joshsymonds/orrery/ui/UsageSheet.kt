package io.github.joshsymonds.orrery.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The accounts sheet's root node: the scrim and the sheet over the inbox, opened by a tap on the usage band. */
internal const val USAGE_SHEET_TAG = "usage-sheet"

/** The sheet's close control, which dismisses it. */
internal const val USAGE_SHEET_CLOSE_TAG = "usage-sheet-close"

/** The sheet's link to the accounts page, "Accounts and routes". */
internal const val USAGE_SHEET_ACCOUNTS_TAG = "usage-sheet-accounts"

/** The tag of [account]'s card in the sheet, by the account's name. */
internal fun usageCardTag(account: String): String = "usage-card:$account"

/** The tag of the Use button on [account]'s card, by the account's name. */
internal fun usageUseTag(account: String): String = "usage-use:$account"

/** The tag of one window column in [account]'s card, by the window's [claim]. */
internal fun usageSheetWindowTag(account: String, claim: String): String = "usage-sheet-window:$account:$claim"

/**
 * The accounts sheet a tap on the usage band opens, as the phone sheet does: a scrim over the inbox, and the sheet on the
 * side's ground, its rounded top corners and a grab bar, under its head with the title and Close. Each account in [rows]
 * is a card: its name, a line of who uses it, its plan or its limit ("limited until HH:MM"), and how long ago it was read
 * at [now] in unix seconds; then three equal window columns, each with its value, its bar and the countdown to its reset.
 * Accounts and routes calls [onOpenAccounts]. Close, a tap on the scrim, and the back gesture call [onClose]. Each card's
 * header ends with what its row offers: a Use button calls [onUse] with the row, "in use" when every context names the
 * account, and nothing when no context names an account.
 */
@Composable
internal fun UsageSheet(
    rows: List<UsageRow>,
    now: Long,
    onClose: () -> Unit,
    onOpenAccounts: () -> Unit,
    onUse: (UsageRow) -> Unit,
    modifier: Modifier = Modifier,
) {
    BackHandler(onBack = onClose)
    Column(modifier = modifier.fillMaxSize().testTag(USAGE_SHEET_TAG)) {
        Box(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                .background(Color.Black.copy(alpha = OrreryTokens.Alpha.scrim))
                .clickable(onClick = onClose),
        )
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .background(
                    OrreryTokens.Surface.overlay,
                    RoundedCornerShape(topStart = OrreryTokens.Radius.three, topEnd = OrreryTokens.Radius.three),
                )
                .verticalScroll(rememberScrollState())
                .padding(bottom = OrreryTokens.Space.sixteen),
        ) {
            Box(
                modifier = Modifier
                    .align(Alignment.CenterHorizontally)
                    .padding(top = OrreryTokens.Space.eight)
                    .size(OrreryTokens.Size.controlDesk, OrreryTokens.Size.meterBarHeight)
                    .clip(CircleShape)
                    .background(OrreryTokens.Line.firm),
            )
            SheetHead(onClose)
            rows.forEach { row -> AccountCard(row, now, onUse) }
            TextButton(
                onClick = onOpenAccounts,
                modifier = Modifier
                    .padding(horizontal = OrreryTokens.Space.eight)
                    .testTag(USAGE_SHEET_ACCOUNTS_TAG),
            ) {
                Text("Accounts and routes")
            }
        }
    }
}

/** The sheet's head: its title, and Close at the end of it. */
@Composable
private fun SheetHead(onClose: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = OrreryTokens.Space.sixteen, end = OrreryTokens.Space.eight, top = OrreryTokens.Space.four),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            "Claude accounts",
            modifier = Modifier.weight(1f),
            style = OrreryTokens.Type.title,
            color = OrreryTokens.Ink.one,
        )
        TextButton(onClick = onClose, modifier = Modifier.testTag(USAGE_SHEET_CLOSE_TAG)) {
            Text("Close")
        }
    }
}

/**
 * The line under an account's name: its limit when it is limited ("limited until HH:MM"), else who uses it ("used by"),
 * else its plan; then how long ago it was read. A line with no part is left out, and an account never read says so.
 */
private fun cardLineOf(row: UsageRow, now: Long): String {
    val subject = when {
        row.limited -> row.liftsAt?.let { "limited until ${usageClockOf(it)}" } ?: "limited"
        row.usedBy.isNotEmpty() -> "used by ${row.usedBy.joinToString(", ")}"
        else -> row.account.plan
    }
    val read = row.readAt?.let { "read ${agoText(it, now)}" } ?: "no reading"
    return listOf(subject, read).filter { it.isNotEmpty() }.joinToString(" · ")
}

/** One account's card: its name and line, its Use or "in use" at the end of its header, and its three window columns. */
@Composable
private fun AccountCard(row: UsageRow, now: Long, onUse: (UsageRow) -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four)
            .clip(RoundedCornerShape(OrreryTokens.Radius.three))
            .background(OrreryTokens.Surface.well)
            .testTag(usageCardTag(row.account.name))
            .padding(OrreryTokens.Space.twelve),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Row(
                modifier = Modifier.weight(1f),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                Text(
                    row.account.name,
                    modifier = Modifier.weight(1f, fill = false),
                    style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                    color = OrreryTokens.Ink.one,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                if (row.limited) LimitedMark()
            }
            when (row.useOffer) {
                UseOffer.IN_USE -> Text("in use", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                UseOffer.USE -> TextButton(
                    onClick = { onUse(row) },
                    modifier = Modifier.testTag(usageUseTag(row.account.name)),
                ) {
                    Text("Use")
                }
                null -> Unit
            }
        }
        Text(
            cardLineOf(row, now),
            style = OrreryTokens.Type.small,
            color = if (row.stale) OrreryTokens.Ink.two else OrreryTokens.Ink.three,
        )
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.sixteen),
        ) {
            USAGE_COLUMNS.forEachIndexed { index, column ->
                SheetWindow(
                    account = row.account.name,
                    window = row.windows.getOrNull(index),
                    column = column,
                    now = now,
                    modifier = Modifier.weight(1f),
                )
            }
        }
    }
}

/** One window column of a card: its head, its value, its bar, and the countdown to its reset at [now]. */
@Composable
private fun SheetWindow(
    account: String,
    window: UsageWindow?,
    column: UsageColumn,
    now: Long,
    modifier: Modifier,
) {
    val level = levelColorOf(window?.level)
    val countdown = window?.resetAt?.let { "in ${untilWords(it - now)}" } ?: ""
    Column(
        modifier = modifier.testTag(usageSheetWindowTag(account, column.claim)),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        Text(column.head, style = OrreryTokens.Type.label, color = column.hue, maxLines = 1)
        Text(
            windowValueOf(window),
            style = OrreryTokens.Type.body,
            color = level ?: OrreryTokens.Ink.one,
            maxLines = 1,
        )
        MeterBar(fraction = fillOf(window), hue = level ?: column.hue)
        Text(countdown, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three, maxLines = 1)
    }
}
