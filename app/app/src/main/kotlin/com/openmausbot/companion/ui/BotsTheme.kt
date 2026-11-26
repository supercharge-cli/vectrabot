package com.openmausbot.companion.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/** One neutral near-black palette and one green accent, shared with the PC app. */
internal object Vx {
    val bg = Color(0xFF0A0A0B)
    val panel = Color(0xFF0F0F11)
    val raised = Color(0xFF17171A)
    val raised2 = Color(0xFF1D1D21)
    val selected = Color(0xFF212125)
    val line = Color(0xFF222226)
    val line2 = Color(0xFF2C2C31)
    val text = Color(0xFFEDEDEF)
    val text2 = Color(0xFFA0A0A8)
    val text3 = Color(0xFF6C6C74)
    val accent = Color(0xFF1F9F5C)
    val accentHi = Color(0xFF24B469)
    val accentSoft = Color(0x1F24B469)
    val ok = Color(0xFF33C57A)
    val warn = Color(0xFFE2A640)
    val bad = Color(0xFFEF6461)
    val badSoft = Color(0x1AEF6461)
    val add = Color(0x1F33C57A)
    val del = Color(0x1FEF6461)
    val border = BorderStroke(1.dp, line)
    val card = RoundedCornerShape(12.dp)
    val small = RoundedCornerShape(8.dp)
    val swatches = listOf("#22a06b", "#3b82f6", "#ef6461", "#f08c3a", "#8b5cf6", "#06b6d4", "#ec4899", "#d4a017", "#14b8a6", "#f97362")
}

internal val vectraColors = darkColorScheme(
    primary = Vx.accent,
    onPrimary = Color.White,
    primaryContainer = Vx.accent,
    onPrimaryContainer = Color.White,
    secondary = Vx.text2,
    background = Vx.bg,
    onBackground = Vx.text,
    surface = Vx.panel,
    onSurface = Vx.text,
    surfaceVariant = Vx.raised,
    onSurfaceVariant = Vx.text2,
    surfaceContainer = Vx.raised,
    surfaceContainerHigh = Vx.raised2,
    surfaceContainerHighest = Vx.raised2,
    surfaceContainerLow = Vx.panel,
    outline = Vx.line2,
    outlineVariant = Vx.line,
    error = Vx.bad,
    errorContainer = Color(0xFF2A1414),
    onErrorContainer = Vx.bad,
)

@Composable
internal fun VectraTheme(content: @Composable () -> Unit) = MaterialTheme(colorScheme = vectraColors, content = content)

internal fun parseHex(hex: String, fallback: Color = Color(0xFF3B82F6)): Color =
    runCatching { Color(android.graphics.Color.parseColor(if (hex.startsWith("#")) hex else "#$hex")) }.getOrDefault(fallback)

internal fun timeAgo(ms: Long, now: Long = System.currentTimeMillis()): String {
    if (ms <= 0) return ""
    val s = (now - ms) / 1000
    return when {
        s < 60 -> "now"
        s < 3600 -> "${s / 60}m"
        s < 86_400 -> "${s / 3600}h"
        s < 7 * 86_400 -> "${s / 86_400}d"
        else -> java.text.SimpleDateFormat("MMM d", java.util.Locale.getDefault()).format(java.util.Date(ms))
    }
}

internal fun clockTime(ms: Long): String = if (ms <= 0) "" else java.text.SimpleDateFormat("h:mm a", java.util.Locale.getDefault()).format(java.util.Date(ms))
