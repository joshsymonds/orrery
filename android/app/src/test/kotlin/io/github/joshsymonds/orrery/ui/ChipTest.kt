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
class ChipTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun eachChipTextCarriesTheToneGiven() {
        composeRule.setContent {
            Column { ChipTone.entries.forEach { tone -> Chip(tone.name, tone) } }
        }

        ChipTone.entries.forEach { tone ->
            composeRule.onNodeWithText(tone.name).assert(SemanticsMatcher.expectValue(ChipToneKey, tone))
        }
    }
}
