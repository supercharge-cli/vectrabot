package com.openmausbot.companion.ui

import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.util.concurrent.TimeUnit

/*
 * The workspace host's bot API under /api: bots, their threads, streamed chat
 * over server-sent events, agent requests (permissions, questions, plans),
 * providers and routines. Everything runs in the sandbox; this is a client.
 */

private val botsJson = "application/json; charset=utf-8".toMediaType()

internal data class BotSettings(val model: String = "", val permissionMode: String = "ask", val mode: String = "default")
internal data class BotThread(val id: String, val title: String, val busy: Boolean, val preview: String, val updatedAt: Long, val pinned: Boolean)
internal data class Bot(
    val id: String,
    val name: String,
    val title: String,
    val instructions: String,
    val color: String,
    val settings: BotSettings,
    val threadId: String,
    val busy: Boolean,
    val preview: String,
    val lastAt: Long,
    val threads: List<BotThread>,
    val description: String = "",
    /** The workspace's built-in bot: listed first, never deleted. */
    val main: Boolean = false,
    val pinned: Boolean = false,
    /** generated | initial | emoji | image */
    val avatarKind: String = "generated",
    val emoji: String = "",
    /** circle | rounded | square */
    val avatarShape: String = "circle",
    /** Host path of the uploaded image, null when there is none. */
    val avatarUrl: String? = null,
    /** 15-bit pattern of the generated face; -1 when the host sent none. */
    val glyph: Int = -1,
    val createdAt: Long = 0,
)

internal data class ToolDiff(val path: String, val oldText: String, val newText: String)
internal data class ToolCall(val id: String, val title: String, val kind: String, val status: String, val input: String, val output: String, val diffs: List<ToolDiff>)
internal data class PermissionChoice(val id: String, val name: String, val kind: String)
internal data class QuestionChoice(val label: String, val description: String)
internal data class Question(val question: String, val header: String, val options: List<QuestionChoice>, val multiSelect: Boolean)
internal data class AgentRequest(
    val id: String,
    val type: String,
    val state: String,
    val title: String,
    val options: List<PermissionChoice>,
    val questions: List<Question>,
    val plan: String,
    val mode: String,
    val answer: String,
)
internal data class PlanStep(val content: String, val status: String)
internal data class FileChange(val path: String, val kind: String)
internal data class BotMessage(
    val id: String,
    val threadId: String,
    val role: String,
    val kind: String,
    val at: Long,
    val text: String,
    val error: Boolean,
    val errorCode: Int,
    val action: String,
    val tool: ToolCall?,
    val request: AgentRequest?,
    val entries: List<PlanStep>,
    val changes: List<FileChange>,
) {
    val fromUser get() = role == "user"
}

internal data class ConfigChoice(val value: String, val name: String)
internal data class ConfigOption(val id: String, val name: String, val current: String, val choices: List<ConfigChoice>)
internal data class AgentCommand(val name: String, val description: String, val hint: String)
internal data class SessionInfo(
    val threadId: String,
    val open: Boolean,
    val busy: Boolean,
    val queued: Int,
    val mode: String,
    val modes: List<ConfigChoice>,
    val options: List<ConfigOption>,
    val commands: List<AgentCommand>,
    val usedTokens: Long,
    val maxTokens: Long,
) {
    companion object { val Empty = SessionInfo("", false, false, 0, "default", emptyList(), emptyList(), emptyList(), 0, 0) }
}

internal data class Provider(val id: String, val name: String, val description: String, val auth: List<String>, val baseUrl: String, val defaultModel: String, val signedIn: Boolean)
internal data class ProviderStatus(val configured: Boolean, val providerId: String, val baseUrl: String, val modelId: String, val hasApiKey: Boolean, val providers: List<Provider>)
internal data class LoginStart(val loginId: String, val url: String, val userCode: String, val interval: Int)
internal data class Routine(val id: String, val name: String, val prompt: String, val botId: String, val enabled: Boolean, val type: String, val time: String, val everyMinutes: Int, val nextRunAt: Long, val lastRunAt: Long)

/** One SSE frame from /api/events. */
internal data class HostFrame(val kind: String, val json: JSONObject)

internal class HostError(val status: Int, message: String) : IOException(message)

