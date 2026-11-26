package com.openmausbot.companion.ui

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.ByteArrayOutputStream
import java.util.concurrent.ConcurrentHashMap

/*
 * A bot's face, the same everywhere the PC app shows one: a generated
 * pattern (the host's 15-bit glyph), its initial, an emoji or an uploaded
 * image, on the bot's colour and cropped to its shape.
 */

/** Fetches and keeps decoded face images for one workspace host. */
internal class BotFaces(private val client: BotsClient) {
    private val cache = ConcurrentHashMap<String, ImageBitmap>()
    fun cached(path: String): ImageBitmap? = cache[client.host + path]
    suspend fun load(path: String): ImageBitmap? = cached(path) ?: withContext(Dispatchers.IO) {
        runCatching { client.avatarBytes(path) }.getOrNull()
            ?.let { BitmapFactory.decodeByteArray(it, 0, it.size)?.asImageBitmap() }
            ?.also { cache[client.host + path] = it }
    }
}

internal val LocalBotFaces = staticCompositionLocalOf<BotFaces?> { null }

internal fun faceShape(shape: String): Shape = when (shape) {
    "rounded" -> RoundedCornerShape(28)
    "square" -> RoundedCornerShape(10)
    else -> CircleShape
}

/** Draws [bot]'s face; [preview] shows a picked image that is not uploaded yet. */
@Composable
internal fun BotFace(bot: Bot, size: Dp, busy: Boolean = false, preview: ImageBitmap? = null, modifier: Modifier = Modifier) {
    val shape = faceShape(bot.avatarShape)
    val color = parseHex(bot.color)
    val faces = LocalBotFaces.current
    val path = bot.avatarUrl
    var image by remember(path) { mutableStateOf(path?.let { faces?.cached(it) }) }
    if (bot.avatarKind == "image" && preview == null && path != null && image == null && faces != null) {
        LaunchedEffect(path) { image = faces.load(path) }
    }
    Box(modifier.size(size).semantics { contentDescription = bot.name }.testTag("face-${bot.name}"), contentAlignment = Alignment.Center) {
        val shown = if (bot.avatarKind == "image") preview ?: image else null
        when {
            shown != null -> Image(shown, null, Modifier.size(size).clip(shape), contentScale = ContentScale.Crop)
            bot.avatarKind == "emoji" && bot.emoji.isNotBlank() -> Box(Modifier.size(size).clip(shape).background(lerpColor(color, Vx.raised, 0.78f)), contentAlignment = Alignment.Center) {
                Text(bot.emoji, fontSize = (size.value * 0.52f).sp)
            }
            bot.avatarKind == "initial" || bot.glyph < 0 -> Box(Modifier.size(size).clip(shape).background(color), contentAlignment = Alignment.Center) {
                Text(bot.name.trim().take(1).uppercase().ifBlank { "B" }, color = Color.White, fontWeight = FontWeight.SemiBold, fontSize = (size.value * 0.42f).sp)
            }
            else -> GlyphFace(bot.glyph, color, Modifier.size(size).clip(shape))
        }
        if (busy) Box(Modifier.align(Alignment.BottomEnd).size((size.value * 0.3f).dp).background(Vx.warn, CircleShape).border(2.dp, Vx.panel, CircleShape))
    }
}

private fun lerpColor(a: Color, b: Color, t: Float) = Color(a.red + (b.red - a.red) * t, a.green + (b.green - a.green) * t, a.blue + (b.blue - a.blue) * t, 1f)

/** The 5x5 mirrored grid; bit row*3+col, columns 3 and 4 mirror 1 and 0. */
internal fun glyphCells(bits: Int): List<Pair<Int, Int>> = buildList {
    for (r in 0 until 5) for (c in 0 until 3) {
        if ((bits shr (r * 3 + c)) and 1 == 0) continue
        if (c == 2) add(2 to r) else { add(c to r); add(4 - c to r) }
    }
}

@Composable
private fun GlyphFace(bits: Int, color: Color, modifier: Modifier) {
    Canvas(modifier) {
        val u = this.size.width / 34f
        drawRect(color)
        drawRect(Color.White.copy(alpha = 0.08f))
        for ((x, r) in glyphCells(bits)) {
            drawRoundRect(Color.White.copy(alpha = 0.93f), topLeft = Offset((6 + x * 4.4f) * u, (6 + r * 4.4f) * u), size = Size(4 * u, 4 * u), cornerRadius = CornerRadius(1.1f * u))
        }
    }
}

internal val faceEmojis = listOf("🤖", "🚀", "🧠", "🛠️", "🔍", "📈", "🎨", "✍️", "🧪", "🐛", "📦", "🛰️", "⚡", "🔥", "🌱", "🦊", "🐙", "🦉", "🐝", "🐳", "🎯", "📚", "💡", "🧭")

/** Everything the editor saves; [image] is a picked picture to upload. */
internal data class BotDraft(
    val name: String, val title: String, val description: String, val instructions: String,
    val color: String, val avatarKind: String, val emoji: String, val avatarShape: String,
    val glyphSeed: String?, val pinned: Boolean, val image: ByteArray?, val removeImage: Boolean,
)

