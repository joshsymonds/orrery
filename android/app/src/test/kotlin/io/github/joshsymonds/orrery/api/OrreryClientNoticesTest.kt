package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.ListedNotice
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeBlock
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeKey
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesReadAllRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesReadAllResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesSeenResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientNoticesTest {
    @Test
    fun noticesListsAndMapsEveryFieldInServerOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                NOTICES_LIST,
                NoticesListResponse.newBuilder()
                    .addNotices(
                        ListedNotice.newBuilder()
                            .setKey(NoticeKey.newBuilder().setPane("%7"))
                            .setState("waiting")
                            .setTitle("Pane waiting")
                            .setBody("Input needed")
                            .setBlock(NoticeBlock.newBuilder().setKind("question").setRef("Q2"))
                            .setTime(300L)
                            .setRead(false),
                    )
                    .addNotices(
                        ListedNotice.newBuilder()
                            .setKey(NoticeKey.newBuilder().setSession("example-session-1"))
                            .setState("done")
                            .setTitle("Session done")
                            .setBody("Finished")
                            .setTime(250L)
                            .setRead(true),
                    )
                    .addNotices(
                        ListedNotice.newBuilder()
                            .setKey(NoticeKey.newBuilder().setEpic("example-epic"))
                            .setState("waiting")
                            .setTitle("Release step")
                            .setBody("Ready to release")
                            .setBlock(NoticeBlock.newBuilder().setKind("release_step").setRef("3"))
                            .setTime(200L)
                            .setRead(false),
                    )
                    .addNotices(
                        ListedNotice.newBuilder()
                            .setKey(NoticeKey.newBuilder().setLimit("five_hour@1700000000"))
                            .setState("limited")
                            .setTitle("Limit reached")
                            .setBody("Resets soon")
                            .setTime(100L)
                            .setRead(true),
                    )
                    .addNotices(
                        ListedNotice.newBuilder()
                            .setKey(NoticeKey.newBuilder().setAlert("disk"))
                            .setState("alerted")
                            .setTitle("Disk alert")
                            .setBody("Disk full")
                            .setTime(50L)
                            .setRead(false),
                    )
                    .build(),
            )

            val notices = OrreryClient(server.baseUrl).notices()

            assertEquals(
                listOf(
                    NoticeRow(
                        pane = "%7",
                        session = "",
                        epic = "",
                        limit = "",
                        alert = "",
                        state = "waiting",
                        title = "Pane waiting",
                        body = "Input needed",
                        blockKind = "question",
                        blockRef = "Q2",
                        time = 300L,
                        read = false,
                    ),
                    NoticeRow(
                        pane = "",
                        session = "example-session-1",
                        epic = "",
                        limit = "",
                        alert = "",
                        state = "done",
                        title = "Session done",
                        body = "Finished",
                        blockKind = "",
                        blockRef = "",
                        time = 250L,
                        read = true,
                    ),
                    NoticeRow(
                        pane = "",
                        session = "",
                        epic = "example-epic",
                        limit = "",
                        alert = "",
                        state = "waiting",
                        title = "Release step",
                        body = "Ready to release",
                        blockKind = "release_step",
                        blockRef = "3",
                        time = 200L,
                        read = false,
                    ),
                    NoticeRow(
                        pane = "",
                        session = "",
                        epic = "",
                        limit = "five_hour@1700000000",
                        alert = "",
                        state = "limited",
                        title = "Limit reached",
                        body = "Resets soon",
                        blockKind = "",
                        blockRef = "",
                        time = 100L,
                        read = true,
                    ),
                    NoticeRow(
                        pane = "",
                        session = "",
                        epic = "",
                        limit = "",
                        alert = "disk",
                        state = "alerted",
                        title = "Disk alert",
                        body = "Disk full",
                        blockKind = "",
                        blockRef = "",
                        time = 50L,
                        read = false,
                    ),
                ),
                notices,
            )
            val request = server.requests.single()
            assertNoticesRequest(NOTICES_LIST, request)
            assertEquals(NoticesListRequest.getDefaultInstance(), NoticesListRequest.parseFrom(request.body))
        }
    }

    @Test
    fun noticeRowSeenPostsTheRowsKeyAlone() {
        FakeOrreryServer().use { server ->
            server.respond(NOTICES_SEEN, NoticesSeenResponse.getDefaultInstance())
            val row = NoticeRow(
                pane = "",
                session = "",
                epic = "example-epic",
                limit = "",
                alert = "",
                state = "waiting",
                title = "Release step",
                body = "Ready to release",
                blockKind = "release_step",
                blockRef = "3",
                time = 200L,
                read = false,
            )

            OrreryClient(server.baseUrl).noticeRowSeen(row)

            val request = server.requests.single()
            assertNoticesRequest(NOTICES_SEEN, request)
            assertEquals(
                NoticesSeenRequest.newBuilder()
                    .setKey(NoticeKey.newBuilder().setEpic("example-epic"))
                    .build(),
                NoticesSeenRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun readAllNoticesPostsReadAll() {
        FakeOrreryServer().use { server ->
            server.respond(NOTICES_READ_ALL, NoticesReadAllResponse.getDefaultInstance())

            OrreryClient(server.baseUrl).readAllNotices()

            val request = server.requests.single()
            assertNoticesRequest(NOTICES_READ_ALL, request)
            assertEquals(NoticesReadAllRequest.getDefaultInstance(), NoticesReadAllRequest.parseFrom(request.body))
        }
    }

    @Test
    fun noticesErrorThrowsCallExceptionCarryingCode() {
        FakeOrreryServer().use { server ->
            server.fail(NOTICES_LIST, Code.INTERNAL_ERROR, "orreryd failed")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).notices()
            }

            assertEquals(Code.INTERNAL_ERROR, error.code)
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

    @Test
    fun paneNoticeSeenPostsKeyWithOnlyThePane() {
        FakeOrreryServer().use { server ->
            server.respond(NOTICES_SEEN, NoticesSeenResponse.getDefaultInstance())

            OrreryClient(server.baseUrl).paneNoticeSeen("%7")

            val request = server.requests.single()
            assertNoticesRequest(NOTICES_SEEN, request)
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
            assertNoticesRequest(NOTICES_SEEN, request)
            assertEquals(
                NoticesSeenRequest.newBuilder()
                    .setKey(NoticeKey.newBuilder().setSession("example-session-1"))
                    .build(),
                NoticesSeenRequest.parseFrom(request.body),
            )
        }
    }

    private fun assertNoticesRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private companion object {
        const val NOTICES_LIST = "/orrery.v1.Notices/List"
        const val NOTICES_SEEN = "/orrery.v1.Notices/Seen"
        const val NOTICES_READ_ALL = "/orrery.v1.Notices/ReadAll"
    }
}
