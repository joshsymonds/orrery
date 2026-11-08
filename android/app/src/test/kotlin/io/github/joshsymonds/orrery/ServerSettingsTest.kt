package io.github.joshsymonds.orrery

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

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
    fun defaultConstantIsTheDocumentedServerAddress() {
        assertEquals("https://vermissian.tail82223.ts.net:8490", ServerSettings.DEFAULT_SERVER_URL)
    }

    @Test
    fun usesDefaultWhenNothingIsSaved() {
        assertEquals(ServerSettings.DEFAULT_SERVER_URL, ServerSettings(context).serverUrl)
    }

    @Test
    fun savedValueIsTrimmedAndReadBackByNewInstance() {
        ServerSettings(context).saveServerUrl("  https://orrery.example.com:9000  ")

        assertEquals("https://orrery.example.com:9000", ServerSettings(context).serverUrl)
    }

    @Test
    fun blankSaveRestoresDefault() {
        val settings = ServerSettings(context)
        settings.saveServerUrl("https://orrery.example.com:9000")

        settings.saveServerUrl("   ")

        assertEquals(ServerSettings.DEFAULT_SERVER_URL, settings.serverUrl)
    }
}
