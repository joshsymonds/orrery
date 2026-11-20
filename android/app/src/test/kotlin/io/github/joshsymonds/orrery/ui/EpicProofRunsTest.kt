package io.github.joshsymonds.orrery.ui

import android.os.Looper
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import com.google.protobuf.ByteString
import io.github.joshsymonds.orrery.api.EvidenceArtifactFile
import io.github.joshsymonds.orrery.api.EvidenceArtifactRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifactRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifactResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceRun
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.IOException
import java.util.Base64
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicProofRunsTest {
    private companion object {
        const val REQUIREMENT = 1
        const val REVISION = "0123456789abcdef0123456789abcdef01234567"
        const val LOG = "example log line one\nexample log line two\n"
        const val STDERR = "example stderr line one\n"
        const val EPIC_ID = "example-repo/example-epic"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val EVIDENCE_ARTIFACT = "/orrery.v1.Evidence/Artifact"
        const val CHANGED = 4_102_444_800L
        const val IMAGE_TAG = "evidence-image:1:0:0"
        const val NOT_AVAILABLE = "The log is not available."
        const val LOADING = "loading the log…"
        const val HANG_GUARD_SECONDS = 30L

        // A 1x1 PNG.
        val PNG: ByteArray = Base64.getDecoder().decode(
            "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=",
        )
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    /**
     * A loader over [files], keyed by run, name and stderr flag. It records each call and whether one ran on the main
     * thread. When [gate] is set, each call waits on it, so a test can hold a load open.
     */
    private class Loader(
        private val files: Map<Triple<String, String, Boolean>, EvidenceArtifactFile>,
        private val gate: CountDownLatch? = null,
    ) {
        val calls = ConcurrentLinkedQueue<Triple<String, String, Boolean>>()
        val onMainThread = AtomicBoolean(false)

        val load: (String, String, Boolean) -> EvidenceArtifactFile? = { run, name, stderr ->
            calls.add(Triple(run, name, stderr))
            if (Looper.myLooper() == Looper.getMainLooper()) onMainThread.set(true)
            gate?.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS)
            files[Triple(run, name, stderr)]
        }
    }

    private fun run(
        id: String,
        phase: String = "red",
        source: String = "captured",
        state: String = "ended",
        verdict: String = "holds",
        judgeReason: String = "",
        passed: Boolean = true,
        exit: Int = 0,
        notCaptured: String = "",
        logBytes: Long = 0L,
        stderrBytes: Long = 0L,
        artifacts: List<EvidenceArtifactRow> = emptyList(),
    ): EvidenceRunRow =
        EvidenceRunRow(
            id = id,
            phase = phase,
            source = source,
            state = state,
            exit = exit,
            timedOut = false,
            notCaptured = notCaptured,
            verdict = verdict,
            judgeReason = judgeReason,
            passed = passed,
            revision = REVISION,
            created = CHANGED - 600,
            ended = if (state == "ended") CHANGED - 60 else 0L,
            logBytes = logBytes,
            stderrBytes = stderrBytes,
            artifacts = artifacts,
        )

    private fun setRuns(runs: List<EvidenceRunRow>, loader: Loader) {
        composeRule.setContent {
            EvidenceRuns(requirement = REQUIREMENT, runs = runs, loadArtifact = loader.load)
        }
    }

    private fun rowTag(index: Int) = "evidence-run:$REQUIREMENT:$index"

    private fun logTag(index: Int) = "evidence-log:$REQUIREMENT:$index"

    private fun streamTag(index: Int, stream: String) = "evidence-stream:$REQUIREMENT:$index:$stream"

    private fun countAll(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun countIn(tag: String, matcher: SemanticsMatcher): Int =
        countAll(matcher and hasAnyAncestor(hasTestTag(tag)))

    private fun awaitAll(matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countAll(matcher) > 0 }
    }

    private fun awaitIn(tag: String, matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countIn(tag, matcher) > 0 }
    }

    private fun clickTag(tag: String) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).performClick()
    }

    /** The one chip of run [index] that reads [text], and the tone it is drawn in. */
    private fun toneOf(index: Int, text: String): ChipTone =
        composeRule.onNode(hasText(text) and hasAnyAncestor(hasTestTag(rowTag(index))), useUnmergedTree = true)
            .fetchSemanticsNode()
            .config[ChipToneKey]

    /** Asserts run [index] reads its sentence, its verdict chip in its tone, and its meta. */
    private fun assertLine(index: Int, sentence: String, chip: String, tone: ChipTone, meta: String) {
        assertEquals(1, countIn(rowTag(index), hasText(sentence)))
        assertEquals(1, countIn(rowTag(index), hasText(chip)))
        assertEquals(tone, toneOf(index, chip))
        assertEquals(1, countIn(rowTag(index), hasText(meta)))
    }

    private fun textLayoutOf(matcher: SemanticsMatcher): TextLayoutResult {
        val layouts = mutableListOf<TextLayoutResult>()
        val node = composeRule.onNode(matcher, useUnmergedTree = true).fetchSemanticsNode()
        node.config.getOrNull(SemanticsActions.GetTextLayoutResult)?.action?.invoke(layouts)
        return layouts.single()
    }

    private fun setScreen(server: FakeOrreryServer) {
        // The screen reads the epic's history with its detail, so an empty history is served beside it.
        server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
        // One client for the screen's life: a client built inside the content keys the screen's load anew each time
        // the content recomposes (see PoolsScreenTest's aClientBuiltInsideTheContentReloadsTheScreen...).
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            EpicScreen(
                client = client,
                epicId = EPIC_ID,
                onBack = {},
                onOpenStep = {},
                onOpenContract = {},
                onTalk = {},
                listedSteerer = { null },
            )
        }
    }

    private fun epicBody(vararg requirements: EpicRequirement): EpicsGetResponse =
        EpicsGetResponse.newBuilder()
            .setEpic(
                Epic.newBuilder()
                    .setId(EPIC_ID)
                    .setRepository("example-repo")
                    .setSlug("example-epic")
                    .setOutcome("in progress")
                    .setChanged(CHANGED)
                    .setLive(true)
                    .setSource("orreryd"),
            )
            .addAllRequirements(requirements.toList())
            .build()

    @Test
    fun epicProofCapturingRunReadsCapturingInItsSentenceAndLiveChip() {
        setRuns(listOf(run("example-run-1", phase = "red", state = "capturing", verdict = "")), Loader(emptyMap()))

        assertLine(0, "Red run: capturing the output…", "capturing", ChipTone.Live, "captured")
        assertEquals(0, countIn(rowTag(0), hasText("exit", substring = true)))
    }

    @Test
    fun epicProofJudgingRunReadsAJudgeIsReadingTheOutputAndLiveChip() {
        setRuns(
            listOf(run("example-run-1", phase = "green", source = "supplied", state = "judging", verdict = "")),
            Loader(emptyMap()),
        )

        assertLine(0, "Green run: a judge is reading the output…", "judging", ChipTone.Live, "supplied by the agent")
        assertEquals(0, countIn(rowTag(0), hasText("exit", substring = true)))
    }

    @Test
    fun epicProofNotCapturedRunSaysWhyItsOutputIsMissingAndReadsNoVerdict() {
        setRuns(
            listOf(run("example-run-1", phase = "red", notCaptured = "the output was too large", verdict = "", passed = false)),
            Loader(emptyMap()),
        )

        assertLine(
            0,
            "Red run: the output was not captured: the output was too large.",
            "no verdict",
            ChipTone.Neutral,
            "captured · exit 0",
        )
    }

    @Test
    fun epicProofRedRunThatPassedReadsTheTestFailsBeforeAnyChange() {
        setRuns(listOf(run("example-run-1", phase = "red", verdict = "holds", passed = true, exit = 3)), Loader(emptyMap()))

        assertLine(
            0,
            "Red run: the test fails before any change, as it should.",
            "✓ holds",
            ChipTone.Ok,
            "captured · exit 3",
        )
    }

    @Test
    fun epicProofRedRunThatDidNotPassReadsTheTestPassesBeforeAnyChange() {
        setRuns(listOf(run("example-run-1", phase = "red", verdict = "fails", passed = false)), Loader(emptyMap()))

        assertLine(
            0,
            "Red run: the test passes before any change. It should fail.",
            "✗ fails",
            ChipTone.Bad,
            "captured · exit 0",
        )
    }

    @Test
    fun epicProofGreenRunThatPassedReadsTheTestPassesOnTheChange() {
        setRuns(listOf(run("example-run-1", phase = "green", verdict = "holds", passed = true)), Loader(emptyMap()))

        assertLine(0, "Green run: the test passes on the change.", "✓ holds", ChipTone.Ok, "captured · exit 0")
    }

    @Test
    fun epicProofGreenRunThatDidNotPassReadsTheTestStillFailsOnTheChange() {
        setRuns(listOf(run("example-run-1", phase = "green", verdict = "fails", passed = false)), Loader(emptyMap()))

        assertLine(0, "Green run: the test still fails on the change.", "✗ fails", ChipTone.Bad, "captured · exit 0")
    }

    @Test
    fun epicProofRunWithoutAVerdictReadsNoVerdictFollowedByTheJudgeReason() {
        setRuns(
            listOf(run("example-run-1", phase = "green", verdict = "", judgeReason = "example judge reason")),
            Loader(emptyMap()),
        )

        assertLine(
            0,
            "Green run: the test passes on the change.",
            "no verdict · example judge reason",
            ChipTone.Neutral,
            "captured · exit 0",
        )
    }

    @Test
    fun epicProofMetaShowsExitOnlyOnceTheRunHasEndedAndNamesASuppliedRun() {
        setRuns(
            listOf(
                run("example-run-1", phase = "green", source = "supplied", verdict = "holds", passed = true, exit = 7),
            ),
            Loader(emptyMap()),
        )

        assertEquals(1, countIn(rowTag(0), hasText("supplied by the agent · exit 7")))
    }

    @Test
    fun epicProofRunShowsNoRevisionOrItsHash() {
        setRuns(listOf(run("example-run-1")), Loader(emptyMap()))

        assertEquals(0, countIn(rowTag(0), hasText("revision", substring = true)))
        assertEquals(0, countIn(rowTag(0), hasText(REVISION.take(12), substring = true)))
        assertEquals(0, countIn(rowTag(0), hasText(REVISION, substring = true)))
    }

    @Test
    fun epicProofLogToggleOpensTheLogAndHidesItWhenOpen() {
        val loader = Loader(
            mapOf(
                Triple("example-run-1", "", false) to EvidenceArtifactFile(LOG.toByteArray(Charsets.UTF_8), "text/plain"),
            ),
        )
        setRuns(listOf(run("example-run-1", logBytes = LOG.length.toLong())), loader)

        assertEquals(1, countIn(logTag(0), hasText("log")))
        clickTag(logTag(0))

        awaitIn(logTag(0), hasText("hide the log"))
        awaitIn(streamTag(0, "stdout"), hasText("example log line two", substring = true))

        clickTag(logTag(0))

        awaitIn(logTag(0), hasText("log"))
        assertEquals(0, countIn(logTag(0), hasText("hide the log")))
        assertEquals(0, countAll(hasTestTag(streamTag(0, "stdout"))))
    }

    @Test
    fun epicProofOpeningTheLogLoadsItOffTheMainThreadInMonospace() {
        val loader = Loader(
            mapOf(
                Triple("example-run-1", "", false) to EvidenceArtifactFile(LOG.toByteArray(Charsets.UTF_8), "text/plain"),
            ),
        )
        setRuns(listOf(run("example-run-1", logBytes = LOG.length.toLong())), loader)

        clickTag(logTag(0))

        awaitAll(hasText("example log line two", substring = true))
        assertEquals(listOf(Triple("example-run-1", "", false)), loader.calls.toList())
        assertFalse(loader.onMainThread.get())
        assertEquals(
            OrreryTokens.Type.small.fontFamily,
            textLayoutOf(hasText("example log line two", substring = true)).layoutInput.style.fontFamily,
        )
    }

    @Test
    fun epicProofLogShowsLoadingTheLogUntilItArrives() {
        val gate = CountDownLatch(1)
        val loader = Loader(
            mapOf(
                Triple("example-run-1", "", false) to EvidenceArtifactFile(LOG.toByteArray(Charsets.UTF_8), "text/plain"),
            ),
            gate,
        )
        setRuns(listOf(run("example-run-1", logBytes = LOG.length.toLong())), loader)

        clickTag(logTag(0))

        assertEquals(1, countIn(rowTag(0), hasText(LOADING)))

        gate.countDown()
        awaitIn(streamTag(0, "stdout"), hasText("example log line two", substring = true))
        assertEquals(0, countIn(rowTag(0), hasText(LOADING)))
    }

    @Test
    fun epicProofLogSaysItIsNotAvailableWhenTheServerHasNoLog() {
        setRuns(listOf(run("example-run-1", logBytes = LOG.length.toLong())), Loader(emptyMap()))

        clickTag(logTag(0))

        awaitIn(rowTag(0), hasText(NOT_AVAILABLE))
        assertEquals(1, countIn(rowTag(0), hasText(NOT_AVAILABLE)))
    }

    @Test
    fun epicProofLogSaysItIsNotAvailableWhenItsLoadFails() {
        val failing: ArtifactLoader = { _, _, _ -> throw IOException("example failure") }
        composeRule.setContent {
            EvidenceRuns(
                requirement = REQUIREMENT,
                runs = listOf(run("example-run-1", logBytes = LOG.length.toLong())),
                loadArtifact = failing,
            )
        }

        clickTag(logTag(0))

        awaitIn(rowTag(0), hasText(NOT_AVAILABLE))
        assertEquals(1, countIn(rowTag(0), hasText(NOT_AVAILABLE)))
    }

    @Test
    fun epicProofLogReadsStdoutThenStderrOnlyForARunThatHasStderr() {
        val loader = Loader(
            mapOf(
                Triple("example-run-1", "", false) to EvidenceArtifactFile(LOG.toByteArray(Charsets.UTF_8), "text/plain"),
                Triple("example-run-1", "", true) to EvidenceArtifactFile(STDERR.toByteArray(Charsets.UTF_8), "text/plain"),
                Triple("example-run-2", "", false) to EvidenceArtifactFile(LOG.toByteArray(Charsets.UTF_8), "text/plain"),
            ),
        )
        setRuns(
            listOf(
                run("example-run-1", logBytes = LOG.length.toLong(), stderrBytes = STDERR.length.toLong()),
                run("example-run-2", logBytes = LOG.length.toLong(), stderrBytes = 0L),
            ),
            loader,
        )

        clickTag(logTag(0))
        awaitIn(streamTag(0, "stderr"), hasText("example stderr line one", substring = true))

        assertEquals(1, countIn(streamTag(0, "stdout"), hasText("example log line two", substring = true)))
        assertEquals(0, countIn(streamTag(0, "stdout"), hasText("example stderr line one", substring = true)))
        assertEquals(0, countIn(streamTag(0, "stderr"), hasText("example log line two", substring = true)))
        val stdoutTop = composeRule.onNodeWithTag(streamTag(0, "stdout"), useUnmergedTree = true)
            .fetchSemanticsNode().boundsInRoot.top
        val stderrTop = composeRule.onNodeWithTag(streamTag(0, "stderr"), useUnmergedTree = true)
            .fetchSemanticsNode().boundsInRoot.top
        assertTrue(stdoutTop < stderrTop)

        clickTag(logTag(1))
        awaitIn(streamTag(1, "stdout"), hasText("example log line two", substring = true))

        assertEquals(0, countAll(hasTestTag(streamTag(1, "stderr"))))
        assertEquals(
            listOf(
                Triple("example-run-1", "", false),
                Triple("example-run-1", "", true),
                Triple("example-run-2", "", false),
            ),
            loader.calls.toList(),
        )
        assertFalse(loader.onMainThread.get())
    }

    @Test
    fun epicProofKeptImageFoldsWithItsLogAndIsDrawn() {
        val artifact = EvidenceArtifactRow(
            path = "example/screenshot.png",
            state = "kept",
            size = PNG.size.toLong(),
            mediaType = "image/png",
        )
        val loader = Loader(
            mapOf(Triple("example-run-1", "example/screenshot.png", false) to EvidenceArtifactFile(PNG, "image/png")),
        )
        setRuns(listOf(run("example-run-1", artifacts = listOf(artifact))), loader)

        assertEquals(0, countAll(hasTestTag(IMAGE_TAG)))
        clickTag(logTag(0))

        awaitAll(hasTestTag(IMAGE_TAG))

        assertEquals(1, countAll(hasTestTag(IMAGE_TAG)))
        assertEquals(1, countIn(rowTag(0), hasText("example/screenshot.png · kept · ${PNG.size} bytes")))
        assertTrue(loader.calls.contains(Triple("example-run-1", "example/screenshot.png", false)))
        assertTrue(composeRule.onNodeWithTag(IMAGE_TAG, useUnmergedTree = true).fetchSemanticsNode().size.width > 0)
    }

    @Test
    fun epicProofMissingArtifactLineSaysMissingOnceTheLogIsOpen() {
        val artifact = EvidenceArtifactRow(
            path = "example/screenshot.png",
            state = "missing",
            size = 0L,
            mediaType = "image/png",
        )
        val loader = Loader(emptyMap())
        setRuns(listOf(run("example-run-1", artifacts = listOf(artifact))), loader)
        val line = "example/screenshot.png · missing · 0 bytes"

        assertEquals(0, countIn(rowTag(0), hasText(line)))
        clickTag(logTag(0))

        awaitIn(rowTag(0), hasText(line))

        assertEquals(1, countIn(rowTag(0), hasText(line)))
        assertEquals(0, countAll(hasTestTag(IMAGE_TAG)))
        assertEquals(0, loader.calls.count { it.second == "example/screenshot.png" })
    }

    @Test
    fun epicProofOverCapArtifactLineSaysTooLargeToKeepOnceTheLogIsOpen() {
        val artifact = EvidenceArtifactRow(
            path = "example/report.json",
            state = "over_cap",
            size = 9_000_000L,
            mediaType = "application/json",
        )
        val loader = Loader(emptyMap())
        setRuns(listOf(run("example-run-1", artifacts = listOf(artifact))), loader)
        val line = "example/report.json · too large to keep · 9000000 bytes"

        assertEquals(0, countIn(rowTag(0), hasText(line)))
        clickTag(logTag(0))

        awaitIn(rowTag(0), hasText(line))

        assertEquals(1, countIn(rowTag(0), hasText(line)))
        assertEquals(0, loader.calls.count { it.second == "example/report.json" })
    }

    @Test
    fun epicProofKeptImageTheServerNoLongerHoldsSaysMissing() {
        val artifact = EvidenceArtifactRow(
            path = "example/screenshot.png",
            state = "kept",
            size = PNG.size.toLong(),
            mediaType = "image/png",
        )
        setRuns(listOf(run("example-run-1", artifacts = listOf(artifact))), Loader(emptyMap()))

        clickTag(logTag(0))

        awaitIn(rowTag(0), hasText("missing"))

        assertEquals(1, countIn(rowTag(0), hasText("missing")))
        assertEquals(0, countAll(hasTestTag(IMAGE_TAG)))
    }

    @Test
    fun epicProofALegacyRequirementShowsNoRuns() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                epicBody(
                    EpicRequirement.newBuilder()
                        .setId("R1")
                        .setText("Example legacy requirement.")
                        .setEvidence("example check")
                        .setKind("check")
                        .build(),
                ),
            )
            setScreen(server)
            awaitAll(hasText("Example legacy requirement."))

            assertEquals(0, countAll(hasTestTag("evidence-run:0:0")))
            assertEquals(0, countAll(hasTestTag("evidence-log:0:0")))
        }
    }

    @Test
    fun epicProofScreenOpensTheLogOverItsClientAndFetchesIt() {
        val redRun = EvidenceRun.newBuilder()
            .setId("example-run-1")
            .setPhase("red")
            .setSource("captured")
            .setState("ended")
            .setVerdict("fails")
            .setPassed(true)
            .setRevision(REVISION)
            .setCreated(CHANGED - 600)
            .setEnded(CHANGED - 60)
            .setLogBytes(LOG.length.toLong())
            .build()
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                epicBody(
                    EpicRequirement.newBuilder()
                        .setId("R1")
                        .setText("Example requirement.")
                        .setEvidence("example check")
                        .setKind("check")
                        .setConclusion("example conclusion")
                        .addRuns(redRun)
                        .build(),
                ),
            )
            server.respond(
                EVIDENCE_ARTIFACT,
                EvidenceArtifactResponse.newBuilder()
                    .setData(ByteString.copyFromUtf8(LOG))
                    .setMediaType("text/plain")
                    .build(),
            )
            setScreen(server)
            awaitAll(hasText("Example requirement."))
            clickTag("req-row:R1")
            awaitIn("evidence-run:0:0", hasText("Red run: the test fails before any change, as it should."))

            clickTag("evidence-log:0:0")
            awaitIn("evidence-stream:0:0:stdout", hasText("example log line two", substring = true))

            val request = server.requests.last { it.rawPath == EVIDENCE_ARTIFACT }
            val sent = EvidenceArtifactRequest.parseFrom(request.body)
            assertEquals("example-run-1", sent.run)
            assertEquals("", sent.name)
        }
    }

    @Test
    fun epicProofScreenFetchesTheStderrLogWithItsFlagAndShowsItUnderTheLog() {
        val redRun = EvidenceRun.newBuilder()
            .setId("example-run-1")
            .setPhase("red")
            .setSource("captured")
            .setState("ended")
            .setVerdict("fails")
            .setPassed(true)
            .setRevision(REVISION)
            .setCreated(CHANGED - 600)
            .setEnded(CHANGED - 60)
            .setLogBytes(LOG.length.toLong())
            .setStderrBytes(STDERR.length.toLong())
            .build()
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                epicBody(
                    EpicRequirement.newBuilder()
                        .setId("R1")
                        .setText("Example requirement.")
                        .setEvidence("example check")
                        .setKind("check")
                        .setConclusion("example conclusion")
                        .addRuns(redRun)
                        .build(),
                ),
            )
            server.route(EVIDENCE_ARTIFACT) { request ->
                val sent = EvidenceArtifactRequest.parseFrom(request.body)
                val text = if (sent.stderr) STDERR else LOG
                Reply.Message(
                    EvidenceArtifactResponse.newBuilder()
                        .setData(ByteString.copyFromUtf8(text))
                        .setMediaType("text/plain")
                        .build(),
                    0L,
                )
            }
            setScreen(server)
            awaitAll(hasText("Example requirement."))
            clickTag("req-row:R1")
            awaitIn("evidence-run:0:0", hasText("Red run: the test fails before any change, as it should."))

            clickTag("evidence-log:0:0")
            awaitIn("evidence-stream:0:0:stderr", hasText("example stderr line one", substring = true))

            assertEquals(1, countIn("evidence-stream:0:0:stdout", hasText("example log line two", substring = true)))
            assertEquals(1, countIn("evidence-stream:0:0:stderr", hasText("example stderr line one", substring = true)))
            assertEquals(0, countIn("evidence-stream:0:0:stderr", hasText("example log line two", substring = true)))
            val sent = server.requests
                .filter { it.rawPath == EVIDENCE_ARTIFACT }
                .map { EvidenceArtifactRequest.parseFrom(it.body) }
            assertEquals(listOf(false, true), sent.map { it.stderr })
            assertEquals(listOf("", ""), sent.map { it.name })
        }
    }
}
