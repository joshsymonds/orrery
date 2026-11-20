package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.assertIsOn
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class SwitchTest {
    private companion object {
        const val LABEL = "Everything else"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun reportsOffWhenUnchecked() {
        composeRule.setContent { Switch(checked = false, onCheckedChange = {}, label = LABEL) }

        composeRule.onNodeWithContentDescription(LABEL).assertIsOff()
    }

    @Test
    fun reportsOnWhenChecked() {
        composeRule.setContent { Switch(checked = true, onCheckedChange = {}, label = LABEL) }

        composeRule.onNodeWithContentDescription(LABEL).assertIsOn()
    }

    @Test
    fun tappingCallsOnCheckedChangeWithTheNewValue() {
        val reported = mutableListOf<Boolean>()
        var checked by mutableStateOf(false)
        composeRule.setContent {
            Switch(
                checked = checked,
                onCheckedChange = {
                    reported += it
                    checked = it
                },
                label = LABEL,
            )
        }

        composeRule.onNodeWithContentDescription(LABEL).performClick()
        composeRule.onNodeWithContentDescription(LABEL).assertIsOn()
        composeRule.onNodeWithContentDescription(LABEL).performClick()
        composeRule.onNodeWithContentDescription(LABEL).assertIsOff()

        assertEquals(listOf(true, false), reported)
    }
}
