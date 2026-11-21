package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import io.github.joshsymonds.orrery.api.ConversationItem
import io.github.joshsymonds.orrery.api.StepFacts
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.time.ZoneId
import java.util.TimeZone

// Unix seconds, 22:13:20 UTC on 14 November 2023, and the next day's instant a day later.
private const val T1 = 1_700_000_000L
private const val ONE_DAY = 86_400L
private const val NOW_SAME_DAY = T1
private val UTC: ZoneId = ZoneId.of("UTC")
private const val SLUG = "rota"
private const val EPIC = "synthetic/$SLUG"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ShapesTranscriptTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val originalZone: TimeZone = TimeZone.getDefault()

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    private fun item(
        kind: String,
        time: Long = T1,
        text: String = "",
        tool: String = "Read",
        input: String = "synthetic input",
        id: String = "",
        error: Boolean = false,
        notice: String = "",
        trigger: String = "",
        tokens: Long = 0L,
        promptId: String = "",
        sender: String = "",
        round: Int = 0,
        epic: String = "",
        stepFacts: StepFacts? = null,
    ) = ConversationItem(
        kind = kind,
        time = time,
        text = text,
        tool = tool,
        input = input,
        id = id,
        error = error,
        notice = notice,
        trigger = trigger,
        tokens = tokens,
        promptId = promptId,
        sender = sender,
        round = round,
        epic = epic,
        stepFacts = stepFacts,
    )

    private fun prompt(sender: String, text: String = "synthetic prompt", promptId: String = "") =
        item("prompt", text = text, sender = sender, promptId = promptId)

    private fun block(kind: String, id: String, body: String, attributes: String = "") =
        "<orrery-event kind=\"$kind\" id=\"$id\"$attributes>\n$body\n</orrery-event>"

    private fun fold(vararg items: ConversationItem, now: Long = NOW_SAME_DAY) =
        foldConversation(items.toList(), now = now, zone = UTC)

    private fun call(id: String, name: String = "Read") = ToolCall(id, name, "synthetic input", null, false, T1)

    // ---- Folding: each kind folds to its entry. ----

    @Test
    fun ownerPromptFoldsToAPromptWithItsTimeAndPromptId() {
        assertEquals(
            listOf(ConvoEntry.Prompt("synthetic prompt", T1, "p-1")),
            fold(prompt("owner", promptId = "p-1")),
        )
    }

    @Test
    fun promptWithNoSenderFoldsToAPrompt() {
        assertEquals(listOf(ConvoEntry.Prompt("synthetic prompt", T1)), fold(prompt("")))
    }

    @Test
    fun stepEndEventFoldsToAStepEndWithItsFacts() {
        val facts = StepFacts(rounds = 1, seconds = 480, counted = true, files = 2, added = 96, removed = 4)
        val text = block(
            "step_end",
            "step:s-1",
            "Wrote the CSV writer.",
            " end=\"done\" status=\"done\" agent=\"csv-writer\"",
        )

        assertEquals(
            listOf(ConvoEntry.StepEnd("csv-writer", "done", "Wrote the CSV writer.", facts, T1, "p-2")),
            fold(item("prompt", text = text, sender = "event", promptId = "p-2", stepFacts = facts)),
        )
    }

    @Test
    fun epicQuestionParkReleaseAndEndFoldToEpicEvents() {
        val kinds = listOf("question", "park", "release", "end")
        val items = kinds.mapIndexed { index, kind ->
            item(
                "prompt",
                text = block(kind, "epic:$EPIC:e-$index", "Synthetic sentence. More."),
                sender = "event",
                promptId = "p-$index",
            )
        }

        assertEquals(
            kinds.mapIndexed { index, kind ->
                ConvoEntry.EpicEvent(kind, EPIC, "Synthetic sentence.", T1, "p-$index")
            },
            fold(*items.toTypedArray()),
        )
    }

    @Test
    fun epicCreateWithANonErrorResultFoldsToCreated() {
        val entries = fold(
            item("tool", tool = "epic_create", id = "c-1", epic = EPIC),
            item("result", id = "c-1", text = "synthetic ok"),
        )

        assertEquals(listOf(ConvoEntry.Created(EPIC, T1)), entries)
    }

    @Test
    fun epicCreateWithAnErrorResultFoldsToAnOrdinaryCall() {
        val entries = fold(
            item("tool", tool = "epic_create", id = "c-1", epic = EPIC),
            item("result", id = "c-1", text = "synthetic failure", error = true),
        )

        assertEquals(listOf(ConvoEntry.Tool(ToolCall("c-1", "epic_create", "synthetic input", "synthetic failure", true, T1))), entries)
    }

    @Test
    fun movedEventFoldsToAMoveAndOtherEventsToAnEvent() {
        val moved = block("moved", "epic:$EPIC:A3", "Synthetic steering for the conversation that took it.")
        val other = block("mystery", "epic:$EPIC:x", "Synthetic body.")

        assertEquals(
            listOf(
                ConvoEntry.Move(MoveLineEvent.Took(EPIC), T1, "p-3"),
                ConvoEntry.Event(other, T1, "p-4"),
            ),
            fold(
                item("prompt", text = moved, sender = "event", promptId = "p-3"),
                item("prompt", text = other, sender = "event", promptId = "p-4"),
            ),
        )
    }

    @Test
    fun cronFoldsToACronEvent() {
        assertEquals(
            listOf(ConvoEntry.Cron("Nightly sweep.", T1, "p-5")),
            fold(prompt("cron", text = "Nightly sweep.", promptId = "p-5")),
        )
    }

    @Test
    fun briefAndRoundZeroEngineFoldToEngineFolds() {
        assertEquals(
            listOf(
                ConvoEntry.Brief("Brief text.", T1, "p-6"),
                ConvoEntry.Engine("Engine text.", T1, "p-7"),
            ),
            fold(
                item("prompt", text = "Brief text.", sender = "brief", promptId = "p-6"),
                item("prompt", text = "Engine text.", sender = "engine", round = 0, promptId = "p-7"),
            ),
        )
    }

    @Test
    fun engineRoundOneOrLaterFoldsToACheckWithItsRound() {
        assertEquals(
            listOf(ConvoEntry.Check(2, "Send back.", T1, "p-8")),
            fold(item("prompt", text = "Send back.", sender = "engine", round = 2, promptId = "p-8")),
        )
    }

    @Test
    fun replyFoldsToAReplyWithItsTime() {
        assertEquals(listOf(ConvoEntry.Reply("synthetic reply", T1)), fold(item("reply", text = "synthetic reply")))
    }

    @Test
    fun threeCallsFoldToOneGroupAndOneCallToATool() {
        assertEquals(
            listOf(ConvoEntry.Group(listOf(call("a"), call("b"), call("c")))),
            fold(item("tool", id = "a"), item("tool", id = "b"), item("tool", id = "c")),
        )
        assertEquals(listOf(ConvoEntry.Tool(call("a"))), fold(item("tool", id = "a")))
    }

    @Test
    fun interruptedFoldsToAnInterruptedRule() {
        assertEquals(listOf(ConvoEntry.Interrupted(T1)), fold(item("interrupted")))
    }

    @Test
    fun compactionFoldsToACompactionWithItsTriggerTokensAndSummary() {
        assertEquals(
            listOf(ConvoEntry.Compaction("auto", 611_000L, "synthetic summary", T1)),
            fold(item("compaction", text = "synthetic summary", trigger = "auto", tokens = 611_000L)),
        )
    }

    @Test
    fun noticeFoldsToANotice() {
        assertEquals(
            listOf(ConvoEntry.Notice("task", "synthetic task", false, T1)),
            fold(item("notice", notice = "task", text = "synthetic task")),
        )
    }

    @Test
    fun agentFinishedFoldsToATaskWithItsEndWordFromItsNotice() {
        assertEquals(
            listOf(
                ConvoEntry.Task("agent", "Explore", "synthetic description", "done", T1),
                ConvoEntry.Task("agent", "Explore", "synthetic description", "failed", T1),
            ),
            fold(
                item("agent_finished", tool = "Explore", text = "synthetic description"),
                item("agent_finished", tool = "Explore", text = "synthetic description", notice = "failed"),
            ),
        )
    }

    @Test
    fun anUnknownKindFoldsToNothing() {
        assertEquals(emptyList<ConvoEntry>(), fold(item("thinking", text = "synthetic thought")))
    }

    // ---- Day rules: a change of local day (DL-G6), judged against the injected now. ----

    @Test
    fun aFirstItemOfAnEarlierDayOpensOnItsDayRule() {
        assertEquals(
            listOf(ConvoEntry.Day(T1), ConvoEntry.Prompt("synthetic prompt", T1)),
            fold(prompt("owner"), now = T1 + ONE_DAY),
        )
    }

    @Test
    fun aFirstItemOfTodayDrawsNoDayRule() {
        assertEquals(listOf(ConvoEntry.Prompt("synthetic prompt", T1)), fold(prompt("owner"), now = NOW_SAME_DAY))
    }

    @Test
    fun aChangeOfLocalDayBetweenItemsDrawsOneDayRuleBeforeTheLaterOne() {
        assertEquals(
            listOf(
                ConvoEntry.Reply("synthetic reply", T1),
                ConvoEntry.Day(T1 + ONE_DAY),
                ConvoEntry.Prompt("synthetic prompt", T1 + ONE_DAY),
            ),
            fold(
                item("reply", text = "synthetic reply"),
                prompt("owner").copy(time = T1 + ONE_DAY),
                now = T1,
            ),
        )
    }

    @Test
    fun anItemWithNoTimeOpensNoDayRule() {
        assertEquals(
            listOf(ConvoEntry.Prompt("synthetic prompt", 0L)),
            fold(prompt("owner").copy(time = 0L), now = T1 + ONE_DAY),
        )
    }

    // ---- Drawing: each entry draws its tag, its tone and its words. ----

    @Test
    fun ownerPromptDrawsAYouBlockWithItsTimeAndYouRail() {
        setEntries(ConvoEntry.Prompt("synthetic prompt", T1, "p-1"))

        composeRule.onNodeWithTag("block:you").assertIsDisplayed()
        composeRule.onNodeWithText("YOU").assertIsDisplayed()
        composeRule.onNodeWithTag("at").assertIsDisplayed()
        assertRailTone("block:you", "you")
    }

    @Test
    fun engineEventDrawsAnEventBlockWithTheEngineRail() {
        setEntries(ConvoEntry.Event("synthetic event", T1, "p-1"))

        composeRule.onNodeWithTag("block:event").assertIsDisplayed()
        composeRule.onNodeWithText("EVENT").assertIsDisplayed()
        assertRailTone("block:event", "engine")
    }

    @Test
    fun cronDrawsAnEventBlockLabelledCron() {
        setEntries(ConvoEntry.Cron("Nightly sweep.", T1, "p-2"))

        composeRule.onNodeWithTag("block:event").assertIsDisplayed()
        composeRule.onNodeWithText("CRON").assertIsDisplayed()
        assertRailTone("block:event", "engine")
    }

    @Test
    fun stepEndDrawsAStepEndBlockWithItsVerdictRailAndFacts() {
        val facts = StepFacts(rounds = 1, seconds = 480, counted = true, files = 2, added = 96, removed = 4)
        setEntries(ConvoEntry.StepEnd("csv-writer", "done", "Wrote the CSV writer.", facts, T1, "p-3"))

        composeRule.onNodeWithTag("block:stepend").assertIsDisplayed()
        composeRule.onNodeWithText("STEP ENDED").assertIsDisplayed()
        composeRule.onNodeWithText("+96").assertIsDisplayed()
        assertRailTone("block:stepend", "ok")
    }

    @Test
    fun epicQuestionDrawsAnAttentionEpicEventWithItsSlug() {
        setEntries(ConvoEntry.EpicEvent("question", EPIC, "Which rota?", T1, "p-4"))

        composeRule.onNodeWithTag("block:epicevent").assertIsDisplayed()
        composeRule.onNodeWithText("EPIC").assertIsDisplayed()
        composeRule.onNodeWithText("Which rota?").assertIsDisplayed()
        composeRule.onNodeWithText(SLUG).assertIsDisplayed()
        composeRule.onNodeWithTag("icon:question").assertIsDisplayed()
        assertRailTone("block:epicevent", "attention")
    }

    @Test
    fun epicEndDrawsAQuietEpicEventWithTheStopIcon() {
        setEntries(ConvoEntry.EpicEvent("end", EPIC, "Ended the epic.", T1, "p-5"))

        composeRule.onNodeWithTag("icon:stop").assertIsDisplayed()
        assertRailTone("block:epicevent", "quiet")
    }

    @Test
    fun createdDrawsAnEngineEpicEventWithTheGaugeIcon() {
        setEntries(ConvoEntry.Created(EPIC, T1))

        composeRule.onNodeWithTag("block:epicevent").assertIsDisplayed()
        composeRule.onNodeWithText("Created the epic from the accepted contract.").assertIsDisplayed()
        composeRule.onNodeWithTag("icon:gauge").assertIsDisplayed()
        assertRailTone("block:epicevent", "engine")
    }

    @Test
    fun movedDrawsTheMoveLine() {
        setEntries(ConvoEntry.Move(MoveLineEvent.Took(EPIC), T1, "p-6"))

        composeRule.onNodeWithTag("move-line").assertIsDisplayed()
    }

    @Test
    fun briefDrawsAnEngineFoldWithFromTheEngineAndNoRail() {
        setEntries(ConvoEntry.Brief("Brief text.", T1, "p-7"))

        composeRule.onNodeWithTag("line:fold").assertIsDisplayed()
            .assertTextContains("Brief")
            .assertTextContains("from the engine")
        composeRule.onAllNodesWithTag("rail", useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    fun engineRoundZeroDrawsAnEngineFoldNamedByItsFirstLine() {
        setEntries(ConvoEntry.Engine("First line.\nSecond line.", T1, "p-8"))

        composeRule.onNodeWithTag("line:fold").assertIsDisplayed()
            .assertTextContains("Engine")
            .assertTextContains("First line.")
    }

    @Test
    fun checkDrawsACheckBlockWithItsRoundAndTheEngineRail() {
        setEntries(ConvoEntry.Check(2, "Send back.", T1, "p-9"))

        composeRule.onNodeWithTag("block:checks").assertIsDisplayed()
        composeRule.onNodeWithText("CHECKS").assertIsDisplayed()
        composeRule.onNodeWithText("round 2 from the engine").assertIsDisplayed()
        assertRailTone("block:checks", "engine")
    }

    @Test
    fun replyDrawsItsTextUnderTheReplyTag() {
        setEntries(ConvoEntry.Reply("synthetic reply", T1))

        composeRule.onNodeWithTag("reply").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic reply").assertIsDisplayed()
    }

    @Test
    fun toolDrawsOneToolLineWithItsNameAndInput() {
        setEntries(ConvoEntry.Tool(call("a", name = "Read")))

        composeRule.onNodeWithTag("line:tool").assertIsDisplayed().assertTextContains("Read")
    }

    @Test
    fun threeCallsDrawOneRunLineThatNamesThem() {
        setEntries(ConvoEntry.Group(listOf(call("a"), call("b"), call("c"))))

        composeRule.onNodeWithTag("line:run").assertIsDisplayed().assertTextContains("3 tool calls")
    }

    @Test
    fun contractLineDrawsTheContractLineInPlaceOfAToolLine() {
        setEntries(ConvoEntry.ContractLine(ToolCall("k", "Write", "synthetic", "ok", false, T1, "C-1")))

        composeRule.onAllNodesWithTag("line:tool").assertCountEquals(0)
        composeRule.onNodeWithTag("contract-line:C-1").assertIsDisplayed()
    }

    @Test
    fun interruptedDrawsAHaltRuleWithItsLabel() {
        setEntries(ConvoEntry.Interrupted(T1))

        composeRule.onNodeWithTag("rule:interrupted").assertIsDisplayed().assertTextContains("INTERRUPTED BY YOU")
        assertEquals(OrreryTokens.Role.halt, ruleToneOf("rule:interrupted"))
    }

    @Test
    fun compactionDrawsACompactionRule() {
        setEntries(ConvoEntry.Compaction("auto", 611_000L, "synthetic summary", T1))

        composeRule.onNodeWithTag("rule:compaction").assertIsDisplayed().assertTextContains("COMPACTED")
    }

    @Test
    fun dayDrawsADayRuleWithTheDayAsItsLabel() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
        setEntries(ConvoEntry.Day(T1))

        composeRule.onNodeWithTag("rule:day").assertIsDisplayed().assertTextContains("Tuesday 14 November".inLabelCase())
    }

    @Test
    fun noticeDrawsANoticeLineWithItsLabelAndText() {
        setEntries(ConvoEntry.Notice("task", "synthetic task", false, T1))

        composeRule.onNodeWithTag("line:notice").assertIsDisplayed()
            .assertTextContains("TASK")
            .assertTextContains("synthetic task")
    }

    @Test
    fun taskDrawsATaskLineWithItsEndWord() {
        setEntries(ConvoEntry.Task("agent", "Explore", "synthetic description", "done", T1))

        composeRule.onNodeWithTag("line:task").assertIsDisplayed()
            .assertTextContains("Explore")
            .assertTextContains("finished")
    }

    @Test
    fun aNewRuleDrawsBeforeTheFirstEntryNewerThanTheSeenTime() {
        setEntries(ConvoEntry.Reply("older", 1_000L), ConvoEntry.Reply("newer", 3_000L), seen = 2_000L)

        composeRule.onNodeWithTag("rule:new").assertIsDisplayed().assertTextContains("NEW SINCE YOU LOOKED")
    }

    @Test
    fun onlyBlocksCarryARail() {
        setEntries(
            ConvoEntry.Prompt("synthetic prompt", T1, "p-1"),
            ConvoEntry.Reply("synthetic reply", T1),
            ConvoEntry.Tool(call("a")),
            ConvoEntry.Interrupted(T1),
        )

        composeRule.onAllNodesWithTag("rail", useUnmergedTree = true).assertCountEquals(1)
    }

    private fun setEntries(vararg entries: ConvoEntry, seen: Long? = null) {
        composeRule.setContent {
            MaterialTheme {
                ConversationView(entries = entries.toList(), seen = seen, now = NOW_SAME_DAY)
            }
        }
    }

    private fun assertRailTone(blockTag: String, tone: String) {
        composeRule.onNodeWithTag(blockTag).assert(
            hasAnyDescendant(hasTestTag("rail") and SemanticsMatcher.expectValue(ShapeToneKey, tone)),
        )
    }

    private fun ruleToneOf(tag: String) =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config[RuleToneKey]
}