private fun JSONObject.str(key: String) = if (isNull(key)) "" else optString(key)
private inline fun <T> JSONArray?.mapObjects(f: (JSONObject) -> T?): List<T> =
    if (this == null) emptyList() else (0 until length()).mapNotNull { i -> optJSONObject(i)?.let(f) }

internal fun parseBot(o: JSONObject): Bot {
    val s = o.optJSONObject("settings") ?: JSONObject()
    return Bot(
        id = o.str("id"), name = o.str("name").ifBlank { "Bot" }, title = o.str("title"), instructions = o.str("instructions"),
        color = o.str("color"),
        settings = BotSettings(s.str("model"), s.str("permissionMode").ifBlank { "ask" }, s.str("mode").ifBlank { "default" }),
        threadId = o.str("threadId"), busy = o.optBoolean("busy"), preview = o.str("preview"), lastAt = o.optLong("lastAt"),
        threads = o.optJSONArray("tasks").mapObjects { t ->
            BotThread(t.str("threadId"), t.str("title").ifBlank { "New thread" }, t.optBoolean("busy"), t.str("preview"), t.optLong("updatedAt"), t.optBoolean("pinned"))
        },
        description = o.str("description"), main = o.optBoolean("main"), pinned = o.optBoolean("pinned"),
        avatarKind = o.str("avatarKind").ifBlank { "generated" }, emoji = o.str("emoji"), avatarShape = o.str("avatarShape").ifBlank { "circle" },
        avatarUrl = o.str("avatarUrl").ifBlank { null }, glyph = if (o.has("glyph") && !o.isNull("glyph")) o.optInt("glyph", -1) else -1,
        createdAt = o.optLong("createdAt"),
    )
}

/** The generated face's pattern for a seed; the host and the PC app compute the same value. */
internal fun glyphOf(seed: String): Int {
    var x = 0x811c9dc5.toInt()
    for (b in seed.toByteArray(Charsets.UTF_8)) { x = x xor (b.toInt() and 0xff); x *= 0x01000193 }
    var g = x and 0x7fff
    while (Integer.bitCount(g) < 4 || Integer.bitCount(g) > 11) g = (g * 31 + 0x2b5d) and 0x7fff
    return g
}

/** The order every client lists bots in: the main bot, pinned ones, then latest activity. */
internal fun sortBots(list: List<Bot>): List<Bot> = list.sortedWith(
    compareByDescending<Bot> { it.main }.thenByDescending { it.pinned }.thenByDescending { maxOf(it.lastAt, it.createdAt) }.thenBy { it.createdAt },
)

internal fun parseMessage(o: JSONObject): BotMessage {
    val tool = o.optJSONObject("tool")?.let { t ->
        ToolCall(
            t.str("id"), t.str("title").ifBlank { t.str("name").ifBlank { "Tool" } }, t.str("kind"), t.str("status"), t.str("input"), t.str("output"),
            t.optJSONArray("diffs").mapObjects { d -> ToolDiff(d.str("path"), d.str("oldText"), d.str("newText")) },
        )
    }
    val req = o.optJSONObject("request")?.let { r ->
        AgentRequest(
            id = r.str("id"), type = r.str("type"), state = r.str("state"), title = r.str("title"),
            options = r.optJSONArray("options").mapObjects { p -> PermissionChoice(p.str("optionId"), p.str("name").ifBlank { p.str("optionId") }, p.str("kind")) },
            questions = r.optJSONArray("questions").mapObjects { q ->
                Question(q.str("question"), q.str("header"), q.optJSONArray("options").mapObjects { c -> QuestionChoice(c.str("label"), c.str("description")) }, q.optBoolean("multiSelect"))
            },
            plan = r.str("plan"), mode = r.str("mode"), answer = r.opt("answer")?.takeIf { it != JSONObject.NULL }?.toString().orEmpty(),
        )
    }
    return BotMessage(
        id = o.str("id"), threadId = o.str("threadId"), role = o.str("role"), kind = o.str("kind").ifBlank { "text" }, at = o.optLong("at"),
        text = o.str("text"), error = o.optBoolean("error"), errorCode = o.optInt("errorCode"), action = o.str("action"),
        tool = tool, request = req,
        entries = o.optJSONArray("entries").mapObjects { e -> PlanStep(e.str("content"), e.str("status")) },
        changes = o.optJSONArray("changes").mapObjects { c -> FileChange(c.str("path"), c.str("kind")) },
    )
}

