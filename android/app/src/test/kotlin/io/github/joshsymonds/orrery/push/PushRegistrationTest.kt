package io.github.joshsymonds.orrery.push

import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PushRegistrationTest {
    private val server = FakeOrreryServer()
    private val path = "/v1/push/register"
    private val endpointUrl = "https://push.example.com/up/synthetic"
    private val publicKey = "synthetic-public-key"
    private val authSecret = "synthetic-auth-secret"

    @Before
    fun setUp() {
        server.respond("POST", path, 503)
    }

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun twoFailuresThenAcceptedRegistersAfterThreeIdenticalPosts() {
        val delays = mutableListOf<Long>()
        val registration = PushRegistration(OrreryClient(server.baseUrl)) { millis ->
            delays.add(millis)
            if (delays.size == 2) server.respond("POST", path, 204)
        }

        registration.register(endpointUrl, publicKey, authSecret)

        val requests = server.requests
        assertEquals(3, requests.size)
        assertEquals(1, requests.map { it.body }.toSet().size)
        requests.forEach { request ->
            assertEquals("POST", request.method)
            assertEquals(path, request.rawPath)
        }
        val body = JSONObject(requests.first().body)
        assertEquals(endpointUrl, body.getString("endpoint"))
        assertEquals(publicKey, body.getString("p256dh"))
        assertEquals(authSecret, body.getString("auth"))
        assertEquals(2, delays.size)
        assertTrue("second delay ${delays[1]} must be longer than first ${delays[0]}", delays[1] > delays[0])
    }

    @Test
    fun delaysGrowToTheCapAndStopThere() {
        val delays = mutableListOf<Long>()
        val registration = PushRegistration(OrreryClient(server.baseUrl)) { millis ->
            delays.add(millis)
            if (delays.size == 12) server.respond("POST", path, 204)
        }

        registration.register(endpointUrl, publicKey, authSecret)

        assertEquals(12, delays.size)
        assertEquals(PushRegistration.INITIAL_DELAY_MS, delays.first())
        assertEquals(PushRegistration.MAX_DELAY_MS, delays.last())
        assertTrue("no delay may exceed the cap: $delays", delays.all { it <= PushRegistration.MAX_DELAY_MS })
        assertTrue("delays must reach the cap: $delays", delays.count { it == PushRegistration.MAX_DELAY_MS } >= 2)
    }
}
