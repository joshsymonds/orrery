package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onNodeWithTag
import io.github.joshsymonds.orrery.api.EpicDecisionRow
import io.github.joshsymonds.orrery.api.EpicHistoryRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.After
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone

// 1_700_000_000 is 22:13:20 in UTC, the zone these tests run in, so its clock text is 22:13.
private const val MADE_AT = 1_700_000_000L

// decisionEntry is a decision line whose sentence is the one the server built from the decision's stored fields.
private fun decisionEntry(by: String, sentence: String, session: String = ""): EpicHistoryRow = EpicHistoryRow(
    time = MADE_AT,
    cursor = "m1",
    kind = "decision",
    sentence = sentence,
    decision = EpicDecisionRow(
        seq = 1L,
        id = "example-decision-1",
        text = "use the table",
        time = MADE_AT,
        question = "Q1",
        by = by,
        session = session,
    ),
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicHappeningsMakerTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val originalZone: TimeZone = TimeZone.getDefault()

    @Before
    fun useUtc() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
    }

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    @Test
    fun `a decision by the owner reads the sentence the server gave it, which names the owner`() {
        composeRule.setContent {
            MaterialTheme {
                JustHappened(
                    entries = listOf(decisionEntry(by = "owner", sentence = "Decision by owner: use the table")),
                    steps = emptyList(),
                    onOpenStep = {},
                )
            }
        }

        composeRule.onNodeWithTag("happened:0").assertTextEquals("22:13", "Decision by owner: use the table")
    }

    @Test
    fun `a decision by a conversation reads the sentence the server gave it, which does not name its session`() {
        composeRule.setContent {
            MaterialTheme {
                JustHappened(
                    entries = listOf(
                        decisionEntry(
                            by = "conversation",
                            sentence = "Decision by conversation: use the table",
                            session = "example-session-1",
                        ),
                    ),
                    steps = emptyList(),
                    onOpenStep = {},
                )
            }
        }

        composeRule.onNodeWithTag("happened:0")
            .assertTextEquals("22:13", "Decision by conversation: use the table")
    }

    @Test
    fun `a decision by orreryd reads the sentence the server gave it, which names orreryd`() {
        composeRule.setContent {
            MaterialTheme {
                JustHappened(
                    entries = listOf(decisionEntry(by = "orreryd", sentence = "Decision by orreryd: use the table")),
                    steps = emptyList(),
                    onOpenStep = {},
                )
            }
        }

        composeRule.onNodeWithTag("happened:0").assertTextEquals("22:13", "Decision by orreryd: use the table")
    }

    @Test
    fun `a decision with no maker reads the sentence the server gave it, which names no maker`() {
        composeRule.setContent {
            MaterialTheme {
                JustHappened(
                    entries = listOf(decisionEntry(by = "", sentence = "Decision: use the table")),
                    steps = emptyList(),
                    onOpenStep = {},
                )
            }
        }

        composeRule.onNodeWithTag("happened:0").assertTextEquals("22:13", "Decision: use the table")
    }
}