internal fun parseSession(o: JSONObject): SessionInfo {
    val modes = o.optJSONObject("modes")?.optJSONArray("availableModes").mapObjects { m -> ConfigChoice(m.str("id"), m.str("name").ifBlank { m.str("id") }) }
    val options = o.optJSONArray("configOptions").mapObjects { c ->
        if (c.has("type") && c.str("type") != "select") return@mapObjects null
        ConfigOption(c.str("id"), c.str("name").ifBlank { c.str("id") }, c.str("currentValue"), c.optJSONArray("options").mapObjects { x -> ConfigChoice(x.str("value"), x.str("name").ifBlank { x.str("value") }) })
    }
    val commands = o.optJSONArray("commands").mapObjects { c -> AgentCommand(c.str("name"), c.str("description"), c.optJSONObject("input")?.str("hint").orEmpty()) }
    val usage = o.optJSONObject("usage")
    return SessionInfo(
        threadId = o.str("threadId"), open = o.optBoolean("open"), busy = o.optBoolean("busy"), queued = o.optInt("queued"),
        mode = o.str("mode").ifBlank { "default" }, modes = modes, options = options, commands = commands,
        usedTokens = usage?.optLong("usedTokens") ?: 0, maxTokens = usage?.optLong("maxTokens") ?: 0,
    )
}

internal fun parseProviders(o: JSONObject): ProviderStatus {
    val oauth = o.optJSONObject("oauth") ?: JSONObject()
    return ProviderStatus(
        configured = o.optBoolean("configured"), providerId = o.str("providerId"), baseUrl = o.str("baseUrl"), modelId = o.str("modelId"), hasApiKey = o.optBoolean("hasApiKey"),
        providers = o.optJSONArray("providers").mapObjects { p ->
            val methods = p.optJSONArray("authMethods")?.let { a -> (0 until a.length()).map { a.optString(it) } }.orEmpty()
            Provider(p.str("id"), p.str("name").ifBlank { p.str("id") }, p.str("description"), methods, p.str("baseUrl"), p.str("defaultModel"), oauth.optJSONObject(p.str("id"))?.optBoolean("signedIn") ?: oauth.optBoolean(p.str("id")))
        },
    )
}

internal fun parseRoutine(o: JSONObject): Routine {
    val s = o.optJSONObject("schedule") ?: JSONObject()
    return Routine(o.str("id"), o.str("name"), o.str("prompt"), o.str("botId"), o.optBoolean("enabled", true), s.str("type"), s.str("time"), s.optInt("everyMinutes"), o.optLong("nextRunAt"), o.optLong("lastRunAt"))
}

/** Answers for an agent request, in the host's /respond shape. */
internal object Answers {
    fun permission(optionId: String): JSONObject = JSONObject().put("optionId", optionId)
    fun cancelled(): JSONObject = JSONObject().put("outcome", "cancelled")
    fun planApproved(): JSONObject = JSONObject().put("outcome", "approved")
    fun planRevise(feedback: String): JSONObject = JSONObject().put("outcome", "cancelled").put("feedback", feedback)
    fun planRejected(): JSONObject = JSONObject().put("outcome", "abandoned")
    fun questions(outcome: String, picked: Map<String, List<String>>): JSONObject {
        val answers = JSONObject()
        for ((q, labels) in picked) if (labels.isNotEmpty()) answers.put(q, JSONArray(labels))
        return JSONObject().put("outcome", outcome).put("answers", answers)
    }
}

/** Short, human copy for an ACP turn error the host classified. */
internal fun errorActionLabel(action: String): String? = when (action) {
    "providers" -> "Providers"
    "model" -> "Choose model"
    else -> null
}

