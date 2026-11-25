package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.Account as AccountProto
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsAddRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsAddResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsRemoveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsRemoveResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsRenameRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsRenameResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteContext as RouteContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteRegistry as RouteRegistryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteSeat as RouteSeatProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesSetContextAccountRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesSetContextAccountResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientAccountsTest {
    @Test
    fun llmAccountsMapsTheAccountsThenTheRouteRegistry() {
        FakeOrreryServer().use { server ->
            server.respond(
                ACCOUNTS_LIST,
                AccountsListResponse.newBuilder().addAccounts(WORK_ACCOUNT).addAccounts(HOME_ACCOUNT).build(),
            )
            server.respond(ROUTES_GET, RoutesGetResponse.newBuilder().setRegistry(MAIN_REGISTRY).build())

            val view = OrreryClient(server.baseUrl).llmAccounts()

            assertEquals(
                LlmAccounts(
                    accounts = listOf(WORK_LLM_ACCOUNT, HOME_LLM_ACCOUNT),
                    routes = MAIN_LLM_ROUTES,
                ),
                view,
            )
            assertEquals(listOf(ACCOUNTS_LIST, ROUTES_GET), server.requests.map { it.rawPath })
            assertRequest(ACCOUNTS_LIST, server.requests[0])
            assertRequest(ROUTES_GET, server.requests[1])
        }
    }

    @Test
    fun llmRoutesReadsTheRegistryAloneAndMapsIt() {
        FakeOrreryServer().use { server ->
            server.respond(ROUTES_GET, RoutesGetResponse.newBuilder().setRegistry(OWN_LOGIN_REGISTRY).build())

            val routes = OrreryClient(server.baseUrl).llmRoutes()

            assertEquals(OWN_LOGIN_LLM_ROUTES, routes)
            assertEquals(listOf(ROUTES_GET), server.requests.map { it.rawPath })
            assertRequest(ROUTES_GET, server.requests.single())
        }
    }

    @Test
    fun llmAccountsThrowsWhenTheAccountListFailsAndDoesNotReadTheRoutes() {
        FakeOrreryServer().use { server ->
            server.fail(ACCOUNTS_LIST, Code.UNAVAILABLE, "accounts are unavailable")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).llmAccounts()
            }

            assertEquals(Code.UNAVAILABLE, error.code)
            assertEquals(listOf(ACCOUNTS_LIST), server.requests.map { it.rawPath })
        }
    }

    @Test
    fun addAccountSendsTheFieldsWithTheAnthropicProviderAndAnswersTheAccount() {
        FakeOrreryServer().use { server ->
            server.respond(ACCOUNTS_ADD, AccountsAddResponse.newBuilder().setAccount(WORK_ACCOUNT).build())

            val result = OrreryClient(server.baseUrl).addAccount(name = "Example Work", plan = "max", token = EXAMPLE_TOKEN)

            assertEquals(LlmWrite.Done(WORK_LLM_ACCOUNT), result)
            val request = server.requests.single()
            assertRequest(ACCOUNTS_ADD, request)
            assertEquals(
                AccountsAddRequest.newBuilder()
                    .setName("Example Work")
                    .setProvider("anthropic")
                    .setPlan("max")
                    .setToken(EXAMPLE_TOKEN)
                    .build(),
                AccountsAddRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun renameAccountSendsTheIdAndNewNameAndAnswersTheAccount() {
        val renamed = WORK_ACCOUNT.toBuilder().setName("Example Renamed").build()
        FakeOrreryServer().use { server ->
            server.respond(ACCOUNTS_RENAME, AccountsRenameResponse.newBuilder().setAccount(renamed).build())

            val result = OrreryClient(server.baseUrl).renameAccount(id = "acct-example-1", name = "Example Renamed")

            assertEquals(LlmWrite.Done(WORK_LLM_ACCOUNT.copy(name = "Example Renamed")), result)
            val request = server.requests.single()
            assertRequest(ACCOUNTS_RENAME, request)
            assertEquals(
                AccountsRenameRequest.newBuilder().setId("acct-example-1").setName("Example Renamed").build(),
                AccountsRenameRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun removeAccountSendsTheIdAndAnswersDone() {
        FakeOrreryServer().use { server ->
            server.respond(ACCOUNTS_REMOVE, AccountsRemoveResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).removeAccount("acct-example-2")

            assertEquals(LlmWrite.Done(Unit), result)
            val request = server.requests.single()
            assertRequest(ACCOUNTS_REMOVE, request)
            assertEquals(
                AccountsRemoveRequest.newBuilder().setId("acct-example-2").build(),
                AccountsRemoveRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun setContextAccountSendsTheContextAndAccountAndAnswersTheRegistry() {
        FakeOrreryServer().use { server ->
            server.respond(
                ROUTES_SET_CONTEXT_ACCOUNT,
                RoutesSetContextAccountResponse.newBuilder().setRegistry(MAIN_REGISTRY).build(),
            )

            val result = OrreryClient(server.baseUrl).setContextAccount(context = "ctx-main", account = "acct-example-2")

            assertEquals(LlmWrite.Done(MAIN_LLM_ROUTES), result)
            val request = server.requests.single()
            assertRequest(ROUTES_SET_CONTEXT_ACCOUNT, request)
            assertEquals(
                RoutesSetContextAccountRequest.newBuilder().setContext("ctx-main").setAccount("acct-example-2").build(),
                RoutesSetContextAccountRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun setContextAccountWithNoAccountSendsAnEmptyAccountForTheSessionsOwnLogin() {
        FakeOrreryServer().use { server ->
            server.respond(
                ROUTES_SET_CONTEXT_ACCOUNT,
                RoutesSetContextAccountResponse.newBuilder().setRegistry(OWN_LOGIN_REGISTRY).build(),
            )

            val result = OrreryClient(server.baseUrl).setContextAccount(context = "ctx-own", account = "")

            assertEquals(LlmWrite.Done(OWN_LOGIN_LLM_ROUTES), result)
            val sent = RoutesSetContextAccountRequest.parseFrom(server.requests.single().body)
            assertEquals("ctx-own", sent.context)
            assertEquals("", sent.account)
        }
    }

    @Test
    fun addAccountInvalidArgumentIsRefusedWithTheServersReason() {
        FakeOrreryServer().use { server ->
            server.fail(ACCOUNTS_ADD, Code.INVALID_ARGUMENT, "name is already taken")

            val result = OrreryClient(server.baseUrl).addAccount(name = "Example Work", plan = "", token = EXAMPLE_TOKEN)

            assertEquals(LlmWrite.Refused(Code.INVALID_ARGUMENT, "name is already taken"), result)
        }
    }

    @Test
    fun renameAccountNotFoundIsRefusedWithTheServersReason() {
        FakeOrreryServer().use { server ->
            server.fail(ACCOUNTS_RENAME, Code.NOT_FOUND, "no account acct-example-9")

            val result = OrreryClient(server.baseUrl).renameAccount(id = "acct-example-9", name = "Example")

            assertEquals(LlmWrite.Refused(Code.NOT_FOUND, "no account acct-example-9"), result)
        }
    }

    @Test
    fun removeAccountFailedPreconditionIsRefusedWithTheServersReason() {
        FakeOrreryServer().use { server ->
            server.fail(ACCOUNTS_REMOVE, Code.FAILED_PRECONDITION, "account is a context's account")

            val result = OrreryClient(server.baseUrl).removeAccount("acct-example-1")

            assertEquals(LlmWrite.Refused(Code.FAILED_PRECONDITION, "account is a context's account"), result)
        }
    }

    @Test
    fun renameAccountUnavailableIsNoAnswer() {
        FakeOrreryServer().use { server ->
            server.fail(ACCOUNTS_RENAME, Code.UNAVAILABLE, "try again later")

            val result = OrreryClient(server.baseUrl).renameAccount(id = "acct-example-1", name = "Example")

            assertEquals(LlmWrite.NoAnswer, result)
        }
    }

    @Test
    fun addAccountDroppedIsNoAnswer() {
        FakeOrreryServer().use { server ->
            server.drop(ACCOUNTS_ADD)

            val result = OrreryClient(server.baseUrl).addAccount(name = "Example Work", plan = "max", token = EXAMPLE_TOKEN)

            assertEquals(LlmWrite.NoAnswer, result)
            assertEquals(listOf(ACCOUNTS_ADD), server.requests.map { it.rawPath })
        }
    }

    private fun assertRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val ACCOUNTS_LIST = "/orrery.v1.Accounts/List"
        const val ACCOUNTS_ADD = "/orrery.v1.Accounts/Add"
        const val ACCOUNTS_RENAME = "/orrery.v1.Accounts/Rename"
        const val ACCOUNTS_REMOVE = "/orrery.v1.Accounts/Remove"
        const val ROUTES_GET = "/orrery.v1.Routes/Get"
        const val ROUTES_SET_CONTEXT_ACCOUNT = "/orrery.v1.Routes/SetContextAccount"

        /** An invented token in the shape of a Claude OAuth token. It is sent once and asserted on, never logged. */
        const val EXAMPLE_TOKEN = "sk-ant-oat01-example-token"

        val WORK_ACCOUNT: AccountProto = AccountProto.newBuilder()
            .setId("acct-example-1")
            .setName("Example Work")
            .setProvider("anthropic")
            .setPlan("max")
            .setFingerprint("fp-example-1")
            .setCreated(1_700_000_000L)
            .build()

        val HOME_ACCOUNT: AccountProto = AccountProto.newBuilder()
            .setId("acct-example-2")
            .setName("Example Home")
            .setProvider("anthropic")
            .setPlan("")
            .setFingerprint("fp-example-2")
            .setCreated(1_700_000_500L)
            .build()

        val WORK_LLM_ACCOUNT = LlmAccount(
            id = "acct-example-1",
            name = "Example Work",
            provider = "anthropic",
            plan = "max",
            fingerprint = "fp-example-1",
            created = 1_700_000_000L,
        )

        val HOME_LLM_ACCOUNT = LlmAccount(
            id = "acct-example-2",
            name = "Example Home",
            provider = "anthropic",
            plan = "",
            fingerprint = "fp-example-2",
            created = 1_700_000_500L,
        )

        val MAIN_REGISTRY: RouteRegistryProto = RouteRegistryProto.newBuilder()
            .setDefaultContext("ctx-main")
            .addSeats(
                RouteSeatProto.newBuilder()
                    .setId("seat-forward")
                    .setAuthMode("forward")
                    .setModel("example-model-a")
                    .setBilling("subscription")
                    .setSpeed("standard"),
            )
            .addSeats(
                RouteSeatProto.newBuilder()
                    .setId("seat-sigv4")
                    .setAuthMode("sigv4")
                    .setUpstream("https://bedrock.example.test")
                    .setModel("example-model-b")
                    .setAwsProfile("example-profile")
                    .setAwsRegion("example-region-1")
                    .setModelMapEnvFile("/home/user/example-model-map.env")
                    .setBilling("metered")
                    .setSpeed("fast"),
            )
            .addContexts(
                RouteContextProto.newBuilder()
                    .setId("ctx-main")
                    .setDefaultSeat("seat-forward")
                    .putModels("example-model-a", "seat-forward")
                    .putModels("example-model-b", "seat-sigv4")
                    .setAccount("acct-example-1"),
            )
            .addContexts(
                RouteContextProto.newBuilder()
                    .setId("ctx-own")
                    .setDefaultSeat("seat-forward"),
            )
            .build()

        val MAIN_LLM_ROUTES = LlmRoutes(
            defaultContext = "ctx-main",
            seats = listOf(
                LlmSeat(
                    id = "seat-forward",
                    authMode = "forward",
                    upstream = "",
                    model = "example-model-a",
                    billing = "subscription",
                    speed = "standard",
                    awsProfile = "",
                    awsRegion = "",
                    modelMapEnvFile = "",
                ),
                LlmSeat(
                    id = "seat-sigv4",
                    authMode = "sigv4",
                    upstream = "https://bedrock.example.test",
                    model = "example-model-b",
                    billing = "metered",
                    speed = "fast",
                    awsProfile = "example-profile",
                    awsRegion = "example-region-1",
                    modelMapEnvFile = "/home/user/example-model-map.env",
                ),
            ),
            contexts = listOf(
                LlmContext(
                    id = "ctx-main",
                    defaultSeat = "seat-forward",
                    models = mapOf("example-model-a" to "seat-forward", "example-model-b" to "seat-sigv4"),
                    account = "acct-example-1",
                ),
                LlmContext(id = "ctx-own", defaultSeat = "seat-forward", models = emptyMap(), account = ""),
            ),
        )

        val OWN_LOGIN_REGISTRY: RouteRegistryProto = RouteRegistryProto.newBuilder()
            .setDefaultContext("ctx-own")
            .addSeats(
                RouteSeatProto.newBuilder()
                    .setId("seat-inject")
                    .setAuthMode("inject")
                    .setModel("example-model-c"),
            )
            .addContexts(
                RouteContextProto.newBuilder()
                    .setId("ctx-own")
                    .setDefaultSeat("seat-inject"),
            )
            .build()

        val OWN_LOGIN_LLM_ROUTES = LlmRoutes(
            defaultContext = "ctx-own",
            seats = listOf(
                LlmSeat(
                    id = "seat-inject",
                    authMode = "inject",
                    upstream = "",
                    model = "example-model-c",
                    billing = "",
                    speed = "",
                    awsProfile = "",
                    awsRegion = "",
                    modelMapEnvFile = "",
                ),
            ),
            contexts = listOf(
                LlmContext(id = "ctx-own", defaultSeat = "seat-inject", models = emptyMap(), account = ""),
            ),
        )
    }
}
