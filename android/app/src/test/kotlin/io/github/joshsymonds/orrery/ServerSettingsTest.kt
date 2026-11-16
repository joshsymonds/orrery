package io.github.joshsymonds.orrery

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val BUILT_IN = "https://orrery.example.invalid:8490"
private const val SAVED = "https://saved.example.invalid:9000"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ServerSettingsTest {
    private lateinit var context: Context

    @Before
    fun clearSettings() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
    }

    @Test
    fun aBuildGivenNoServerCarriesNoAddress() {
        assertEquals("", BuildConfig.DEFAULT_SERVER_URL)
        assertFalse(ServerSettings(context).hasServer)
    }

    @Test
    fun usesTheBuiltInDefaultWhenNothingIsSaved() {
        val settings = ServerSettings(context, defaultUrl = BUILT_IN)

        assertEquals(BUILT_IN, settings.serverUrl)
        assertTrue(settings.hasServer)
    }

    @Test
    fun withNoDefaultAndNothingSavedThereIsNoServer() {
        val settings = ServerSettings(context, defaultUrl = "")

        assertEquals("", settings.serverUrl)
        assertFalse(settings.hasServer)
    }

    @Test
    fun savedValueIsTrimmedAndReadBackByNewInstance() {
        ServerSettings(context, defaultUrl = BUILT_IN).saveServerUrl("  $SAVED  ")

        assertEquals(SAVED, ServerSettings(context, defaultUrl = BUILT_IN).serverUrl)
    }

    @Test
    fun anAddressAnEarlierInstallSavedOutlivesAnyDefault() {
        // Where every earlier version saved the address, as it wrote it.
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit()
            .putString("server-url", SAVED).commit()

        assertEquals(SAVED, ServerSettings(context, defaultUrl = BUILT_IN).serverUrl)
        assertEquals(SAVED, ServerSettings(context, defaultUrl = "").serverUrl)
    }

    @Test
    fun blankSaveRestoresDefault() {
        val settings = ServerSettings(context, defaultUrl = BUILT_IN)
        settings.saveServerUrl(SAVED)

        settings.saveServerUrl("   ")

        assertEquals(BUILT_IN, settings.serverUrl)
    }

    @Test
    fun blankSaveWithNoDefaultLeavesNoServer() {
        val settings = ServerSettings(context, defaultUrl = "")
        settings.saveServerUrl(SAVED)

        settings.saveServerUrl("   ")

        assertFalse(settings.hasServer)
    }
}