internal class BotsClient(
    private val http: OkHttpClient = OkHttpClient.Builder().callTimeout(90, TimeUnit.SECONDS).build(),
    private val stream: OkHttpClient = OkHttpClient.Builder().readTimeout(70, TimeUnit.SECONDS).build(),
) {
    var host: String = ""
    var session: String = ""
    /** Called on 401 to fetch a fresh device session; returns false when that is impossible. */
    var reattach: (() -> Boolean)? = null

    private fun call(method: String, path: String, body: JSONObject? = null, retried: Boolean = false): JSONObject {
        val req = Request.Builder().url(host.trimEnd('/') + path).header("authorization", "Bearer $session").header("accept", "application/json")
        val payload = body?.toString()?.toRequestBody(botsJson)
        when (method) {
            "GET" -> req.get()
            "DELETE" -> if (payload != null) req.delete(payload) else req.delete()
            else -> req.method(method, payload ?: "{}".toRequestBody(botsJson))
        }
        http.newCall(req.build()).execute().use { res ->
            if (res.code == 401 && !retried && reattach?.invoke() == true) return call(method, path, body, true)
            val text = res.body?.string().orEmpty()
            val json = if (text.trimStart().startsWith("{")) JSONObject(text) else JSONObject()
            if (!res.isSuccessful) throw HostError(res.code, json.optString("error").ifBlank { "Workspace request failed (${res.code})" })
            return json
        }
    }

    private fun enc(s: String) = java.net.URLEncoder.encode(s, "UTF-8").replace("+", "%20")

    fun bots(): List<Bot> = call("GET", "/api/bots").optJSONArray("bots").mapObjects(::parseBot)
    fun createBot(name: String, title: String, instructions: String, color: String): Bot =
        createBot(JSONObject().put("name", name).put("title", title).put("instructions", instructions).put("color", color))
    fun createBot(fields: JSONObject): Bot = parseBot(call("POST", "/api/bots", fields).getJSONObject("bot"))
    fun duplicateBot(id: String): Bot = parseBot(call("POST", "/api/bots/${enc(id)}/duplicate").getJSONObject("bot"))
    /** Stores [bytes] (PNG, JPEG, GIF or WebP) as the bot's face. */
    fun uploadAvatar(id: String, bytes: ByteArray, retried: Boolean = false): Bot {
        val req = Request.Builder().url(host.trimEnd('/') + "/api/bots/${enc(id)}/avatar").header("authorization", "Bearer $session")
            .put(bytes.toRequestBody("application/octet-stream".toMediaType())).build()
        http.newCall(req).execute().use { res ->
            if (res.code == 401 && !retried && reattach?.invoke() == true) return uploadAvatar(id, bytes, true)
            val text = res.body?.string().orEmpty()
            val json = if (text.trimStart().startsWith("{")) JSONObject(text) else JSONObject()
            if (!res.isSuccessful) throw HostError(res.code, json.optString("error").ifBlank { "Upload failed (${res.code})" })
            return parseBot(json.getJSONObject("bot"))
        }
    }
    fun deleteAvatar(id: String): Bot = parseBot(call("DELETE", "/api/bots/${enc(id)}/avatar").getJSONObject("bot"))
    /** The stored face image at a host path from [Bot.avatarUrl]. */
    fun avatarBytes(path: String, retried: Boolean = false): ByteArray? {
        if (!path.startsWith("/api/bots/")) return null
        val req = Request.Builder().url(host.trimEnd('/') + path).header("authorization", "Bearer $session").get().build()
        http.newCall(req).execute().use { res ->
            if (res.code == 401 && !retried && reattach?.invoke() == true) return avatarBytes(path, true)
            return if (res.isSuccessful) res.body?.bytes() else null
        }
    }
    fun updateBot(id: String, fields: JSONObject): Bot = parseBot(call("PATCH", "/api/bots/${enc(id)}", fields).getJSONObject("bot"))
    fun deleteBot(id: String) { call("DELETE", "/api/bots/${enc(id)}") }
    fun newThread(botId: String, title: String = ""): String = call("POST", "/api/bots/${enc(botId)}/tasks", JSONObject().put("title", title)).optString("threadId")
    fun renameThread(botId: String, threadId: String, title: String) { call("PATCH", "/api/bots/${enc(botId)}/tasks/${enc(threadId)}", JSONObject().put("title", title)) }
    fun deleteThread(botId: String, threadId: String) { call("DELETE", "/api/bots/${enc(botId)}/tasks/${enc(threadId)}") }

    data class Page(val messages: List<BotMessage>, val hasMore: Boolean, val session: SessionInfo)
    fun messages(threadId: String, before: String? = null, limit: Int = 80): Page {
        val q = buildString { append("?limit=$limit"); if (before != null) append("&before=${enc(before)}") }
        val o = call("GET", "/api/threads/${enc(threadId)}/messages$q")
        return Page(o.optJSONArray("messages").mapObjects(::parseMessage), o.optBoolean("hasMore"), o.optJSONObject("session")?.let(::parseSession) ?: SessionInfo.Empty)
    }
    fun open(threadId: String): SessionInfo = parseSession(call("POST", "/api/threads/${enc(threadId)}/open"))
    fun send(botId: String, threadId: String, text: String): Boolean =
        call("POST", "/api/bots/${enc(botId)}/messages", JSONObject().put("text", text).put("threadId", threadId)).optBoolean("queued")
    fun interrupt(botId: String, threadId: String) { call("POST", "/api/bots/${enc(botId)}/interrupt", JSONObject().put("threadId", threadId)) }
    fun respond(threadId: String, requestId: String, answer: JSONObject) { call("POST", "/api/threads/${enc(threadId)}/respond", JSONObject().put("requestId", requestId).put("answer", answer)) }
    fun setMode(threadId: String, mode: String) { call("POST", "/api/threads/${enc(threadId)}/mode", JSONObject().put("modeId", mode)) }
    fun setOption(threadId: String, id: String, value: String): SessionInfo? =
        call("POST", "/api/threads/${enc(threadId)}/config", JSONObject().put("configId", id).put("value", value)).let { o -> if (o.has("threadId")) parseSession(o) else o.optJSONObject("session")?.let(::parseSession) }

    fun providers(): ProviderStatus = parseProviders(call("GET", "/api/providers"))
    fun models(providerId: String, apiKey: String?, baseUrl: String?): List<ConfigChoice> {
        val body = JSONObject().put("providerId", providerId)
        if (!apiKey.isNullOrBlank()) body.put("apiKey", apiKey)
        if (!baseUrl.isNullOrBlank()) body.put("baseUrl", baseUrl)
        return call("POST", "/api/providers/models", body).optJSONArray("models").mapObjects { m -> ConfigChoice(m.str("id"), m.str("name").ifBlank { m.str("id") }) }
    }
    fun saveProvider(providerId: String, modelId: String, apiKey: String?, baseUrl: String?) {
        val body = JSONObject().put("providerId", providerId)
        if (modelId.isNotBlank()) body.put("modelId", modelId)
        if (!apiKey.isNullOrBlank()) body.put("apiKey", apiKey)
        if (!baseUrl.isNullOrBlank()) body.put("baseUrl", baseUrl)
        call("POST", "/api/providers/save", body)
    }
    fun startLogin(providerId: String, kind: String): LoginStart {
        val o = call("POST", "/api/providers/auth/start", JSONObject().put("providerId", providerId).put("kind", kind))
        return LoginStart(o.optString("loginId"), o.optString("url").ifBlank { o.optString("verificationUriComplete", o.optString("verificationUri")) }, o.optString("userCode"), o.optInt("interval", 3))
    }
    /** pending | done | denied | expired | error | cancelled, plus a message. */
    fun pollLogin(loginId: String): Pair<String, String> = call("POST", "/api/providers/auth/poll", JSONObject().put("loginId", loginId)).let { it.optString("status", "pending") to it.optString("message") }
    fun cancelLogin(loginId: String) { runCatching { call("POST", "/api/providers/auth/cancel", JSONObject().put("loginId", loginId)) } }

    data class LibraryFile(val name: String, val size: Long)
    fun files(botId: String): List<LibraryFile> = call("GET", "/api/bots/${enc(botId)}/files").optJSONArray("files").mapObjects { f -> LibraryFile(f.str("name"), f.optLong("size")) }

    fun routines(): List<Routine> = call("GET", "/api/routines").optJSONArray("routines").mapObjects(::parseRoutine)
    fun createRoutine(name: String, prompt: String, botId: String, type: String, time: String, everyMinutes: Int): Routine {
        val schedule = JSONObject().put("type", type)
        if (type == "daily") schedule.put("time", time)
        if (type == "interval") schedule.put("everyMinutes", everyMinutes)
        return parseRoutine(call("POST", "/api/routines", JSONObject().put("name", name).put("prompt", prompt).put("botId", botId).put("schedule", schedule).put("enabled", true)).getJSONObject("routine"))
    }
    fun setRoutineEnabled(id: String, enabled: Boolean) { call("PATCH", "/api/routines/${enc(id)}", JSONObject().put("enabled", enabled)) }
    fun runRoutine(id: String) { call("POST", "/api/routines/${enc(id)}/run") }
    fun deleteRoutine(id: String) { call("DELETE", "/api/routines/${enc(id)}") }

    /**
     * Reads /api/events until [stop] says so or the stream ends. Resumes from
     * [since] so nothing is missed across reconnects; returns the last cursor.
     */
    fun events(since: String, stop: () -> Boolean, onFrame: (HostFrame) -> Unit): String {
        var cursor = since
        val url = (host.trimEnd('/') + "/api/events").toHttpUrl().newBuilder().addQueryParameter("token", session).apply { if (cursor.isNotBlank()) addQueryParameter("since", cursor) }.build()
        val req = Request.Builder().url(url).header("accept", "text/event-stream").build()
        stream.newCall(req).execute().use { res ->
            if (res.code == 401) throw HostError(401, "Session expired")
            if (!res.isSuccessful) throw HostError(res.code, "Event stream failed (${res.code})")
            val source = res.body?.source() ?: return cursor
            val data = StringBuilder()
            while (!stop()) {
                val line = source.readUtf8Line() ?: break
                when {
                    line.startsWith("id:") -> cursor = line.removePrefix("id:").trim()
                    line.startsWith("data:") -> data.append(line.removePrefix("data:").trimStart())
                    line.isEmpty() && data.isNotEmpty() -> {
                        val json = runCatching { JSONObject(data.toString()) }.getOrNull()
                        data.setLength(0)
                        if (json != null) {
                            if (json.optString("kind") == "hello") json.optString("cursor").takeIf { it.isNotBlank() }?.let { cursor = it }
                            onFrame(HostFrame(json.optString("kind"), json))
                        }
                    }
                }
            }
        }
        return cursor
    }
}

