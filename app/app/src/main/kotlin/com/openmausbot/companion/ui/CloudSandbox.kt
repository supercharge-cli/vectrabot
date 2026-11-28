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

    /** The workspace's agents, straight from the host. */
    fun agents(host: String, session: String): List<SandboxAgent> {
        val rows = read(hostRequest(host, session, "/v1/agents").get().build()).optJSONArray("agents") ?: return emptyList()
        return (0 until rows.length()).mapNotNull { i ->
            val agent = rows.optJSONObject(i) ?: return@mapNotNull null
            val id = agent.optString("id")
            if (id.isBlank()) null else SandboxAgent(id, agent.optString("name").ifBlank { id }, agent.optString("instructions"))
        }
    }

    fun putAgent(host: String, session: String, name: String, instructions: String) {
        val id = agentIdFor(name)
        if (id.isBlank()) throw IllegalArgumentException("Give the agent a name with letters or numbers")
        val body = JSONObject().put("id", id).put("name", name.trim()).put("instructions", instructions.trim()).put("engine", "supercharge")
        read(hostRequest(host, session, "/v1/agents").put(body.toString().toRequestBody(sandboxJson)).build())
    }

    fun deleteAgent(host: String, session: String, id: String) {
        read(hostRequest(host, session, "/v1/agents?id=" + java.net.URLEncoder.encode(id, "UTF-8")).delete().build())
    }

    /** Ports where an app in the workspace is listening (the sandbox's own services left out). */
    fun ports(host: String, session: String): List<Int> {
        val rows = read(hostRequest(host, session, "/v1/ports").get().build()).optJSONArray("ports") ?: return emptyList()
        return (0 until rows.length()).map { rows.optInt(it) }.filter { it in 1..65535 }
    }

    /** Whether something answers on [port] inside the workspace. */
    fun listening(host: String, session: String, port: Int): Boolean =
        http.newCall(hostRequest(host, session, "/v1/preview?port=$port").get().build()).execute().use { response ->
            val text = response.body?.string().orEmpty()
            text.trimStart().startsWith("{") && JSONObject(text).optBoolean("listening")
        }

    /**
     * The next desktop frame after [after] (a JPEG), or null when the screen has
     * not changed. View only: nothing here can send input to the desktop.
     */
    fun frame(host: String, session: String, after: Long): DesktopFrame? {
        http.newCall(hostRequest(host, session, "/v1/computer/frame?after=$after").get().build()).execute().use { response ->
            if (response.code == 204) return null
            if (response.code == 401) throw SessionExpired("Session expired")
            val bytes = response.body?.bytes() ?: ByteArray(0)
            if (!response.isSuccessful) {
                val text = String(bytes)
                val error = if (text.trimStart().startsWith("{")) JSONObject(text).optString("error") else ""
                throw IllegalStateException(error.ifBlank { "Desktop view unavailable (${response.code})" })
            }
            return DesktopFrame(response.header("X-Frame-Version")?.toLongOrNull() ?: (after + 1), bytes)
        }
    }

    fun clipboard(host: String, session: String): String =
        read(hostRequest(host, session, "/v1/computer/clipboard").get().build()).optString("text")

    fun sendClipboard(host: String, session: String, text: String) {
        read(hostRequest(host, session, "/v1/computer/clipboard").post(JSONObject().put("text", text).toString().toRequestBody(sandboxJson)).build())
    }

    private fun hostRequest(host: String, session: String, path: String): Request.Builder =
        Request.Builder().url(host.trimEnd('/') + path).header("authorization", "Bearer $session")

    private fun read(request: Request): JSONObject {
        http.newCall(request).execute().use { response ->
            val text = response.body?.string().orEmpty()
            val json = if (text.trimStart().startsWith("{")) JSONObject(text) else JSONObject()
            if (response.code == 401) throw SessionExpired(json.optString("error").ifBlank { "Session expired" })
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
        <style>html,body{margin:0;height:100%;background:#111;color:#ddd;font:14px sans-serif}#screen{position:absolute;inset:0;overflow:hidden}#note{position:absolute;left:12px;right:12px;bottom:12px;z-index:2}</style></head>
        <body><div id="screen"></div><div id="note">Connecting to the desktop…</div>
        <input id="keys" autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false" style="position:absolute;left:0;bottom:0;width:1px;height:1px;opacity:0">
        <script src="https://desktop.vectrabot.invalid/novnc/rfb-bundle.js"></script>
        <script>
        const note = document.getElementById("note");
        const RFB = (window.NovncRFB && (window.NovncRFB.default || window.NovncRFB));
        if (typeof RFB !== "function") { note.textContent = "Desktop viewer failed to load"; }
        else {
          const screen = document.getElementById("screen");
          const rfb = new RFB(screen, $target, { shared: true });
          rfb.scaleViewport = true;
          rfb.resizeSession = false;
          rfb.qualityLevel = 6;
          rfb.compressionLevel = 2;
          const fit = () => { screen.style.width = window.innerWidth + "px"; screen.style.height = window.innerHeight + "px"; };
          fit();
          window.addEventListener("resize", fit);
          rfb.addEventListener("connect", () => { note.textContent = ""; fit(); });
          rfb.addEventListener("securityfailure", (e) => { note.textContent = "Desktop refused the connection"; });
          rfb.addEventListener("disconnect", (e) => {
            if (e.detail && e.detail.clean) note.textContent = "Desktop disconnected";
            else note.textContent = "Desktop connection failed. Tap Reconnect.";
          });
          const keys = document.getElementById("keys");
          const named = { Backspace: 0xff08, Enter: 0xff0d, Tab: 0xff09, Escape: 0xff1b, ArrowLeft: 0xff51, ArrowUp: 0xff52, ArrowRight: 0xff53, ArrowDown: 0xff54, Delete: 0xffff };
          function tap(sym) { rfb.sendKey(sym, null, true); rfb.sendKey(sym, null, false); }
          keys.addEventListener("keydown", (e) => {
            const sym = named[e.key];
            if (sym) { e.preventDefault(); tap(sym); return; }
            if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
              e.preventDefault();
              tap(e.key.charCodeAt(0));
            }
          });
          window.focusKeys = () => { keys.focus(); };
        }
        </script></body></html>
    """.trimIndent()
}

internal data class SandboxAgent(val id: String, val name: String, val instructions: String)

internal class DesktopFrame(val version: Long, val jpeg: ByteArray)

/** The id an agent gets from its name, as the PC app makes it. */
internal fun agentIdFor(name: String): String =
    name.trim().lowercase().replace(Regex("[^a-z0-9]+"), "-").trim('-').take(64)

private val localServerLink = Regex("""(?i)(?:https?://)?(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d{2,5})(/[^\s)<]*)?""")

/** Rewrites loopback server links into https://port-sandbox.domain using the workspace host. */
internal fun publicizeLinks(text: String, host: String): String {
    val match = Regex("""^https?://\d+-([A-Za-z0-9]+)\.([^/?#]+)""").find(host.trim()) ?: return text
    val id = match.groupValues[1]
    val domain = match.groupValues[2]
    return localServerLink.replace(text) { found ->
        val port = found.groupValues[1].toIntOrNull()
        if (port == null || port !in 1..65535) found.value
        else "https://$port-$id.$domain${found.groupValues.getOrElse(2) { "" }}"
    }
}

/** Preview hosts may come without a scheme; the page is always served over https. */
internal fun previewUrl(host: String): String {
    val trimmed = host.trim()
    if (trimmed.isEmpty()) return ""
    return if (trimmed.startsWith("https://") || trimmed.startsWith("http://")) trimmed else "https://$trimmed"
}

/** A terminal that resumes the same shell when it reconnects with [shellId]. */
internal fun terminalUrl(host: String, session: String, shellId: String): String {
    val base = host.trim().trimEnd('/').replaceFirst(Regex("^https://"), "wss://").replaceFirst(Regex("^http://"), "ws://")
    val enc = { v: String -> java.net.URLEncoder.encode(v, "UTF-8") }
    return "$base/v1/terminal?token=${enc(session)}&session=${enc(shellId)}"
}

/** A shell id the host accepts (16-64 of A-Z a-z 0-9 _ -). */
internal fun newShellId(): String = java.util.UUID.randomUUID().toString().replace("-", "")

internal sealed interface TerminalControl {
    data class Attached(val resumed: Boolean) : TerminalControl
    data object Exited : TerminalControl
}

/** Text frames on the terminal socket are control messages, never shell output. */
internal fun terminalControl(text: String): TerminalControl? {
    val json = runCatching { JSONObject(text) }.getOrNull() ?: return null
    return when (json.optString("type")) {
        "attached" -> TerminalControl.Attached(json.optBoolean("resumed"))
        "exit" -> TerminalControl.Exited
        else -> null
    }
}

/**
 * Plain-text view of terminal output for a phone screen: escape sequences
 * dropped, carriage returns and backspaces applied, the tail kept.
 */
internal class TerminalScreen(private val limit: Int = 24_000) {
    private val text = StringBuilder()
    private var escape = 0 // 0 none, 1 after ESC, 2 CSI, 3 OSC
    private var lineStart = 0

    val value: String get() = text.toString()

    fun clear() { text.setLength(0); lineStart = 0; escape = 0 }

    fun append(chunk: String) {
        for (c in chunk) {
            when (escape) {
                1 -> escape = when (c) { '[' -> 2; ']' -> 3; else -> 0 }
                2 -> if (c in '@'..'~') escape = 0
                3 -> if (c == '\u0007' || c == '\u001b') escape = if (c == '\u001b') 1 else 0
                else -> when (c) {
                    '\u001b' -> escape = 1
                    // A carriage return moves to the line start: what follows overwrites it.
                    '\r' -> pendingReturn = true
                    '\n' -> { pendingReturn = false; text.append('\n'); lineStart = text.length }
                    '\b' -> if (text.length > lineStart) text.setLength(text.length - 1)
                    '\u0007' -> Unit
                    else -> if (c >= ' ' || c == '\t') {
                        if (pendingReturn) { text.setLength(lineStart); pendingReturn = false }
                        text.append(c)
                    }
                }
            }
        }
        if (text.length > limit) {
            val cut = text.length - limit
            text.delete(0, cut)
            lineStart = (lineStart - cut).coerceAtLeast(0)
        }
    }

    private var pendingReturn = false
}

/** The host or the cloud refused the credential: attach (or sign in) again. */
internal class SessionExpired(message: String) : IllegalStateException(message)
