package com.openmausbot.companion.ui

import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors

/**
 * The last page of each thread, stored on the phone so a thread paints before
 * the sandbox answers. The live page replaces it.
 */
internal class ChatCache(private val root: File) {
    private val io = Executors.newSingleThreadExecutor()
    private val latest = java.util.concurrent.ConcurrentHashMap<String, Pair<List<BotMessage>, Boolean>>()
    @Volatile private var due = false

    fun read(threadId: String): BotsClient.Page? {
        val file = file(threadId)
        if (!file.isFile) return null
        return runCatching {
            val o = JSONObject(file.readText())
            val arr = o.optJSONArray("messages")
            val messages = if (arr == null) emptyList() else (0 until arr.length()).mapNotNull { i -> arr.optJSONObject(i)?.let(::parseMessage) }
            if (messages.isEmpty()) null else BotsClient.Page(messages, o.optBoolean("hasMore"), SessionInfo.Empty)
        }.getOrNull()
    }

    fun save(threadId: String, messages: List<BotMessage>, hasMore: Boolean) {
        if (threadId.isBlank() || messages.isEmpty()) return
        latest[threadId] = messages.takeLast(80) to hasMore
        synchronized(this) { if (due) return; due = true }
        io.execute {
            Thread.sleep(350)
            val snap = HashMap(latest)
            latest.clear()
            synchronized(this) { due = false }
            snap.forEach { (id, pair) -> write(id, pair.first, pair.second) }
        }
    }

    private fun write(threadId: String, messages: List<BotMessage>, hasMore: Boolean) {
        runCatching {
            root.mkdirs()
            val arr = JSONArray()
            messages.forEach { arr.put(messageJson(it)) }
            file(threadId).writeText(JSONObject().put("hasMore", hasMore).put("messages", arr).toString())
            root.listFiles()?.sortedByDescending { it.lastModified() }?.drop(12)?.forEach { it.delete() }
        }
    }

    fun forget(threadId: String) {
        io.execute { runCatching { file(threadId).delete() } }
    }

    private fun file(threadId: String) = File(root, threadId.replace(Regex("[^A-Za-z0-9_-]"), "_") + ".json")
}

private fun clip(s: String, n: Int) = if (s.length <= n) s else s.take(n)

private fun messageJson(m: BotMessage): JSONObject {
    val o = JSONObject()
        .put("id", m.id).put("threadId", m.threadId).put("role", m.role).put("kind", m.kind).put("at", m.at)
        .put("text", clip(m.text, 8000)).put("error", m.error).put("errorCode", m.errorCode).put("action", m.action)
    m.tool?.let { t ->
        o.put("tool", JSONObject()
            .put("id", t.id).put("title", t.title).put("kind", t.kind).put("status", t.status)
            .put("input", clip(t.input, 500)).put("output", clip(t.output, 500))
            .put("diffs", JSONArray().also { arr -> t.diffs.forEach { d -> arr.put(JSONObject().put("path", d.path)) } })
            .put("images", JSONArray().also { arr -> t.images.forEach { img -> arr.put(JSONObject().put("id", img.id).put("mime", img.mime)) } }))
    }
    m.request?.let { r ->
        o.put("request", JSONObject()
            .put("id", r.id).put("type", r.type).put("state", r.state).put("title", r.title)
            .put("plan", clip(r.plan, 4000)).put("mode", r.mode).put("answer", r.answer)
            .put("options", JSONArray().also { arr -> r.options.forEach { p -> arr.put(JSONObject().put("optionId", p.id).put("name", p.name).put("kind", p.kind)) } })
            .put("questions", JSONArray().also { arr ->
                r.questions.forEach { q ->
                    arr.put(JSONObject().put("question", q.question).put("header", q.header).put("multiSelect", q.multiSelect)
                        .put("options", JSONArray().also { opts -> q.options.forEach { c -> opts.put(JSONObject().put("label", c.label).put("description", c.description)) } }))
                }
            }))
    }
    if (m.entries.isNotEmpty()) o.put("entries", JSONArray().also { arr -> m.entries.forEach { e -> arr.put(JSONObject().put("content", e.content).put("status", e.status)) } })
    if (m.changes.isNotEmpty()) o.put("changes", JSONArray().also { arr -> m.changes.forEach { c -> arr.put(JSONObject().put("path", c.path).put("kind", c.kind)) } })
    return o
}
