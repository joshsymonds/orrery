package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.Design
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientDesignsTest {
    private val writeFirstProto: DesignContract = DesignContract.newBuilder()
        .setId("example-repo/write-first")
        .setTitle("Example first contract")
        .setRepository("example-repo")
        .setState("accepted")
        .setPosition(1L)
        .setEpic("example-epic-1")
        .setEpicPhase("build")
        .build()

    private val alphaSecondProto: DesignContract = DesignContract.newBuilder()
        .setId("example-repo/alpha-second")
        .setTitle("Example second contract")
        .setRepository("example-repo")
        .setState("proposed")
        .setPosition(2L)
        .addAfter("example-repo/write-first")
        .build()

    private val lastThirdProto: DesignContract = DesignContract.newBuilder()
        .setId("example-repo/last-third")
        .setTitle("Example third contract")
        .setRepository("other-repo")
        .setState("proposed")
        .setPosition(3L)
        .addAfter("example-repo/write-first")
        .addAfter("example-repo/alpha-second")
        .setEpic("example-epic-3")
        .setEpicPhase("review")
        .build()

    private val designOneProto: Design = Design.newBuilder()
        .setId("example-design-1")
        .setTitle("Example design title")
        .setConversation("example-session-1")
        .setCreated(4102444000L)
        .setOfOne(false)
        .setState("1 of 3 accepted")
        .addContracts(writeFirstProto)
        .addContracts(alphaSecondProto)
        .addContracts(lastThirdProto)
        .build()

    private val loneContractProto: DesignContract = DesignContract.newBuilder()
        .setId("example-repo/lone")
        .setTitle("Example lone contract")
        .setRepository("other-repo")
        .setState("proposed")
        .setPosition(1L)
        .build()

    private val designTwoProto: Design = Design.newBuilder()
        .setId("example-design-2")
        .setTitle("Example lone contract")
        .setConversation("example-session-2")
        .setCreated(4102444500L)
        .setOfOne(true)
        .setState("proposed")
        .addContracts(loneContractProto)
        .build()

    @Test
    fun designsMapEveryFieldOfEachDesignAndItsContractsInServedOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                DESIGNS_LIST,
                DesignsListResponse.newBuilder().addDesigns(designOneProto).addDesigns(designTwoProto).build(),
            )

            val designs = OrreryClient(server.baseUrl).designs()

            assertEquals(
                listOf(
                    DesignRow(
                        id = "example-design-1",
                        title = "Example design title",
                        conversation = "example-session-1",
                        created = 4102444000L,
                        ofOne = false,
                        state = "1 of 3 accepted",
                        contracts = listOf(
                            DesignContractRow(
                                id = "example-repo/write-first",
                                title = "Example first contract",
                                repository = "example-repo",
                                state = "accepted",
                                position = 1L,
                                after = emptyList(),
                                epic = "example-epic-1",
                                epicPhase = "build",
                            ),
                            DesignContractRow(
                                id = "example-repo/alpha-second",
                                title = "Example second contract",
                                repository = "example-repo",
                                state = "proposed",
                                position = 2L,
                                after = listOf("example-repo/write-first"),
                                epic = "",
                                epicPhase = "",
                            ),
                            DesignContractRow(
                                id = "example-repo/last-third",
                                title = "Example third contract",
                                repository = "other-repo",
                                state = "proposed",
                                position = 3L,
                                after = listOf("example-repo/write-first", "example-repo/alpha-second"),
                                epic = "example-epic-3",
                                epicPhase = "review",
                            ),
                        ),
                    ),
                    DesignRow(
                        id = "example-design-2",
                        title = "Example lone contract",
                        conversation = "example-session-2",
                        created = 4102444500L,
                        ofOne = true,
                        state = "proposed",
                        contracts = listOf(
                            DesignContractRow(
                                id = "example-repo/lone",
                                title = "Example lone contract",
                                repository = "other-repo",
                                state = "proposed",
                                position = 1L,
                                after = emptyList(),
                                epic = "",
                                epicPhase = "",
                            ),
                        ),
                    ),
                ),
                designs,
            )
            val request = server.requests.single()
            assertEquals("POST", request.method)
            assertEquals(DESIGNS_LIST, request.rawPath)
        }
    }

    @Test
    fun unavailableUnimplementedAndNotFoundGiveNoDesigns() {
        for (code in listOf(Code.UNAVAILABLE, Code.UNIMPLEMENTED, Code.NOT_FOUND)) {
            FakeOrreryServer().use { server ->
                server.fail(DESIGNS_LIST, code)

                assertEquals(emptyList<DesignRow>(), OrreryClient(server.baseUrl).designs())
            }
        }
    }

    @Test
    fun designsThrowAnyOtherError() {
        FakeOrreryServer().use { server ->
            server.fail(DESIGNS_LIST, Code.INTERNAL_ERROR)

            assertThrows(OrreryCallException::class.java) { OrreryClient(server.baseUrl).designs() }
        }
    }

    private companion object {
        const val DESIGNS_LIST = "/orrery.v1.Designs/List"
    }
}
