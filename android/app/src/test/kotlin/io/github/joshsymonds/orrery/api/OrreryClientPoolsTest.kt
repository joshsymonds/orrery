package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolHold as PoolHoldProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolHolder as PoolHolderProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolMemberStatus as PoolMemberStatusProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolStatus as PoolStatusProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolWaiter as PoolWaiterProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsFreeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsHoldRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsHoldResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsFreeRequest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientPoolsTest {
    @Test
    fun poolsMapsEveryFieldOfTheStatus() {
        val status = PoolsStatusResponse.newBuilder()
            .addPools(
                PoolStatusProto.newBuilder()
                    .setName("example-pool")
                    .setCap(2)
                    .addMembers(
                        PoolMemberStatusProto.newBuilder()
                            .setName("example-member-1")
                            .setState("awake")
                            .setHolder(
                                PoolHolderProto.newBuilder()
                                    .setProject("/home/user/project")
                                    .addCommand("claude")
                                    .addCommand("-p")
                                    .setSince(1_700_000_100L)
                                    .setStopping(false),
                            )
                            .setHeldSince(1_700_000_120L),
                    )
                    .addMembers(
                        PoolMemberStatusProto.newBuilder()
                            .setName("example-member-2")
                            .setState("asleep"),
                    )
                    .addWaiters(
                        PoolWaiterProto.newBuilder()
                            .setPosition(1)
                            .setProject("/home/user/other")
                            .addCommand("make")
                            .addCommand("test")
                            .setCount(2)
                            .setSince(1_700_000_200L),
                    ),
            )
            .addPools(
                PoolStatusProto.newBuilder()
                    .setName("example-pool-2")
                    .setCap(1)
                    .addMembers(
                        PoolMemberStatusProto.newBuilder()
                            .setName("example-member-3")
                            .setState("admitted")
                            .setHolder(
                                PoolHolderProto.newBuilder()
                                    .setProject("/home/user/third")
                                    .setSince(1_700_000_150L)
                                    .setStopping(true),
                            )
                            .setHeldSince(1_700_000_150L),
                    ),
            )
            .addHolds(PoolHoldProto.newBuilder().setMember("example-orphan").setSince(1_700_000_300L))
            .build()
        FakeOrreryServer().use { server ->
            server.respond(POOLS_STATUS, status)

            val view = OrreryClient(server.baseUrl).pools()

            assertEquals(
                PoolsView(
                    pools = listOf(
                        Pool(
                            name = "example-pool",
                            cap = 2,
                            members = listOf(
                                PoolMember(
                                    name = "example-member-1",
                                    state = "awake",
                                    holder = Holder(
                                        project = "/home/user/project",
                                        command = listOf("claude", "-p"),
                                        since = 1_700_000_100L,
                                        stopping = false,
                                    ),
                                    heldSince = 1_700_000_120L,
                                ),
                                PoolMember(
                                    name = "example-member-2",
                                    state = "asleep",
                                    holder = null,
                                    heldSince = 0L,
                                ),
                            ),
                            waiters = listOf(
                                Waiter(
                                    position = 1,
                                    project = "/home/user/other",
                                    command = listOf("make", "test"),
                                    count = 2,
                                    since = 1_700_000_200L,
                                ),
                            ),
                        ),
                        Pool(
                            name = "example-pool-2",
                            cap = 1,
                            members = listOf(
                                PoolMember(
                                    name = "example-member-3",
                                    state = "admitted",
                                    holder = Holder(
                                        project = "/home/user/third",
                                        command = emptyList(),
                                        since = 1_700_000_150L,
                                        stopping = true,
                                    ),
                                    heldSince = 1_700_000_150L,
                                ),
                            ),
                            waiters = emptyList(),
                        ),
                    ),
                    holds = listOf(Hold(member = "example-orphan", since = 1_700_000_300L)),
                ),
                view,
            )
            assertPoolsRequest(POOLS_STATUS, server.requests.single())
        }
    }

    @Test
    fun holdMemberPostsMemberWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_HOLD, PoolsHoldResponse.getDefaultInstance())

            OrreryClient(server.baseUrl).holdMember("example-member-1")

            val request = server.requests.single()
            assertPoolsRequest(POOLS_HOLD, request)
            assertEquals(
                PoolsHoldRequest.newBuilder().setMember("example-member-1").build(),
                PoolsHoldRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun freeMemberPostsMemberWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond(POOLS_FREE, PoolsFreeResponse.getDefaultInstance())

            OrreryClient(server.baseUrl).freeMember("example-member-1")

            val request = server.requests.single()
            assertPoolsRequest(POOLS_FREE, request)
            assertEquals(
                PoolsFreeRequest.newBuilder().setMember("example-member-1").build(),
                PoolsFreeRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun freeMemberErrorThrowsCallExceptionCarryingCode() {
        FakeOrreryServer().use { server ->
            server.fail(POOLS_FREE, Code.NOT_FOUND, "not held")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).freeMember("example-member-1")
            }

            assertEquals(Code.NOT_FOUND, error.code)
            assertEquals("not held", error.message)
        }
    }

    private fun assertPoolsRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val POOLS_STATUS = "/orrery.v1.Pools/Status"
        const val POOLS_HOLD = "/orrery.v1.Pools/Hold"
        const val POOLS_FREE = "/orrery.v1.Pools/Free"
    }
}
