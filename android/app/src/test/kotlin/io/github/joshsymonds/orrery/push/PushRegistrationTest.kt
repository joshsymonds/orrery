package io.github.joshsymonds.orrery.push

import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.gen.orrery.v1.RegisterRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RegisterResponse
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
    private val path = "/orrery.v1.Push/Register"
    private val endpointUrl = "https://push.example.com/up/synthetic"
    private val publicKey = "synthetic-public-key"
    private val authSecret = "synthetic-auth-secret"

    @Before
    fun setUp() {
        server.fail(path, Code.UNAVAILABLE)
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
            if (delays.size == 2) server.respond(path, RegisterResponse.getDefaultInstance())
        }

        registration.register(endpointUrl, publicKey, authSecret)

        val requests = server.requests
        assertEquals(3, requests.size)
        assertEquals(1, requests.map { it.body.toList() }.toSet().size)
        requests.forEach { request ->
            assertEquals("POST", request.method)
            assertEquals(path, request.rawPath)
        }
        assertEquals(
            RegisterRequest.newBuilder().setEndpoint(endpointUrl).setP256Dh(publicKey).setAuth(authSecret).build(),
            RegisterRequest.parseFrom(requests.first().body),
        )
        assertEquals(2, delays.size)
        assertTrue("second delay ${delays[1]} must be longer than first ${delays[0]}", delays[1] > delays[0])
    }

    @Test
    fun delaysGrowToTheCapAndStopThere() {
        val delays = mutableListOf<Long>()
        val registration = PushRegistration(OrreryClient(server.baseUrl)) { millis ->
            delays.add(millis)
            if (delays.size == 12) server.respond(path, RegisterResponse.getDefaultInstance())
        }

        registration.register(endpointUrl, publicKey, authSecret)

        assertEquals(12, delays.size)
        assertEquals(PushRegistration.INITIAL_DELAY_MS, delays.first())
        assertEquals(PushRegistration.MAX_DELAY_MS, delays.last())
        assertTrue("no delay may exceed the cap: $delays", delays.all { it <= PushRegistration.MAX_DELAY_MS })
        assertTrue("delays must reach the cap: $delays", delays.count { it == PushRegistration.MAX_DELAY_MS } >= 2)
    }
}
