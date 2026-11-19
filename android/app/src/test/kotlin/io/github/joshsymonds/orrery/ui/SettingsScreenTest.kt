package io.github.joshsymonds.orrery.ui

import android.content.Context
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextReplacement
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.BuildConfig
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.awaitUntilAfterIdle
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
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
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val NO_ANSWER = "No answer from that address. Nothing was changed."
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
    fun saveAsksTheTypedAddressForItsHostThenSavesAndStartsPushSetup() {
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS, hostStatus("halyard"))
            var saved = false
            val pushStartedAt = mutableListOf<String>()
            setContent(
                onSaved = { saved = true },
                startPushSetup = { pushStartedAt += ServerSettings(context).serverUrl },
            )

            composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement(server.baseUrl)
            composeRule.onNodeWithText("Save").performClick()
            composeRule.awaitUntilAfterIdle { saved }

            assertEquals(1, server.requests.count { it.rawPath == HOST_STATUS })
            assertEquals(server.baseUrl, ServerSettings(context).serverUrl)
            // Push setup starts once, after the new address is saved, so its registration reads the new address.
            assertEquals(listOf(server.baseUrl), pushStartedAt)
            composeRule.onNodeWithText("Connected to halyard.").assert(hasTone(NoteTone.Ok))
            assertFalse(addressInError())
        }
    }

    @Test
    fun aNotFoundAnswerIsAnAnswerAndConnectsByTheAddressFirstLabel() {
        FakeOrreryServer().use { server ->
            val address = server.baseUrl.replace("127.0.0.1", "localhost")
            var saved = false
            var pushStarted = false
            setContent(onSaved = { saved = true }, startPushSetup = { pushStarted = true })

            composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement(address)
            composeRule.onNodeWithText("Save").performClick()
            composeRule.awaitUntilAfterIdle { saved }

            assertEquals(address, ServerSettings(context).serverUrl)
            assertTrue(pushStarted)
            composeRule.onNodeWithText("Connected to localhost.").assert(hasTone(NoteTone.Ok))
        }
    }

    @Test
    fun anAddressThatDoesNotAnswerIsNotSavedAndSaysSo() {
        ServerSettings(context, defaultUrl = BUILT_IN).saveServerUrl(SAVED)
        FakeOrreryServer().use { server ->
            server.fail(HOST_STATUS, Code.UNAVAILABLE)
            var saved = false
            var pushStarted = false
            setContent(onSaved = { saved = true }, startPushSetup = { pushStarted = true })

            composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement(server.baseUrl)
            composeRule.onNodeWithText("Save").performClick()
            composeRule.awaitUntilAfterIdle { shown(hasText(NO_ANSWER)) }

            assertEquals(SAVED, ServerSettings(context, defaultUrl = BUILT_IN).serverUrl)
            assertFalse(saved)
            assertFalse(pushStarted)
            assertTrue(addressInError())
            composeRule.onNodeWithText(NO_ANSWER).assert(hasTone(NoteTone.Error))
        }
    }

    @Test
    fun aMalformedAddressIsNotSavedAndSaysSo() {
        ServerSettings(context, defaultUrl = BUILT_IN).saveServerUrl(SAVED)
        var saved = false
        setContent(onSaved = { saved = true })

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement("ftp://halyard.invalid")
        composeRule.onNodeWithText("Save").performClick()
        composeRule.awaitUntilAfterIdle { shown(hasText(NO_ANSWER)) }

        assertEquals(SAVED, ServerSettings(context, defaultUrl = BUILT_IN).serverUrl)
        assertFalse(saved)
        assertTrue(addressInError())
    }

    @Test
    fun saveIsDisabledWhileTheFieldIsBlank() {
        setContent()

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement("   ")
        composeRule.onNodeWithText("Save").assertIsNotEnabled()

        composeRule.onNodeWithTag(ADDRESS_FIELD_TAG).performTextReplacement(SAVED)
        composeRule.onNodeWithText("Save").assertIsEnabled()
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

    private fun addressInError(): Boolean =
        shown(hasTestTag(ADDRESS_FIELD_TAG).and(SemanticsMatcher.keyIsDefined(SemanticsProperties.Error)))

    private fun hasTone(tone: NoteTone): SemanticsMatcher = SemanticsMatcher.expectValue(NoteToneKey, tone)

    private fun hostStatus(host: String): HostStatusResponse = HostStatusResponse.newBuilder().setHost(host).build()

    private fun shown(matcher: SemanticsMatcher): Boolean =
        composeRule.onAllNodes(matcher).fetchSemanticsNodes().isNotEmpty()

    /** The top edge of the section head [label], in the root's coordinates. */
    private fun topOf(label: String): Float = topOfText(label.inLabelCase())

    /** The top edge of the node that reads [text], in the root's coordinates. */
    private fun topOfText(text: String): Float =
        composeRule.onNodeWithText(text).fetchSemanticsNode().boundsInRoot.top

    private fun setContent(
        onSaved: () -> Unit = {},
        onBack: () -> Unit = {},
        startPushSetup: () -> Unit = {},
        defaultUrl: String = BUILT_IN,
    ) {
        composeRule.setContent {
            SettingsScreen(
                settings = ServerSettings(context, defaultUrl = defaultUrl),
                onBack = onBack,
                onSaved = onSaved,
                startPushSetup = startPushSetup,
            )
        }
    }
}
