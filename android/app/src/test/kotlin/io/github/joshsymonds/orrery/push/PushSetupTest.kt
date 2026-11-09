package io.github.joshsymonds.orrery.push

import android.Manifest
import android.app.Application
import android.content.ComponentName
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.ActivityInfo
import android.content.pm.PackageManager
import androidx.activity.ComponentActivity
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.unifiedpush.android.connector.data.PublicKeySet
import org.unifiedpush.android.connector.keys.KeyManager

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PushSetupTest {
    private val application: Application = ApplicationProvider.getApplicationContext()
    private val distributor = "org.example.distributor"
    private val registerAction = "org.unifiedpush.android.distributor.REGISTER"

    private class FakeKeyManager : KeyManager {
        override fun decrypt(instance: String, sealed: ByteArray): ByteArray = sealed

        override fun generate(instance: String) {}

        override fun getPublicKeySet(instance: String): PublicKeySet =
            PublicKeySet("synthetic-public-key", "synthetic-auth-secret")

        override fun exists(instance: String): Boolean = true

        override fun delete(instance: String) {}
    }

    private fun createdActivity(): ComponentActivity =
        Robolectric.buildActivity(ComponentActivity::class.java).create().get()

    private fun pushSetup(activity: ComponentActivity): PushSetup =
        PushSetup(activity, keyManager = FakeKeyManager())

    private fun installFakeDistributor() {
        val receiver = ActivityInfo().apply {
            packageName = distributor
            name = "$distributor.Receiver"
            exported = true
        }
        val packageManager = shadowOf(application.packageManager)
        packageManager.addOrUpdateReceiver(receiver)
        packageManager.addIntentFilterForReceiver(
            ComponentName(distributor, receiver.name),
            IntentFilter(registerAction),
        )
    }

    private fun registerBroadcasts(): List<Intent> =
        shadowOf(application).broadcastIntents.filter { it.action == registerAction }

    @Test
    fun appDeclaresPostNotificationsPermission() {
        val packageInfo = application.packageManager.getPackageInfo(
            application.packageName,
            PackageManager.GET_PERMISSIONS,
        )

        assertTrue(packageInfo.requestedPermissions.orEmpty().contains(Manifest.permission.POST_NOTIFICATIONS))
    }

    @Test
    fun startRequestsPostNotificationsWhenNotGranted() {
        val activity = createdActivity()

        pushSetup(activity).start()

        val request = shadowOf(activity).lastRequestedPermission
        assertNotNull("expected a permission request to be launched", request)
        assertEquals(listOf(Manifest.permission.POST_NOTIFICATIONS), request.requestedPermissions.toList())
    }

    @Test
    fun startDoesNotRequestPostNotificationsWhenGranted() {
        shadowOf(application).grantPermissions(Manifest.permission.POST_NOTIFICATIONS)
        val activity = createdActivity()

        pushSetup(activity).start()

        assertNull(shadowOf(activity).lastRequestedPermission)
    }

    @Test
    fun startRegistersWithTheSingleInstalledDistributor() {
        installFakeDistributor()
        val activity = createdActivity()

        pushSetup(activity).start()

        val registers = registerBroadcasts()
        assertEquals(1, registers.size)
        assertEquals(distributor, registers.single().`package`)
    }

    @Test
    fun startSendsNoRegisterWithoutADistributorAndDoesNotThrow() {
        val activity = createdActivity()

        pushSetup(activity).start()

        assertTrue(registerBroadcasts().isEmpty())
    }
}
