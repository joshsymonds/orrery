package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The rows of the inbox tree on Android: a conversation with what hangs under it, drawn from synthetic panes, epics and
// contracts at a synthetic clock. Queries read the unmerged tree, since a clickable row merges its children.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class InboxTreeRowsTest {
    private companion object {
        const val NOW = 100_000L
        const val EPIC_ID = "lighthouse/rota-export"
        const val CONTRACT_ID = "lighthouse/tide-alerts"
        const val CONVO_KEY = "session:s1"
        const val UNSEEN = "new since you looked"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun paneOf(
        label: String = "Lamp",
        state: String = "working",
        summary: String = "",
        changed: Long = NOW - 120,
        newestReply: Long = 0L,
        seen: Long = 0L,
    ) = Pane(
        id = "s1",
        host = "laptop",
        session = "",
        window = 0,
        pane = 0,
        cwd = "/home/user/lighthouse",
        command = "",
        state = state,
        summary = summary,
        label = label,
        changed = changed,
        seen = seen,
        question = "",
        questionHash = "",
        newestReply = newestReply,
        headless = true,
        orreryId = "conv-1",
    )

    private fun convo(pane: Pane? = paneOf(), standIn: StandIn = StandIn.NONE, hung: List<Hung> = emptyList()) =
        Convo(key = CONVO_KEY, pane = pane, standIn = standIn, again = false, hung = hung)

    private fun epic(steps: List<StepRow> = emptyList()) = EpicRow(
        id = EPIC_ID,
        repository = "/home/user/lighthouse",
        slug = "rota-export",
        outcome = "accepted",
        changed = 0L,
        live = true,
        runningSteps = steps,
        endedSteps = 0,
        problem = "",
        phaseAt = NOW - 49 * 60,
    )

    private fun detail(epic: EpicRow, questions: List<EpicQuestionRow> = emptyList()) = EpicDetail(
        epic = epic,
        tasks = emptyList(),
        steps = emptyList(),
        questions = questions,
    )

    private fun epicNode(epic: EpicRow = epic(), questions: List<EpicQuestionRow> = emptyList()) =
        EpicNode(epic, detail(epic, questions), contract = null)

    private fun step(id: String, held: Boolean = false) = StepRow(
        id = id,
        task = "build",
        agent = "",
        end = "",
        status = "",
        held = held,
        started = 0L,
        ended = 0L,
        doing = "running the suite",
        checks = emptyList(),
        reason = "",
    )

    private fun contractNode(state: String = "proposed") = ContractNode(
        ContractRow(
            id = CONTRACT_ID,
            repository = "/home/user/lighthouse",
            title = "tide alerts",
            care = "",
            state = state,
            version = 3L,
            openDecisions = 0L,
            conversation = "conv-1",
            created = 0L,
            changed = NOW - 180,
        ),
    )

    private fun setRow(
        convo: Convo,
        opened: MutableList<String> = mutableListOf(),
        pressed: MutableList<SheetRow> = mutableListOf(),
    ) {
        composeRule.setContent {
            ConvoRowItem(convo = convo, now = NOW, onOpen = { opened.add(it) }, onLongPress = { pressed.add(it) })
        }
    }

    private fun assertTagShown(tag: String) = composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).assertCountEquals(1)

    private fun assertTagAbsent(tag: String) = composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).assertCountEquals(0)

    private fun assertTextShown(text: String) = composeRule.onAllNodesWithText(text, useUnmergedTree = true).assertCountEquals(1)

    @Test
    fun aConversationRowShowsItsTitleItsAgeAndItsSecondLine() {
        setRow(convo(pane = paneOf(label = "Lamp", state = "done", summary = "Waiting on a synthetic plan.")))

        assertTagShown("convo:$CONVO_KEY")
        assertTextShown("Lamp")
        assertTextShown("2m")
        assertTextShown("Waiting on a synthetic plan.")
    }

    @Test
    fun aRowNewerThanYouLookedAtItDrawsTheNewDot() {
        setRow(convo(pane = paneOf(newestReply = NOW - 60, seen = NOW - 600)))

        assertTagShown("convo:$CONVO_KEY")
        composeRule.onAllNodes(hasContentDescription(UNSEEN), useUnmergedTree = true).assertCountEquals(1)
    }

    @Test
    fun aRowYouHaveSeenDrawsNoNewDot() {
        setRow(convo(pane = paneOf(newestReply = NOW - 600, seen = NOW - 60)))

        assertTagShown("convo:$CONVO_KEY")
        composeRule.onAllNodes(hasContentDescription(UNSEEN), useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    fun aWaitingRowsGlyphNamesItsStateInWords() {
        setRow(convo(pane = paneOf(state = "waiting")))

        composeRule.onAllNodes(hasContentDescription("waiting on you"), useUnmergedTree = true).assertCountEquals(1)
    }

    @Test
    fun aStandInDrawsItsWordsInPlaceOfARow() {
        setRow(convo(pane = null, standIn = StandIn.UNLISTED))

        assertTextShown("its conversation is not listed")
        assertTagAbsent("convo:$CONVO_KEY")
    }

    @Test
    fun anEpicHangsUnderItsConversationWithItsGaugeNameWordsAndQuestion() {
        val asked = listOf(EpicQuestionRow(id = "Q1", text = "Which branch?", turn = 1L, asked = NOW - 120))
        setRow(convo(hung = listOf(epicNode(epic(), asked))))

        assertTagShown("hung:epic:$EPIC_ID")
        assertTextShown("rota-export")
        assertTextShown("planning · 49 min · 0 of 0 tasks done")
        assertTextShown("? waits on your answer · 2 min")
        composeRule.onNodeWithTag("gauge:$EPIC_ID", useUnmergedTree = true).assert(hasContentDescription("no tasks yet"))
    }

    @Test
    fun aProposedContractDrawsItsMarkNameVersionWordsAndAsk() {
        setRow(convo(hung = listOf(contractNode())))

        assertTagShown("hung:contract:$CONTRACT_ID")
        assertTagShown("mark:contract:$CONTRACT_ID")
        assertTextShown("tide-alerts")
        assertTextShown("v3")
        assertTextShown("contract · proposed · 3 min ago")
        assertTextShown("? waits to be read and accepted")
        assertTagAbsent("gauge:$CONTRACT_ID")
    }

    @Test
    fun thingsHungUnderAConversationDrawInTheirOrder() {
        setRow(convo(hung = listOf(epicNode(), contractNode())))

        val hungTags = composeRule.onAllNodes(
            SemanticsMatcher("a row hung under a conversation") { node ->
                node.config.getOrNull(SemanticsProperties.TestTag)?.startsWith("hung:") == true
            },
            useUnmergedTree = true,
        ).fetchSemanticsNodes().map { it.config[SemanticsProperties.TestTag] }

        assertEquals(listOf("hung:epic:$EPIC_ID", "hung:contract:$CONTRACT_ID"), hungTags)
    }

    @Test
    fun anEpicShowsAtMostFourRunningStepsAndALineForTheRest() {
        val steps = (1..6).map { step("s$it") }
        setRow(convo(hung = listOf(epicNode(epic(steps)))))

        for (id in listOf("s1", "s2", "s3", "s4")) assertTagShown("step:$id")
        for (id in listOf("s5", "s6")) assertTagAbsent("step:$id")
        assertTextShown("2 more")
    }

    @Test
    fun aTapOnAConversationHungThingOrStepCallsOnOpenWithItsKey() {
        val opened = mutableListOf<String>()
        setRow(convo(hung = listOf(epicNode(epic(listOf(step("s1")))))), opened = opened)

        composeRule.onNodeWithTag("convo:$CONVO_KEY", useUnmergedTree = true).performClick()
        composeRule.onNodeWithTag("hung:epic:$EPIC_ID", useUnmergedTree = true).performClick()
        composeRule.onNodeWithTag("step:s1", useUnmergedTree = true).performClick()

        assertEquals(listOf(CONVO_KEY, "epic:$EPIC_ID", "step:s1"), opened)
    }

    @Test
    fun aLongPressOnAConversationRowCallsOnLongPressWithItsPane() {
        val opened = mutableListOf<String>()
        val pressed = mutableListOf<SheetRow>()
        val pane = paneOf()
        setRow(convo(pane = pane), opened = opened, pressed = pressed)

        composeRule.onNodeWithTag("convo:$CONVO_KEY", useUnmergedTree = true).performTouchInput { longClick() }

        assertEquals(listOf(SheetRow.Conversation(pane)), pressed)
        assertEquals(emptyList<String>(), opened)
    }

    @Test
    fun aLongPressOnAnEpicHungUnderAConversationCallsOnLongPressWithTheEpic() {
        val opened = mutableListOf<String>()
        val pressed = mutableListOf<SheetRow>()
        val epic = epic()
        setRow(convo(hung = listOf(EpicNode(epic, detail(epic), contract = null))), opened = opened, pressed = pressed)

        composeRule.onNodeWithTag("hung:epic:$EPIC_ID", useUnmergedTree = true).performTouchInput { longClick() }

        assertEquals(listOf(SheetRow.Epic(epic)), pressed)
        assertEquals(emptyList<String>(), opened)
    }
}
