package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onNodeWithText
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class SectionHeadTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun drawsTitleInLabelCaseAndItsCount() {
        composeRule.setContent { SectionHead("synthetic section", count = "3") }

        composeRule.onNodeWithText("SYNTHETIC SECTION").assertIsDisplayed()
        composeRule.onNodeWithText("3").assertIsDisplayed()
    }

    @Test
    fun drawsOnlyTheTitleWhenCountIsEmpty() {
        composeRule.setContent { SectionHead("synthetic section") }

        composeRule.onAllNodes(SemanticsMatcher.keyIsDefined(SemanticsProperties.Text)).assertCountEquals(1)
    }
}
