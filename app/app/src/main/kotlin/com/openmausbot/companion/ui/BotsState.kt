package com.openmausbot.companion.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.SnapshotStateList
import org.json.JSONObject

/**
 * Everything the phone shows about one workspace's bots, kept current by the
 * host's event stream. Frames are applied on the main thread.
 */
internal class BotsState(val client: BotsClient) {
    var bots by mutableStateOf(listOf<Bot>())
    var botId by mutableStateOf("")
    var threadId by mutableStateOf("")
    var live by mutableStateOf(false)
    var loading by mutableStateOf(true)
    var error by mutableStateOf("")
    val messages = mutableStateMapOf<String, SnapshotStateList<BotMessage>>()
    val hasMore = mutableStateMapOf<String, Boolean>()
    val sessions = mutableStateMapOf<String, SessionInfo>()
    var cursor = ""

    val bot: Bot? get() = bots.firstOrNull { it.id == botId }
    val session: SessionInfo get() = sessions[threadId] ?: SessionInfo.Empty
    fun thread(): List<BotMessage> = messages[threadId] ?: emptyList()

    fun replaceBots(list: List<Bot>) {
        bots = list
        if (bots.none { it.id == botId }) botId = (bots.firstOrNull { it.main } ?: bots.firstOrNull())?.id.orEmpty()
    }

    fun select(bot: Bot, thread: String? = null) {
        botId = bot.id
        threadId = thread ?: bot.threadId.ifBlank { bot.threads.firstOrNull()?.id.orEmpty() }
    }

    fun mergeBot(b: Bot) {
        val i = bots.indexOfFirst { it.id == b.id }
        bots = if (i >= 0) bots.toMutableList().also { it[i] = b } else bots + b
    }

    fun loaded(threadId: String, page: BotsClient.Page, older: Boolean = false) {
        val list = messages.getOrPut(threadId) { mutableStateListOf() }
        if (older) list.addAll(0, page.messages.filter { m -> list.none { it.id == m.id } })
        else { list.clear(); list.addAll(page.messages) }
        hasMore[threadId] = page.hasMore
        if (!older && page.session.threadId.isNotBlank()) sessions[threadId] = page.session
    }

    fun upsert(threadId: String, m: BotMessage) {
        val list = messages[threadId] ?: return
        val i = list.indexOfFirst { it.id == m.id }
        if (i >= 0) list[i] = m else list.add(m)
    }

    /** Applies one frame; returns true when the client should reload (a fresh stream). */
    fun apply(f: HostFrame): Boolean {
        val j = f.json
        when (f.kind) {
            "hello" -> { live = true; return !j.optBoolean("resumed") }
            "message" -> j.optJSONObject("message")?.let { o ->
                val tid = j.optString("threadId").ifBlank { o.optString("threadId") }
                val m = parseMessage(o)
                upsert(tid, m)
                touchPreview(j.optString("botId"), tid, m)
            }
            "session" -> j.optJSONObject("session")?.let { sessions[j.optString("threadId")] = parseSession(it) }
            "turn" -> {
                val tid = j.optString("threadId"); val busy = j.optBoolean("busy")
                sessions[tid] = (sessions[tid] ?: SessionInfo.Empty.copy(threadId = tid)).copy(busy = busy)
                markBusy(j.optString("botId"), tid, busy)
            }
            "bot" -> j.optJSONObject("bot")?.let { mergeBot(parseBot(it)) }
            "bot.deleted" -> { bots = bots.filterNot { it.id == j.optString("botId") }; if (botId == j.optString("botId")) botId = "" }
            "thread" -> j.optJSONObject("thread")?.let { t -> updateThread(j.optString("botId"), t) }
        }
        return false
    }

    private fun touchPreview(botId: String, tid: String, m: BotMessage) {
        if (m.kind != "text" || m.text.isBlank()) return
        bots = bots.map { b ->
            if (b.id != botId) b else b.copy(preview = m.text.take(120), lastAt = m.at, threads = b.threads.map { t -> if (t.id == tid) t.copy(preview = m.text.take(120), updatedAt = m.at) else t })
        }
    }

    private fun markBusy(botId: String, tid: String, busy: Boolean) {
        bots = bots.map { b ->
            if (b.id != botId) b else {
                val threads = b.threads.map { t -> if (t.id == tid) t.copy(busy = busy) else t }
                b.copy(threads = threads, busy = threads.any { it.busy })
            }
        }
    }

    private fun updateThread(botId: String, t: JSONObject) {
        val id = t.optString("threadId")
        bots = bots.map { b ->
            if (b.id != botId) b else {
                val existing = b.threads.firstOrNull { it.id == id }
                val next = BotThread(id, t.optString("title").ifBlank { "New thread" }, existing?.busy ?: false, existing?.preview.orEmpty(), t.optLong("updatedAt"), t.optBoolean("pinned"))
                if (t.optLong("archivedAt") > 0) b.copy(threads = b.threads.filterNot { it.id == id })
                else if (existing != null) b.copy(threads = b.threads.map { if (it.id == id) next else it })
                else b.copy(threads = listOf(next) + b.threads)
            }
        }
    }
}
