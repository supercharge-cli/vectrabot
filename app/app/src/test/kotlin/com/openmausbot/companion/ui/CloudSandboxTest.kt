package com.openmausbot.companion.ui

import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class CloudSandboxTest {
    private lateinit var server: MockWebServer
    private val client = CloudSandboxClient()

    @Before fun start() { server = MockWebServer().also { it.start() } }
    @After fun stop() { server.shutdown() }

    @Test fun turnPostsToTheHostWithTheDeviceSession() {
        server.enqueue(MockResponse().setBody("""{"engine":"supercharge","output":"pong"}"""))
        val reply = client.turn(server.url("/").toString(), "device-session", "ping", "reviewer")
        assertEquals("pong", reply)
        val request = server.takeRequest()
        assertEquals("/v1/turns", request.path)
        assertEquals("Bearer device-session", request.getHeader("authorization"))
        val body = JSONObject(request.body.readUtf8())
        assertEquals("supercharge", body.getString("engine"))
        assertEquals("ping", body.getString("prompt"))
        assertEquals("reviewer", body.getString("agentId"))
    }

    @Test fun turnForTheMainAgentSendsNoAgentAndSurfacesTheCliError() {
        server.enqueue(MockResponse().setResponseCode(502).setBody("""{"error":"No provider credentials are configured."}"""))
        val failure = runCatching { client.turn(server.url("/").toString(), "s", "ping", null) }.exceptionOrNull()
        assertEquals("No provider credentials are configured.", failure?.message)
        assertFalse(JSONObject(server.takeRequest().body.readUtf8()).has("agentId"))
    }

    @Test fun vncTicketUsesTheCloudTokenAndReturnsTheSocket() {
        server.enqueue(MockResponse().setBody("""{"ticket":"t","wsUrl":"wss://8790-sbx.e2b.app/v1/computer/browser/vnc?ticket=t"}"""))
        val url = client.vncTicket(server.url("/").toString(), "cloud-token", "inst-1", "abcdefabcdefabcdef")
        assertEquals("wss://8790-sbx.e2b.app/v1/computer/browser/vnc?ticket=t", url)
        val request = server.takeRequest()
        assertEquals("/api/instances/inst-1/computer/vnc-ticket", request.path)
        assertEquals("Bearer cloud-token", request.getHeader("authorization"))
        assertEquals("abcdefabcdefabcdef", JSONObject(request.body.readUtf8()).getString("handoffId"))
        server.enqueue(MockResponse().setResponseCode(409).setBody("""{"error":"Take control before opening the desktop"}"""))
        val refused = runCatching { client.vncTicket(server.url("/").toString(), "c", "inst-1", "abcdefabcdefabcdef") }.exceptionOrNull()
        assertEquals("Take control before opening the desktop", refused?.message)
    }

    @Test fun agentsListSkipsRowsWithoutAnId() {
        val state = JSONObject("""{"agents":[{"id":"main","name":"Chief"},{"name":"nameless"},{"id":"github"}]}""")
        assertEquals(listOf("main" to "Chief", "github" to "github"), sandboxAgents(state))
        assertEquals(emptyList<Pair<String, String>>(), sandboxAgents(JSONObject()))
    }

    @Test fun viewerPageQuotesTheSocketUrl() {
        val html = desktopViewerHtml("wss://h/v1?ticket=a\"</script><script>alert(1)")
        assertTrue(html.contains("new RFB("))
        assertFalse(html.contains("</script><script>alert"))
    }

    @Test fun agentsAreReadAddedAndRemovedOnTheHost() {
        val host = server.url("/").toString()
        server.enqueue(MockResponse().setBody("""{"agents":[{"id":"main","name":"Chief"},{"id":"reviewer","name":"Reviewer","instructions":"Review"}]}"""))
        assertEquals(listOf(SandboxAgent("main", "Chief", ""), SandboxAgent("reviewer", "Reviewer", "Review")), client.agents(host, "s"))
        assertEquals("/v1/agents", server.takeRequest().path)
        server.enqueue(MockResponse().setBody("""{"agent":{}}"""))
        client.putAgent(host, "s", " Code Reviewer! ", "Be strict")
        val put = server.takeRequest()
        assertEquals("PUT", put.method)
        val body = JSONObject(put.body.readUtf8())
        assertEquals("code-reviewer", body.getString("id"))
        assertEquals("Be strict", body.getString("instructions"))
        server.enqueue(MockResponse().setBody("""{"agents":[]}"""))
        client.deleteAgent(host, "s", "code-reviewer")
        val delete = server.takeRequest()
        assertEquals("DELETE", delete.method)
        assertEquals("/v1/agents?id=code-reviewer", delete.path)
    }

    @Test fun portsPreviewAndClipboard() {
        val host = server.url("/").toString()
        server.enqueue(MockResponse().setBody("""{"ports":[3000,5173,0]}"""))
        assertEquals(listOf(3000, 5173), client.ports(host, "s"))
        server.enqueue(MockResponse().setResponseCode(502).setBody("""{"listening":false,"port":4321}"""))
        assertFalse(client.listening(host, "s", 4321))
        server.enqueue(MockResponse().setBody("""{"listening":true,"port":3000}"""))
        assertTrue(client.listening(host, "s", 3000))
        server.enqueue(MockResponse().setBody("""{"text":"copied"}"""))
        assertEquals("copied", client.clipboard(host, "s"))
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        client.sendClipboard(host, "s", "from phone")
        repeat(4) { server.takeRequest() }
        val sent = server.takeRequest()
        assertEquals("POST", sent.method)
        assertEquals("/v1/computer/clipboard", sent.path)
        assertEquals("from phone", JSONObject(sent.body.readUtf8()).getString("text"))
        assertEquals("https://3000-sbx.e2b.app", previewUrl("3000-sbx.e2b.app"))
        assertEquals("https://3000-sbx.e2b.app/", previewUrl("https://3000-sbx.e2b.app/"))
    }

    @Test fun framesLongPollAndAnExpiredSessionIsReported() {
        val host = server.url("/").toString()
        server.enqueue(MockResponse().setBody(okio.Buffer().write(byteArrayOf(1, 2, 3))).setHeader("X-Frame-Version", "7"))
        val frame = client.frame(host, "s", 0)
        assertEquals(7L, frame?.version)
        assertEquals(3, frame?.jpeg?.size)
        assertEquals("/v1/computer/frame?after=0", server.takeRequest().path)
        server.enqueue(MockResponse().setResponseCode(204))
        assertEquals(null, client.frame(host, "s", 7))
        server.enqueue(MockResponse().setResponseCode(401))
        assertTrue(runCatching { client.frame(host, "s", 7) }.exceptionOrNull() is SessionExpired)
    }

    @Test fun terminalUrlControlFramesAndScreen() {
        assertEquals("wss://8790-sbx.e2b.app/v1/terminal?token=a%2Bb&session=shell1234567890abcd",
            terminalUrl("https://8790-sbx.e2b.app/", "a+b", "shell1234567890abcd"))
        assertTrue(newShellId().matches(Regex("^[A-Za-z0-9_-]{16,64}$")))
        assertEquals(TerminalControl.Attached(true), terminalControl("""{"type":"attached","resumed":true}"""))
        assertEquals(TerminalControl.Exited, terminalControl("""{"type":"exit"}"""))
        assertEquals(null, terminalControl("ls -la"))
        val screen = TerminalScreen(limit = 1000)
        screen.append("\u001b[01;32muser@e2b\u001b[00m:~$ echo hi\r\nhi\r\n")
        screen.append("progress 10%\rprogress 99%\r\n")
        screen.append("abc\b\bX\u001b]0;title\u0007 done")
        assertEquals("user@e2b:~$ echo hi\nhi\nprogress 99%\naX done", screen.value)
        val small = TerminalScreen(limit = 10)
        small.append("0123456789abcdef")
        assertEquals("6789abcdef", small.value)
    }
}
