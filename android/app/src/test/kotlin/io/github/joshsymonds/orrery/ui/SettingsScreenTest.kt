package io.github.joshsymonds.orrery.ui

import android.content.Context
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextReplacement
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.mainThreadComposeRule
import androidx.compose.ui.test.hasTestTag
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class SettingsScreenTest {
    private companion object {
        const val ADDRESS_FIELD_TAG = "server-address"
        const val BUILT_IN = "https://orrery.example.invalid:8490"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private lateinit var context: Context

    @Before
    fun clearSettings() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
    }

    @Test
    fun prefillsFieldWithDefaultWhenNothingIsSaved() {
        setContent()

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).assert(hasText(BUILT_IN))
        assertFalse(asks())
    }

    @Test
    fun asksForTheServerWithAnEmptyFieldWhenThereIsNone() {
        setContent(defaultUrl = "")

        assertTrue(asks())
        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).assert(hasText(""))
    }

    @Test
    fun doesNotAskWhenAnAddressIsSaved() {
        ServerSettings(context, defaultUrl = "").saveServerUrl("https://orrery.example.invalid:9000")
        setContent(defaultUrl = "")

        assertFalse(asks())
    }

    @Test
    fun saveStoresEditedAddressAndSignalsSaved() {
        var saved = false
        setContent(onSaved = { saved = true })

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement("https://orrery.example.invalid:8491")
        composeRule.onNodeWithText("Save").performClick()

        assertEquals("https://orrery.example.invalid:8491", ServerSettings(context).serverUrl)
        assertTrue(saved)
    }

    @Test
    fun savingBlankFieldRestoresDefault() {
        ServerSettings(context, defaultUrl = BUILT_IN).saveServerUrl("https://orrery.example.invalid:9000")
        setContent()

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement("   ")
        composeRule.onNodeWithText("Save").performClick()

        assertEquals(BUILT_IN, ServerSettings(context, defaultUrl = BUILT_IN).serverUrl)
    }

    @Test
    fun showsFontLicenceHeadingNamingOrreryMonoAndMapleMono() {
        setContent()

        composeRule.onNodeWithText("Orrery Mono", substring = true).assertIsDisplayed()
        composeRule.onNodeWithText("Maple Mono NF", substring = true).assertIsDisplayed()
    }

    @Test
    fun showsFontLicenceTextReachableByScrolling() {
        setContent()

        composeRule.onNodeWithText("Copyright (c) 2022, subframe7536", substring = true).performScrollTo()
            .assertIsDisplayed()
        composeRule.onNodeWithText("SIL OPEN FONT LICENSE Version 1.1", substring = true).performScrollTo()
            .assertIsDisplayed()
    }

    private fun asks(): Boolean =
        composeRule.onAllNodes(hasTestTag(SERVER_ASK_TAG)).fetchSemanticsNodes().isNotEmpty()

    private fun setContent(onSaved: () -> Unit = {}, defaultUrl: String = BUILT_IN) {
        composeRule.setContent {
            SettingsScreen(settings = ServerSettings(context, defaultUrl = defaultUrl), onSaved = onSaved)
        }
    }
}
