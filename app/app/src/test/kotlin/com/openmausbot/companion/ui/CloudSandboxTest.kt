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
}
