package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performTextReplacement
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class FieldTest {
    private companion object {
        const val FIELD_TAG = "field"
        const val LABEL = "Address"
        const val PLACEHOLDER = "https://example.invalid:8490"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun showsLabelAndPlaceholderWhileEmpty() {
        composeRule.setContent {
            Field(value = "", onValueChange = {}, label = LABEL, placeholder = PLACEHOLDER)
        }

        composeRule.onNodeWithText(LABEL.inLabelCase()).assertIsDisplayed()
        composeRule.onNodeWithText(PLACEHOLDER).assertIsDisplayed()
    }

    @Test
    fun showsValueAndHidesPlaceholderOnceFilled() {
        composeRule.setContent {
            Field(value = "https://example.invalid:9000", onValueChange = {}, label = LABEL, placeholder = PLACEHOLDER)
        }

        composeRule.onNodeWithText("https://example.invalid:9000").assertIsDisplayed()
        composeRule.onAllNodesWithText(PLACEHOLDER).assertCountEquals(0)
    }

    @Test
    fun reportsEditsToOnValueChange() {
        var text by mutableStateOf("")
        composeRule.setContent {
            Field(value = text, onValueChange = { text = it }, label = LABEL, modifier = Modifier.testTag(FIELD_TAG))
        }

        composeRule.onNodeWithTag(FIELD_TAG).performTextReplacement("https://example.invalid:8491")

        assertEquals("https://example.invalid:8491", text)
        composeRule.onNodeWithTag(FIELD_TAG).assertTextEquals("https://example.invalid:8491")
    }

    @Test
    fun carriesErrorSemanticsInError() {
        composeRule.setContent {
            Field(value = "", onValueChange = {}, label = LABEL, modifier = Modifier.testTag(FIELD_TAG), error = true)
        }

        composeRule.onNodeWithTag(FIELD_TAG).assert(SemanticsMatcher.keyIsDefined(SemanticsProperties.Error))
    }

    @Test
    fun carriesNoErrorSemanticsOutsideError() {
        composeRule.setContent {
            Field(value = "", onValueChange = {}, label = LABEL, modifier = Modifier.testTag(FIELD_TAG))
        }

        composeRule.onNodeWithTag(FIELD_TAG).assert(SemanticsMatcher.keyNotDefined(SemanticsProperties.Error))
    }
}
