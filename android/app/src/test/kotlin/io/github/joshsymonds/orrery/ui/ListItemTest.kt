package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.onNodeWithText
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
class ListItemTest {
    private companion object {
        const val TITLE = "Questions and failures"
        const val SUPPORTING = "Sound and vibration"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun showsTitleAndSupportingText() {
        composeRule.setContent { ListItem(title = TITLE, supporting = SUPPORTING) }

        composeRule.onNodeWithText(TITLE).assertIsDisplayed()
        composeRule.onNodeWithText(SUPPORTING).assertIsDisplayed()
    }

    @Test
    fun showsTitleExtraAndEndContent() {
        composeRule.setContent {
            ListItem(
                title = TITLE,
                titleExtra = { Text("extra-note") },
                end = { Text("end-control") },
            )
        }

        composeRule.onNodeWithText("extra-note").assertIsDisplayed()
        composeRule.onNodeWithText("end-control").assertIsDisplayed()
    }

    @Test
    fun clickingCallsOnClick() {
        var taps = 0
        composeRule.setContent { ListItem(title = TITLE, onClick = { taps++ }) }

        composeRule.onNodeWithText(TITLE).performClick()

        assertEquals(1, taps)
    }

    @Test
    fun clickableRowIsAButton() {
        composeRule.setContent { ListItem(title = TITLE, onClick = {}) }

        composeRule.onNodeWithText(TITLE).assert(hasClickAction())
        composeRule.onNodeWithText(TITLE).assert(
            SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button),
        )
    }
}
