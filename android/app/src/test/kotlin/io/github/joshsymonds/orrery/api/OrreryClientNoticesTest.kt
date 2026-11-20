package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeKey
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesSeenResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientNoticesTest {
    @Test
    fun paneNoticeSeenPostsKeyWithOnlyThePane() {
        FakeOrreryServer().use { server ->
            server.respond(NOTICES_SEEN, NoticesSeenResponse.getDefaultInstance())

            OrreryClient(server.baseUrl).paneNoticeSeen("%7")

            val request = server.requests.single()
            assertNoticesSeenRequest(request)
            assertEquals(
                NoticesSeenRequest.newBuilder().setKey(NoticeKey.newBuilder().setPane("%7")).build(),
                NoticesSeenRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun sessionNoticeSeenPostsKeyWithOnlyTheSession() {
        FakeOrreryServer().use { server ->
            server.respond(NOTICES_SEEN, NoticesSeenResponse.getDefaultInstance())

            OrreryClient(server.baseUrl).sessionNoticeSeen("example-session-1")

            val request = server.requests.single()
            assertNoticesSeenRequest(request)
            assertEquals(
                NoticesSeenRequest.newBuilder()
                    .setKey(NoticeKey.newBuilder().setSession("example-session-1"))
                    .build(),
                NoticesSeenRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun noticeSeenErrorThrowsCallExceptionCarryingCode() {
        FakeOrreryServer().use { server ->
            server.fail(NOTICES_SEEN, Code.NOT_FOUND, "no such notice")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).paneNoticeSeen("%7")
            }

            assertEquals(Code.NOT_FOUND, error.code)
        }
    }

    private fun assertNoticesSeenRequest(request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(NOTICES_SEEN, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val NOTICES_SEEN = "/orrery.v1.Notices/Seen"
    }
}