/** Large pictures are scaled to 512px; small PNG/JPEG/GIF/WebP files go as they are. */
internal fun prepareFace(raw: ByteArray): ByteArray? {
    val known = raw.size > 12 && (raw[0] == 0x89.toByte() && raw[1] == 'P'.code.toByte() || raw[0] == 0xff.toByte() && raw[1] == 0xd8.toByte() ||
        String(raw, 0, 4, Charsets.ISO_8859_1) == "GIF8" || String(raw, 0, 4, Charsets.ISO_8859_1) == "RIFF" && String(raw, 8, 4, Charsets.ISO_8859_1) == "WEBP")
    if (known && raw.size <= 512 * 1024) return raw
    val bmp = BitmapFactory.decodeByteArray(raw, 0, raw.size) ?: return null
    val scale = minOf(1f, 512f / maxOf(bmp.width, bmp.height))
    val out = Bitmap.createScaledBitmap(bmp, maxOf(1, (bmp.width * scale).toInt()), maxOf(1, (bmp.height * scale).toInt()), true)
    return ByteArrayOutputStream().use { s -> out.compress(Bitmap.CompressFormat.PNG, 100, s); s.toByteArray() }.takeIf { it.size <= 2 * 1024 * 1024 }
}

private fun randomSeed() = java.util.UUID.randomUUID().toString().replace("-", "").take(10)

/** What the bot editor holds while it is open. */
internal class BotEditorState(val existing: Bot?, takenColors: List<String>) {
    var name by mutableStateOf(existing?.name.orEmpty())
    var title by mutableStateOf(existing?.title.orEmpty())
    var description by mutableStateOf(existing?.description.orEmpty())
    var instructions by mutableStateOf(existing?.instructions.orEmpty())
    var color by mutableStateOf(existing?.color?.ifBlank { null } ?: (Vx.swatches.filterNot { it in takenColors }.ifEmpty { Vx.swatches }).random())
    var kind by mutableStateOf(existing?.avatarKind ?: "generated")
    var emoji by mutableStateOf(existing?.emoji.orEmpty())
    var shape by mutableStateOf(existing?.avatarShape ?: "circle")
    var seed by mutableStateOf(if (existing == null) randomSeed() else null)
    var glyph by mutableStateOf(existing?.glyph?.takeIf { it >= 0 } ?: glyphOf(seed ?: ""))
    var pinned by mutableStateOf(existing?.pinned ?: false)
    var image by mutableStateOf<ByteArray?>(null)
    var imagePreview by mutableStateOf<ImageBitmap?>(null)
    var removeImage by mutableStateOf(false)
    var error by mutableStateOf("")
    var confirmDelete by mutableStateOf(false)
    val hasImage get() = image != null || (!removeImage && existing?.avatarUrl != null)

    fun shuffle() { seed = randomSeed(); glyph = glyphOf(seed!!) }
    fun preview(): Bot = (existing ?: Bot("", "", "", "", color, BotSettings(), "", false, "", 0, emptyList())).copy(
        name = name.ifBlank { "?" }, color = color, avatarKind = if (kind == "image" && !hasImage) "generated" else kind, emoji = emoji, avatarShape = shape, glyph = glyph,
        avatarUrl = if (removeImage) null else existing?.avatarUrl,
    )
    /** The draft to save, or null (with [error] set) when it can't be saved yet. */
    fun draft(): BotDraft? {
        if (name.isBlank()) { error = "Give the bot a name."; return null }
        if (kind == "image" && !hasImage) { error = "Choose an image, or pick another icon."; return null }
        return BotDraft(name.trim(), title.trim(), description.trim(), instructions.trim(), color, kind, emoji, shape, seed, pinned, image, removeImage)
    }
}

