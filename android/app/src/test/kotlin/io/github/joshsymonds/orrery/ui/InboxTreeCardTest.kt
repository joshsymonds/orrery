package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Text
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class InboxTreeCardTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun pane(id: String) = Pane(
        id = id,
        host = "laptop",
        session = "work",
        window = 0,
        pane = 0,
        cwd = "/home/user/lighthouse",
        command = "",
        state = "working",
        summary = "",
        label = id,
        changed = 100L,
        seen = 0L,
        question = "",
        questionHash = "",
    )

    private fun row(key: String) = Convo(key, pane(key), StandIn.NONE, again = false, hung = emptyList())

    private val standIn = Convo("standin:unlisted:k", null, StandIn.UNLISTED, again = false, hung = emptyList())

    private fun project(host: String = "") = Project(
        key = "$host|/home/user/lighthouse",
        dir = "/home/user/lighthouse",
        name = "lighthouse",
        path = "~/lighthouse",
        host = host,
        convos = listOf(row("a"), row("b"), standIn),
        ended = listOf(row("c")),
    )

    private fun setCard(
        project: Project = project(),
        folded: Boolean = false,
        waiting: Int = 0,
        endedOpen: Boolean = false,
        onFold: () -> Unit = {},
        onEnded: () -> Unit = {},
    ) {
        composeRule.setContent {
            ProjectCard(
                project = project,
                folded = folded,
                waiting = waiting,
                endedOpen = endedOpen,
                onFold = onFold,
                onEnded = onEnded,
                convo = { c -> Text("convo ${c.key}", modifier = Modifier.testTag("convo:${c.key}")) },
            )
        }
    }

    private fun setChips(counts: Counts, narrow: Narrow, onNarrow: (Narrow) -> Unit) {
        composeRule.setContent {
            Column { InboxChips(counts, narrow, onNarrow) }
        }
    }

    @Test
    fun aCardHeadShowsItsNameAndItsHomePathAndAHostChipForAnotherHost() {
        setCard(project(host = "gpu-box"))

        composeRule.onNodeWithText("lighthouse").assertExists()
        composeRule.onNodeWithText("~/lighthouse").assertExists()
        composeRule.onNodeWithText("gpu-box").assertExists()
    }

    @Test
    fun aCardOnTheServingHostDrawsNoHostChip() {
        setCard(project(host = ""))

        composeRule.onAllNodes(SemanticsMatcher.keyIsDefined(ChipToneKey)).assertCountEquals(0)
    }

    @Test
    fun aFoldedCardSaysWhatWaitsAndItsConversationCountAndNothingElse() {
        val card = project()
        setCard(card, folded = true, waiting = 2)

        composeRule.onNodeWithText("2 waiting").assertExists()
        composeRule.onNodeWithText("3").assertExists()
        composeRule.onNodeWithTag("convo:a").assertDoesNotExist()
        composeRule.onNodeWithTag("convo:c").assertDoesNotExist()
        composeRule.onNodeWithTag("ended:${card.key}").assertDoesNotExist()
    }

    @Test
    fun aFoldedCardWithNothingWaitingSaysNoWaitingCount() {
        setCard(folded = true, waiting = 0)

        composeRule.onNodeWithText("0 waiting").assertDoesNotExist()
    }

    @Test
    fun anOpenCardDrawsItsConversationsAndAnEndedLineWithItsCount() {
        val card = project()
        setCard(card)

        composeRule.onNodeWithTag("convo:a").assertExists()
        composeRule.onNodeWithTag("convo:b").assertExists()
        composeRule.onNodeWithTag("convo:standin:unlisted:k").assertExists()
        composeRule.onNodeWithText("1 ended").assertExists()
        composeRule.onNodeWithTag("convo:c").assertDoesNotExist()
    }

    @Test
    fun theEndedLineOpensToTheEndedConversations() {
        setCard(endedOpen = true)

        composeRule.onNodeWithTag("convo:c").assertExists()
    }

    @Test
    fun aCardWithNothingEndedDrawsNoEndedLine() {
        val card = project().copy(ended = emptyList())
        setCard(card)

        composeRule.onNodeWithTag("ended:${card.key}").assertDoesNotExist()
    }

    @Test
    fun aPressOnTheFoldControlAndOnTheEndedLineCallsTheirPresses() {
        val card = project()
        var folds = 0
        var endeds = 0
        setCard(card, onFold = { folds++ }, onEnded = { endeds++ })

        composeRule.onNodeWithTag("fold:${card.key}").performClick()
        assertEquals(1, folds)
        assertEquals(0, endeds)

        composeRule.onNodeWithTag("ended:${card.key}").performClick()
        assertEquals(1, folds)
        assertEquals(1, endeds)
    }

    @Test
    fun theChipsAreDrawnOnlyWhenTheirCountIsNotZeroWithTheirWords() {
        setChips(Counts(waiting = 1, fresh = 0), Narrow.NONE) {}

        composeRule.onNodeWithText("1 waits on you").assertExists()
        composeRule.onNodeWithTag("chip-new").assertDoesNotExist()
    }

    @Test
    fun theWaitingChipSaysWaitOnYouForMoreThanOneAndTheNewChipSaysNew() {
        setChips(Counts(waiting = 3, fresh = 2), Narrow.NONE) {}

        composeRule.onNodeWithText("3 wait on you").assertExists()
        composeRule.onNodeWithText("2 new").assertExists()
    }

    @Test
    fun aWaitingChipWithNothingWaitingIsNotDrawn() {
        setChips(Counts(waiting = 0, fresh = 4), Narrow.NONE) {}

        composeRule.onNodeWithTag("chip-waiting").assertDoesNotExist()
        composeRule.onNodeWithText("4 new").assertExists()
    }

    @Test
    fun aPressOnAChipNarrowsToItsKind() {
        val picked = mutableListOf<Narrow>()
        setChips(Counts(waiting = 2, fresh = 2), Narrow.NONE) { picked += it }

        composeRule.onNodeWithTag("chip-waiting").performClick()
        composeRule.onNodeWithTag("chip-new").performClick()

        assertEquals(listOf(Narrow.WAITING, Narrow.NEW), picked)
    }

    @Test
    fun aPressOnTheOnChipReturnsToAll() {
        val picked = mutableListOf<Narrow>()
        setChips(Counts(waiting = 2, fresh = 2), Narrow.WAITING) { picked += it }

        composeRule.onNodeWithTag("chip-waiting").assertIsSelected()
        composeRule.onNodeWithTag("chip-new").assertIsNotSelected()
        composeRule.onNodeWithTag("chip-waiting").performClick()

        assertEquals(listOf(Narrow.NONE), picked)
    }
}
