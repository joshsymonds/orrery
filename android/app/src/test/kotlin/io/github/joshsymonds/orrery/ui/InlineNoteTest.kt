package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.onNodeWithText
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class InlineNoteTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun eachNoteTextCarriesTheToneGiven() {
        composeRule.setContent {
            Column { NoteTone.entries.forEach { tone -> InlineNote(tone.name, tone) } }
        }

        NoteTone.entries.forEach { tone ->
            composeRule.onNodeWithText(tone.name).assert(SemanticsMatcher.expectValue(NoteToneKey, tone))
        }
    }
}
