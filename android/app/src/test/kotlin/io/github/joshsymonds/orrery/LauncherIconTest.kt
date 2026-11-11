package io.github.joshsymonds.orrery

import android.content.Context
import android.content.res.XmlResourceParser
import android.graphics.Color
import android.graphics.drawable.AdaptiveIconDrawable
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.xmlpull.v1.XmlPullParser

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class LauncherIconTest {
    private val context: Context = ApplicationProvider.getApplicationContext()

    private data class VectorPath(val pathData: String?, val fillColor: Int?, val fillType: String?)

    private fun argb(hex: String): Int = Color.parseColor(hex)

    private fun resourceId(type: String, name: String): Int {
        val id = context.resources.getIdentifier(name, type, context.packageName)
        assertNotEquals("missing @$type/$name", 0, id)
        return id
    }

    private fun attribute(parser: XmlResourceParser, name: String): String? =
        parser.getAttributeValue(ANDROID_NS, name)

    private fun parse(drawable: String, onTag: (XmlResourceParser) -> Unit) {
        val parser = context.resources.getXml(resourceId("drawable", drawable))
        try {
            while (parser.next() != XmlPullParser.END_DOCUMENT) {
                if (parser.eventType == XmlPullParser.START_TAG) onTag(parser)
            }
        } finally {
            parser.close()
        }
    }

    private fun vectorPaths(drawable: String): List<VectorPath> {
        val paths = mutableListOf<VectorPath>()
        parse(drawable) { parser ->
            if (parser.name == "path") {
                paths += VectorPath(
                    attribute(parser, "pathData"),
                    attribute(parser, "fillColor")?.let { argb(it) },
                    attribute(parser, "fillType"),
                )
            }
        }
        return paths
    }

    private fun assertLayerViewport(drawable: String) {
        var sawVector = false
        parse(drawable) { parser ->
            if (parser.name == "vector") {
                sawVector = true
                assertEquals("$drawable width", DP_108, attribute(parser, "width"))
                assertEquals("$drawable height", DP_108, attribute(parser, "height"))
                assertEquals("$drawable viewportWidth", VIEWPORT_108, attribute(parser, "viewportWidth"))
                assertEquals("$drawable viewportHeight", VIEWPORT_108, attribute(parser, "viewportHeight"))
            }
            assertNotEquals("$drawable must not wrap paths in a group", "group", parser.name)
        }
        assertTrue("$drawable has a <vector> root", sawVector)
    }

    @Test
    fun applicationIconIsTheLauncherMipmap() {
        assertEquals(resourceId("mipmap", "ic_launcher"), context.applicationInfo.icon)
    }

    @Test
    fun launcherIconInflatesToAdaptiveIconWithAllThreeLayers() {
        val drawable = context.getDrawable(resourceId("mipmap", "ic_launcher"))
        val adaptive = drawable as? AdaptiveIconDrawable
        assertNotNull("launcher icon is an AdaptiveIconDrawable", adaptive)
        assertNotNull("background layer", adaptive!!.background)
        assertNotNull("foreground layer", adaptive.foreground)
        assertNotNull("monochrome layer", adaptive.monochrome)
    }

    @Test
    fun foregroundHasTheFiveConceptDPathsInOrderWithEvenOddFills() {
        val paths = vectorPaths("ic_launcher_foreground")
        assertEquals(5, paths.size)
        assertEquals(
            listOf("#11111b", "#1e1e2e", "#94e2d5", "#f9e2af", "#11111b").map { argb(it) },
            paths.map { it.fillColor },
        )
        paths.forEach { path ->
            assertEquals(EVEN_ODD, path.fillType)
            assertTrue("pathData present", !path.pathData.isNullOrBlank())
        }
        assertLayerViewport("ic_launcher_foreground")
    }

    @Test
    fun monochromeHasTheSingleConceptDPathAsOpaqueEvenOdd() {
        val paths = vectorPaths("ic_launcher_monochrome")
        assertEquals(1, paths.size)
        assertEquals(argb("#FFFFFFFF"), paths[0].fillColor)
        assertEquals(EVEN_ODD, paths[0].fillType)
        assertTrue("pathData present", !paths[0].pathData.isNullOrBlank())
        assertLayerViewport("ic_launcher_monochrome")
    }

    @Test
    fun backgroundIsOneFullViewportPathInTheAccentFill() {
        val paths = vectorPaths("ic_launcher_background")
        assertEquals(1, paths.size)
        assertEquals(argb("#FFCBA6F7"), paths[0].fillColor)
        assertTrue("pathData present", !paths[0].pathData.isNullOrBlank())
        assertLayerViewport("ic_launcher_background")
    }

    private companion object {
        const val ANDROID_NS = "http://schemas.android.com/apk/res/android"
        // The XML parser reports attributes in normalized form: the evenOdd enum as its
        // value 1, and 108dp as 108.0dip (same dimension).
        const val EVEN_ODD = "1"
        const val DP_108 = "108.0dip"
        const val VIEWPORT_108 = "108.0"
    }
}