/** Create or edit a bot: name, role, description, instructions and its face. */
@Composable
internal fun BotEditorDialog(existing: Bot?, takenColors: List<String>, onDismiss: () -> Unit, onSave: (BotDraft) -> Unit, onDelete: () -> Unit, onDuplicate: () -> Unit) {
    val st = remember { BotEditorState(existing, takenColors) }
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Vx.raised,
        title = { Text(if (existing == null) "New bot" else "Edit ${existing.name}", fontSize = 17.sp, fontWeight = FontWeight.SemiBold) },
        text = { Column(Modifier.verticalScroll(rememberScrollState())) { BotEditorForm(st, onDelete, onDuplicate) } },
        confirmButton = { PrimaryButton(if (existing == null) "Create bot" else "Save", enabled = st.name.isNotBlank(), modifier = Modifier.testTag("bot-save")) { st.draft()?.let(onSave) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel", color = Vx.text2) } },
    )
}

@Composable
internal fun BotEditorForm(st: BotEditorState, onDelete: () -> Unit, onDuplicate: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val existing = st.existing
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri: Uri? ->
        if (uri == null) { if (!st.hasImage) st.kind = "generated"; return@rememberLauncherForActivityResult }
        scope.launch {
            val bytes = withContext(Dispatchers.IO) { runCatching { context.contentResolver.openInputStream(uri)?.use { it.readBytes() }?.let(::prepareFace) }.getOrNull() }
            if (bytes == null) { st.error = "Choose a PNG, JPEG, GIF or WebP image under 2 MB."; if (!st.hasImage) st.kind = "generated"; return@launch }
            st.image = bytes; st.removeImage = false; st.kind = "image"; st.error = ""
            st.imagePreview = BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap()
        }
    }
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            BotFace(st.preview(), 64.dp, preview = st.imagePreview, modifier = Modifier.clickable(enabled = st.kind == "generated") { st.shuffle() })
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                if (existing?.main == true) Text("Main bot", color = Vx.accentHi, fontSize = 12.sp, fontWeight = FontWeight.SemiBold)
                Text(if (st.kind == "generated") "Tap the face for a new pattern" else "Pick how this bot looks", color = Vx.text3, fontSize = 12.sp)
            }
        }
        OutlinedTextField(st.name, { st.name = it.take(80) }, label = { Text("Name") }, singleLine = true, modifier = Modifier.fillMaxWidth().testTag("bot-name"))
        OutlinedTextField(st.title, { st.title = it.take(120) }, label = { Text("Role") }, placeholder = { Text("e.g. Code reviewer") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        Text("Icon", color = Vx.text2, fontSize = 12.sp)
        Choice(listOf("generated" to "Pattern", "initial" to "Letter", "emoji" to "Emoji", "image" to "Image"), st.kind, "kind") { k ->
            st.kind = k
            if (k == "emoji" && st.emoji.isBlank()) st.emoji = faceEmojis[0]
            if (k == "image" && !st.hasImage) pick.launch("image/*")
        }
        if (st.kind == "emoji") {
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                faceEmojis.forEach { e ->
                    Box(Modifier.size(36.dp).clip(RoundedCornerShape(8.dp)).background(if (e == st.emoji) Vx.selected else Color.Transparent).border(1.dp, if (e == st.emoji) Vx.accent else Color.Transparent, RoundedCornerShape(8.dp)).clickable { st.emoji = e }.testTag("emoji-$e"), contentAlignment = Alignment.Center) { Text(e, fontSize = 19.sp) }
                }
            }
        }
        if (st.kind == "image") Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            QuietButton(if (st.hasImage) "Replace image" else "Choose image") { pick.launch("image/*") }
            if (st.hasImage) QuietButton("Remove", Icons.Default.Delete) { st.image = null; st.imagePreview = null; st.removeImage = true; st.kind = "generated" }
        }
        Text("Color", color = Vx.text2, fontSize = 12.sp)
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Vx.swatches.forEach { s ->
                Box(Modifier.size(26.dp).clip(CircleShape).background(parseHex(s)).border(2.dp, if (s.equals(st.color, true)) Vx.text else Color.Transparent, CircleShape).clickable { st.color = s }.testTag("swatch-$s"))
            }
        }
        Text("Shape", color = Vx.text2, fontSize = 12.sp)
        Choice(listOf("circle" to "Circle", "rounded" to "Rounded", "square" to "Square"), st.shape, "shape") { st.shape = it }
        OutlinedTextField(st.description, { st.description = it.take(500) }, label = { Text("Description") }, minLines = 2, maxLines = 4, modifier = Modifier.fillMaxWidth().testTag("bot-description"))
        OutlinedTextField(st.instructions, { st.instructions = it }, label = { Text("Instructions") }, minLines = 3, maxLines = 8, modifier = Modifier.fillMaxWidth())
        if (existing != null && !existing.main) Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.clickable { st.pinned = !st.pinned }) {
            Checkbox(st.pinned, { st.pinned = it }, colors = CheckboxDefaults.colors(checkedColor = Vx.accent))
            Text("Pin to the top of the list", color = Vx.text2, fontSize = 13.sp)
        }
        if (st.error.isNotBlank()) Text(st.error, color = Vx.bad, fontSize = 12.sp)
        if (existing != null) Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            QuietButton("Duplicate", onClick = onDuplicate)
            if (!existing.main) {
                if (st.confirmDelete) {
                    Text("Delete ${existing.name}?", color = Vx.bad, fontSize = 13.sp, modifier = Modifier.weight(1f))
                    QuietButton("Delete", danger = true, onClick = onDelete)
                } else QuietButton("Delete bot", Icons.Default.Delete, danger = true) { st.confirmDelete = true }
            }
        }
    }
}

@Composable
private fun Choice(options: List<Pair<String, String>>, value: String, tag: String, onPick: (String) -> Unit) {
    Row(Modifier.clip(RoundedCornerShape(9.dp)).background(Vx.bg).border(Vx.border, RoundedCornerShape(9.dp)).padding(3.dp), horizontalArrangement = Arrangement.spacedBy(2.dp)) {
        options.forEach { (id, label) ->
            Text(label, color = if (id == value) Vx.text else Vx.text2, fontSize = 13.sp,
                modifier = Modifier.clip(RoundedCornerShape(6.dp)).background(if (id == value) Vx.raised2 else Color.Transparent).clickable { onPick(id) }.padding(horizontal = 10.dp, vertical = 6.dp).testTag("$tag-$id"))
        }
    }
}
