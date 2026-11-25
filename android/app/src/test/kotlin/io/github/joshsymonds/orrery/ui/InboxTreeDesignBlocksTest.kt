package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.DesignContractRow
import io.github.joshsymonds.orrery.api.DesignRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// A design in the inbox tree on Android: its tab, its well and the kids under it, inside its conversation's block and
// its project's card. Built from synthetic values at a fixed clock, and read through the semantics tree alone.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxTreeDesignBlocksTest {
    private companion object {
        const val NOW = 100_000L
        const val CARD = "lighthouse-card"
        const val CONVO = "session:s1"
        const val DESIGN_ID = "design-tide"
        const val DESIGN_TITLE = "Tide alerts"
        const val EPIC_ID = "lighthouse/rota-export"
        const val CONTRACT_ID = "lighthouse/tide-alerts"
        const val LOOSE_ID = "lighthouse/loose-export"
        const val STATE_LINE = "design · working"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun paneOf() = Pane(
        id = "s1",
        host = "laptop",
        session = "",
        window = 0,
        pane = 0,
        cwd = "/home/user/lighthouse",
        command = "",
        state = "working",
        summary = "",
        label = "Lamp",
        changed = NOW - 120,
        seen = 0L,
        question = "",
        questionHash = "",
        headless = true,
        orreryId = "conv-1",
    )

    private fun epic(id: String) = EpicRow(
        id = id,
        repository = "/home/user/lighthouse",
        slug = id.substringAfter('/'),
        outcome = "accepted",
        changed = 0L,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        phaseAt = NOW - 49 * 60,
    )

    private fun epicNode(id: String) = EpicNode(epic(id), EpicDetail(epic(id), emptyList(), emptyList(), emptyList()), null)

    private fun contractNode() = ContractNode(
        ContractRow(
            id = CONTRACT_ID,
            repository = "/home/user/lighthouse",
            title = "tide alerts",
            care = "",
            state = "proposed",
            version = 3L,
            openDecisions = 0L,
            conversation = "conv-1",
            created = 0L,
            changed = NOW - 180,
        ),
    )

    private fun designRow(title: String = DESIGN_TITLE) = DesignRow(
        id = DESIGN_ID,
        title = title,
        conversation = "conv-1",
        created = 0L,
        ofOne = false,
        state = "working",
        contracts = listOf(
            DesignContractRow(
                id = CONTRACT_ID,
                title = "tide alerts",
                repository = "/home/user/lighthouse",
                state = "proposed",
                position = 1L,
                after = emptyList(),
                epic = EPIC_ID,
                epicPhase = "planning",
            ),
        ),
    )

    private fun designBlock(title: String = DESIGN_TITLE) =
        DesignBlock(designRow(title), kids = listOf(epicNode(EPIC_ID), contractNode()))

    private fun convo(designs: List<DesignBlock>, hung: List<Hung> = emptyList()) = Convo(
        key = CONVO,
        pane = paneOf(),
        hung = hung,
        folded = emptyList(),
        designs = designs,
    )

    private fun setCard(convo: Convo, opened: MutableList<String> = mutableListOf()) {
        composeRule.setContent {
            ProjectCard(
                project = Project(
                    key = CARD,
                    dir = "/home/user/lighthouse",
                    name = "lighthouse",
                    path = "~/lighthouse",
                    host = "",
                    convos = listOf(convo),
                    ended = emptyList(),
                ),
                folded = false,
                waiting = 0,
                endedOpen = false,
                onFold = {},
                onEnded = {},
                foldOpen = emptySet(),
                onConvoFold = {},
                convo = { c, open, onFold ->
                    ConvoRowItem(
                        convo = c,
                        now = NOW,
                        foldOpen = open,
                        onFold = onFold,
                        onOpen = { opened.add(it) },
                        onLongPress = {},
                    )
                },
            )
        }
    }

    // Any node whose test tag starts with [prefix]: the tag of a kind, whatever its key.
    private fun tagStartingWith(prefix: String) = SemanticsMatcher("test tag starts with $prefix") { node ->
        node.config.getOrNull(SemanticsProperties.TestTag)?.startsWith(prefix) == true
    }

    @Test
    fun aDesignsTabHoldsItsName() {
        setCard(convo(listOf(designBlock())))

        composeRule.onNode(
            hasText(DESIGN_TITLE) and hasAnyAncestor(hasTestTag("design-tab:$DESIGN_ID")),
            useUnmergedTree = true,
        ).assertExists()
    }

    @Test
    fun aDesignWithABlankTitleNamesItsTabByItsId() {
        setCard(convo(listOf(designBlock(title = " "))))

        composeRule.onNode(
            hasText(DESIGN_ID) and hasAnyAncestor(hasTestTag("design-tab:$DESIGN_ID")),
            useUnmergedTree = true,
        ).assertExists()
    }

    @Test
    fun theCountOfContractsSitsBesideTheTabNotInIt() {
        setCard(convo(listOf(designBlock())))

        composeRule.onNode(hasText("1 contract"), useUnmergedTree = true).assertExists()
        composeRule.onAllNodes(
            hasText("1 contract") and hasAnyAncestor(hasTestTag("design-tab:$DESIGN_ID")),
            useUnmergedTree = true,
        ).assertCountEquals(0)
    }

    @Test
    fun theWellHoldsTheStateLineAndEveryKidRow() {
        setCard(convo(listOf(designBlock())))

        composeRule.onNode(
            hasText(STATE_LINE) and hasAnyAncestor(hasTestTag("design-well:$DESIGN_ID")),
            useUnmergedTree = true,
        ).assertExists()
        for (key in listOf("epic:$EPIC_ID", "contract:$CONTRACT_ID")) {
            composeRule.onNode(
                hasTestTag("hung:$key") and hasAnyAncestor(hasTestTag("design-well:$DESIGN_ID")),
                useUnmergedTree = true,
            ).assertExists()
        }
    }

    @Test
    fun theDesignBlockIsInsideItsConversationsBlockInsideTheProjectCard() {
        setCard(convo(listOf(designBlock())))

        composeRule.onNode(
            hasTestTag("design:$DESIGN_ID") and hasAnyAncestor(hasTestTag("convo-block:$CONVO")),
            useUnmergedTree = true,
        ).assertExists()
        composeRule.onNode(
            hasTestTag("convo-block:$CONVO") and hasAnyAncestor(hasTestTag("project:$CARD")),
            useUnmergedTree = true,
        ).assertExists()
    }

    @Test
    fun anEpicWithNoDesignIsARowOfItsConversationsBlockAndInNoWell() {
        val loose = EpicNode(epic(LOOSE_ID), EpicDetail(epic(LOOSE_ID), emptyList(), emptyList(), emptyList()), null)
        setCard(convo(listOf(designBlock()), hung = listOf(loose)))

        composeRule.onNode(
            hasTestTag("hung:epic:$LOOSE_ID") and hasAnyAncestor(hasTestTag("convo-block:$CONVO")),
            useUnmergedTree = true,
        ).assertExists()
        composeRule.onAllNodes(
            hasTestTag("hung:epic:$LOOSE_ID") and hasAnyAncestor(tagStartingWith("design-well:")),
            useUnmergedTree = true,
        ).assertCountEquals(0)
    }

    @Test
    fun aTapOnAKidInTheWellCallsOnOpenWithItsKey() {
        val opened = mutableListOf<String>()
        setCard(convo(listOf(designBlock())), opened = opened)

        composeRule.onNodeWithTag("hung:epic:$EPIC_ID", useUnmergedTree = true).performClick()

        assertEquals(listOf("epic:$EPIC_ID"), opened)
    }
}
