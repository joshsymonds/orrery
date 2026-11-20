package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val EPIC = "lighthouse/rota-export"

// The block the conversation the epic left gets, as orreryd writes it for a move to session sess-lamp-11 by the owner.
private val LEFT_BLOCK = """
<orrery-event kind="moved" id="epic:$EPIC:A1">
Epic $EPIC (rota-export) moved to session sess-lamp-11, by the owner.
</orrery-event>
""".trimIndent()

// The block the conversation that took the epic gets: the steering prompt, whose first line is its instruction.
private val TAKER_BLOCK = """
<orrery-event kind="moved" id="epic:$EPIC:A1">
orreryd builds this epic; this session steers it by answering its questions and events, and changes nothing in the repository.
Epic: $EPIC
</orrery-event>
""".trimIndent()

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class MoveLinesTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun moveLineOfReadsTheLeftBlockAsLeftWithEpicToAndBy() {
        assertEquals(MoveLineEvent.Left(EPIC, "sess-lamp-11", "the owner"), moveLineOf(LEFT_BLOCK))
    }

    @Test
    fun moveLineOfReadsAMoverThatIsASessionAsItIsNamed() {
        val block = """
            <orrery-event kind="moved" id="epic:$EPIC:A2">
            Epic $EPIC (rota-export) moved to session sess-lamp-11, by session sess-14.
            </orrery-event>
        """.trimIndent()

        assertEquals(MoveLineEvent.Left(EPIC, "sess-lamp-11", "session sess-14"), moveLineOf(block))
    }

    @Test
    fun moveLineOfReadsTheTakerBlockAsTookWithEpic() {
        assertEquals(MoveLineEvent.Took(EPIC), moveLineOf(TAKER_BLOCK))
    }

    @Test
    fun moveLineOfReadsAnyOtherTextAsNull() {
        val plainPrompt = "synthetic prompt"
        val questionBlock = """
            <orrery-event kind="question" id="epic:$EPIC:Q1">
            Epic $EPIC asks a synthetic question.
            </orrery-event>
        """.trimIndent()
        val malformedId = """
            <orrery-event kind="moved" id="epic:$EPIC">
            Epic $EPIC (rota-export) moved to session sess-lamp-11, by the owner.
            </orrery-event>
        """.trimIndent()

        assertNull(moveLineOf(plainPrompt))
        assertNull(moveLineOf(questionBlock))
        assertNull(moveLineOf(malformedId))
    }

    @Test
    fun conversationDrawsTheLeftLineWithSlugSessionAndYouAndNoPromptBlock() {
        setList(LEFT_BLOCK)

        composeRule.onNodeWithTag("move-line")
            .assertIsDisplayed()
            .assertTextEquals("rota-export moved to session sess-lamp-11, by you. Its questions go there now.")
        composeRule.onAllNodesWithTag("move-line").assertCountEquals(1)
        composeRule.onAllNodesWithText("YOU").assertCountEquals(0)
        composeRule.onAllNodesWithText("orrery-event", substring = true).assertCountEquals(0)
    }

    @Test
    fun conversationDrawsTheTakerLineWithSlugAndNoPromptBlock() {
        setList(TAKER_BLOCK)

        composeRule.onNodeWithTag("move-line")
            .assertIsDisplayed()
            .assertTextEquals("Took over rota-export. Its questions and its ending come here now.")
        composeRule.onAllNodesWithTag("move-line").assertCountEquals(1)
        composeRule.onAllNodesWithText("YOU").assertCountEquals(0)
        composeRule.onAllNodesWithText("orrery-event", substring = true).assertCountEquals(0)
    }

    private fun setList(text: String) {
        composeRule.setContent {
            MaterialTheme {
                ConversationView(entries = listOf(ConvoEntry.Prompt(text)))
            }
        }
    }
}