/** A line diff for edit cards: ' ' same, '+' added, '-' removed. */
internal fun lineDiff(old: String, new: String): List<Pair<Char, String>> {
    val a = if (old.isEmpty()) emptyList() else old.split("\n")
    val b = if (new.isEmpty()) emptyList() else new.split("\n")
    if (a.size * b.size > 400_000) return a.map { '-' to it } + b.map { '+' to it }
    val lcs = Array(a.size + 1) { IntArray(b.size + 1) }
    for (i in a.indices.reversed()) for (j in b.indices.reversed()) lcs[i][j] = if (a[i] == b[j]) lcs[i + 1][j + 1] + 1 else maxOf(lcs[i + 1][j], lcs[i][j + 1])
    val out = ArrayList<Pair<Char, String>>()
    var i = 0; var j = 0
    while (i < a.size && j < b.size) when {
        a[i] == b[j] -> { out += ' ' to a[i]; i++; j++ }
        lcs[i + 1][j] >= lcs[i][j + 1] -> { out += '-' to a[i]; i++ }
        else -> { out += '+' to b[j]; j++ }
    }
    while (i < a.size) out += '-' to a[i++]
    while (j < b.size) out += '+' to b[j++]
    return out
}

/** Workspace-relative paths; agent-internal files show their name only. */
internal fun shortPath(p: String): String = when {
    p.startsWith("/home/user/workspace/") -> p.removePrefix("/home/user/workspace/")
    "/.supercharge/" in p -> p.substringAfterLast('/')
    else -> p
}

/** A tool's human summary when its output is a JSON envelope. */
internal fun toolSummary(output: String): String {
    val o = runCatching { JSONObject(output) }.getOrNull() ?: return output.replace("/home/user/workspace/", "")
    for (k in listOf("tool_output_for_prompt_concise", "tool_output_for_prompt", "output", "stdout", "message")) {
        val v = o.optString(k)
        if (v.isNotBlank()) return v.replace("/home/user/workspace/", "")
    }
    return o.toString(2)
}
