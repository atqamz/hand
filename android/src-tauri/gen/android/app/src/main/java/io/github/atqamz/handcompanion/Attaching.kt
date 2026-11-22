package io.github.atqamz.handcompanion

import android.content.ContentResolver
import android.net.Uri
import android.provider.OpenableColumns
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream

const val MAX_UPLOAD_BYTES = 10L shl 20
const val MAX_ATTACHED = 10

class Picked(val name: String, val size: Long, val read: () -> ByteArray)

typealias Pick = () -> Picked

fun attachmentTokenText(n: Int) = "[attachment $n]"

data class Field(val text: String, val selection: TextRange)

fun insertToken(field: Field, n: Int): Field {
  val start = field.selection.min.coerceIn(0, field.text.length)
  val end = field.selection.max.coerceIn(start, field.text.length)
  val token = attachmentTokenText(n)
  return Field(field.text.replaceRange(start, end, token), TextRange(start + token.length))
}

private fun withoutToken(text: String, n: Int, count: Int) =
  attachmentToken.replace(text) { match ->
    val at = match.groupValues[1].toInt()
    when {
      at == n -> ""
      at > n && at <= count -> attachmentTokenText(at - 1)
      else -> match.value
    }
  }

fun dropToken(field: Field, n: Int, count: Int): Field {
  val text = field.text
  val start = field.selection.min.coerceIn(0, text.length)
  val end = field.selection.max.coerceIn(start, text.length)
  val head = withoutToken(text.substring(0, start), n, count).length
  val tail = withoutToken(text.substring(0, end), n, count).length
  return Field(withoutToken(text, n, count), TextRange(head, tail))
}

fun composerValue(current: TextFieldValue, draft: String, selection: TextRange) =
  if (current.text != draft) TextFieldValue(draft, selection) else current

fun readLimited(input: InputStream, limit: Long): ByteArray {
  val out = ByteArrayOutputStream()
  val chunk = ByteArray(16 * 1024)
  var left = limit + 1
  while (left > 0) {
    val n = input.read(chunk, 0, minOf(chunk.size.toLong(), left).toInt())
    if (n < 0) break
    out.write(chunk, 0, n)
    left -= n
  }
  return out.toByteArray()
}

fun picked(resolver: ContentResolver, uri: Uri): Picked {
  var name = "file"
  var size = -1L
  resolver
    .query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)
    ?.use { cursor ->
      if (cursor.moveToFirst()) {
        cursor.getString(0)?.let { name = it }
        if (!cursor.isNull(1)) size = cursor.getLong(1)
      }
    }
  return Picked(name, size) {
    resolver.openInputStream(uri)?.use { readLimited(it, MAX_UPLOAD_BYTES) }
      ?: throw IOException("no stream")
  }
}
