package com.openmausbot.companion.ui

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.util.concurrent.TimeUnit

private val sandboxJson = "application/json; charset=utf-8".toMediaType()

/**
 * Calls that go straight to the workspace host in the sandbox (with this
 * device's session) or to the cloud's desktop ticket route.
 */
internal class CloudSandboxClient(
    private val http: OkHttpClient = OkHttpClient.Builder()
        // A turn runs the agent to completion; the host stops it after 10 minutes.
        .callTimeout(11, TimeUnit.MINUTES)
        .readTimeout(11, TimeUnit.MINUTES)
        .build(),
) {
    /** One Supercharge turn. Returns the agent's reply, or throws with its error. */
    fun turn(host: String, session: String, prompt: String, agentId: String?): String {
        val body = JSONObject().put("engine", "supercharge").put("prompt", prompt)
        if (!agentId.isNullOrBlank()) body.put("agentId", agentId)
        val request = Request.Builder()
            .url(host.trimEnd('/') + "/v1/turns")
            .header("authorization", "Bearer $session")
            .post(body.toString().toRequestBody(sandboxJson))
            .build()
        val reply = read(request)
        return reply.optString("output").ifBlank { "(no reply)" }
    }

    /** A one-use desktop viewer URL for the takeover the person now holds. */
    fun vncTicket(cloudBase: String, cloudToken: String, instanceId: String, handoffId: String): String {
        val request = Request.Builder()
            .url(cloudBase.trimEnd('/') + "/api/instances/$instanceId/computer/vnc-ticket")
            .header("authorization", "Bearer $cloudToken")
            .post(JSONObject().put("handoffId", handoffId).put("botId", "main").toString().toRequestBody(sandboxJson))
            .build()
        val reply = read(request)
        val url = reply.optString("wsUrl")
        if (!url.startsWith("wss://") && !url.startsWith("ws://")) throw IllegalStateException(reply.optString("error").ifBlank { "Desktop connection unavailable" })
        return url
    }

    private fun read(request: Request): JSONObject {
        http.newCall(request).execute().use { response ->
            val text = response.body?.string().orEmpty()
            val json = if (text.trimStart().startsWith("{")) JSONObject(text) else JSONObject()
            if (!response.isSuccessful) {
                throw IllegalStateException(json.optString("error").ifBlank { "Request failed (${response.code})" })
            }
            return json
        }
    }
}

/** Agents from sandbox-state as (id, name); the main agent is sent without an id. */
internal fun sandboxAgents(state: JSONObject): List<Pair<String, String>> {
    val rows = state.optJSONArray("agents") ?: return emptyList()
    return (0 until rows.length()).mapNotNull { i ->
        val agent = rows.optJSONObject(i) ?: return@mapNotNull null
        val id = agent.optString("id")
        if (id.isBlank()) null else id to agent.optString("name").ifBlank { id }
    }
}

/** A page that shows the desktop over the ticketed socket with noVNC. */
internal fun desktopViewerHtml(wsUrl: String): String {
    // JSONObject.quote keeps the URL a string literal whatever it contains.
    val target = JSONObject.quote(wsUrl).replace("</", "<\\/")
    return """
        <!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1">
        <style>html,body,#screen{margin:0;height:100%;background:#000;color:#ccc;font:14px sans-serif}</style></head>
        <body><div id="screen"></div><script type="module">
        const { default: RFB } = await import("https://esm.sh/@novnc/novnc@1.5.0");
        const rfb = new RFB(document.getElementById("screen"), $target, { shared: true });
        rfb.scaleViewport = true;
        rfb.addEventListener("disconnect", () => { document.body.textContent = "Desktop disconnected"; });
        </script></body></html>
    """.trimIndent()
}
