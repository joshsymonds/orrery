package io.github.joshsymonds.orrery.ui

import android.content.Context
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextReplacement
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.BuildConfig
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
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
        const val SAVED = "https://orrery.example.invalid:9000"
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
        ServerSettings(context, defaultUrl = "").saveServerUrl(SAVED)
        setContent(defaultUrl = "")

        assertFalse(asks())
    }

    @Test
    fun addressFieldHoldsTheSavedAddress() {
        ServerSettings(context, defaultUrl = BUILT_IN).saveServerUrl(SAVED)
        setContent()

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).assert(hasText(SAVED))
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
        ServerSettings(context, defaultUrl = BUILT_IN).saveServerUrl(SAVED)
        setContent()

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement("   ")
        composeRule.onNodeWithText("Save").performClick()

        assertEquals(BUILT_IN, ServerSettings(context, defaultUrl = BUILT_IN).serverUrl)
    }

    @Test
    fun titleRowReadsSettingsAndItsBackCallsOnBack() {
        var backed = false
        setContent(onBack = { backed = true })

        composeRule.onNodeWithText("Settings").assertIsDisplayed()
        composeRule.onNodeWithTag("title-back").performClick()

        assertTrue(backed)
    }

    @Test
    fun sectionsAreServerNotificationsThenAbout() {
        setContent()

        val server = topOf("Server")
        val notifications = topOf("Notifications")
        val about = topOf("About")
        assertTrue(server < notifications)
        assertTrue(notifications < about)
    }

    @Test
    fun addressFieldIsLabelledAddress() {
        setContent()

        composeRule.onNodeWithText("Address".inLabelCase()).assertIsDisplayed()
    }

    @Test
    fun saveSitsUnderServerAboveNotifications() {
        setContent()

        val server = topOf("Server")
        val save = topOfText("Save")
        val notifications = topOf("Notifications")
        assertTrue(server < save)
        assertTrue(save < notifications)
    }

    @Test
    fun fontLicenceRowReadsItsLineAndTappingItShowsTheLicenceText() {
        setContent()

        composeRule.onNodeWithText("Font licence").assertIsDisplayed()
        composeRule.onNodeWithText("Orrery Mono, a subset of Maple Mono NF").assertIsDisplayed()

        composeRule.onNodeWithText("Font licence").performClick()

        composeRule.onNodeWithText("Copyright (c) 2022, subframe7536", substring = true).performScrollTo()
            .assertIsDisplayed()
        composeRule.onNodeWithText("SIL OPEN FONT LICENSE Version 1.1", substring = true).performScrollTo()
            .assertIsDisplayed()
        assertFalse(shown(hasTestTag(ADDRESS_FIELD_TAG)))
    }

    @Test
    fun licenceTitleRowBackReturnsToSettingsWithoutLeaving() {
        var backed = false
        setContent(onBack = { backed = true })

        composeRule.onNodeWithText("Font licence").performClick()
        composeRule.onNodeWithTag("title-back").performClick()

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).assertIsDisplayed()
        assertFalse(backed)
    }

    @Test
    fun versionRowShowsTheBuildVersionName() {
        setContent()

        composeRule.onNodeWithText("Version").performScrollTo().assertIsDisplayed()
        composeRule.onNodeWithText(BuildConfig.VERSION_NAME).performScrollTo().assertIsDisplayed()
    }

    private fun asks(): Boolean = shown(hasTestTag(SERVER_ASK_TAG))

    private fun shown(matcher: SemanticsMatcher): Boolean =
        composeRule.onAllNodes(matcher).fetchSemanticsNodes().isNotEmpty()

    /** The top edge of the section head [label], in the root's coordinates. */
    private fun topOf(label: String): Float = topOfText(label.inLabelCase())

    /** The top edge of the node that reads [text], in the root's coordinates. */
    private fun topOfText(text: String): Float =
        composeRule.onNodeWithText(text).fetchSemanticsNode().boundsInRoot.top

    private fun setContent(onSaved: () -> Unit = {}, onBack: () -> Unit = {}, defaultUrl: String = BUILT_IN) {
        composeRule.setContent {
            SettingsScreen(
                settings = ServerSettings(context, defaultUrl = defaultUrl),
                onBack = onBack,
                onSaved = onSaved,
            )
        }
    }
}
