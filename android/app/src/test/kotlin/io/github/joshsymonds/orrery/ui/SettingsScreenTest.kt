package io.github.joshsymonds.orrery.ui

import android.content.Context
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextReplacement
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.ServerSettings
import org.junit.Assert.assertEquals
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
    }

    @get:Rule
    val composeRule = createComposeRule()

    private lateinit var context: Context

    @Before
    fun clearSettings() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
    }

    @Test
    fun prefillsFieldWithDefaultWhenNothingIsSaved() {
        setContent()

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).assert(hasText(ServerSettings.DEFAULT_SERVER_URL))
    }

    @Test
    fun saveStoresEditedAddressAndSignalsSaved() {
        var saved = false
        setContent(onSaved = { saved = true })

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement("https://orrery.example.com:8490")
        composeRule.onNodeWithText("Save").performClick()

        assertEquals("https://orrery.example.com:8490", ServerSettings(context).serverUrl)
        assertTrue(saved)
    }

    @Test
    fun savingBlankFieldRestoresDefault() {
        ServerSettings(context).saveServerUrl("https://orrery.example.com:9000")
        setContent()

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement("   ")
        composeRule.onNodeWithText("Save").performClick()

        assertEquals(ServerSettings.DEFAULT_SERVER_URL, ServerSettings(context).serverUrl)
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

    private fun setContent(onSaved: () -> Unit = {}) {
        composeRule.setContent {
            SettingsScreen(settings = ServerSettings(context), onSaved = onSaved)
        }
    }
}
