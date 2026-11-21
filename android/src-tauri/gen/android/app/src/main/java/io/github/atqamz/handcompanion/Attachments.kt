package io.github.atqamz.handcompanion

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.LruCache
import java.net.URLEncoder
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable

@Serializable
data class Shot(
  val n: Int,
  val name: String,
  val label: String = "",
  val kind: String = "",
  val size: String = "",
  val image: Boolean = false,
  val missing: Boolean = false
)

sealed interface Part {
  data class Words(val text: String) : Part

  data class Piece(val shot: Shot) : Part
}

private val token = Regex("""\[attachment ([1-9][0-9]{0,3})]""")

fun inlineParts(text: String, shots: List<Shot>): List<Part> {
  val parts = mutableListOf<Part>()
  val placed = HashSet<Int>()
  var last = 0
  for (match in token.findAll(text)) {
    val shot = shots.firstOrNull { it.n == match.groupValues[1].toInt() } ?: continue
    text.substring(last, match.range.first).trim().takeIf(String::isNotEmpty)?.let {
      parts += Part.Words(it)
    }
    parts += Part.Piece(shot)
    placed += shot.n
    last = match.range.last + 1
  }
  if (last == 0) {
    if (text.isNotEmpty()) parts += Part.Words(text)
  } else {
    text.substring(last).trim().takeIf(String::isNotEmpty)?.let { parts += Part.Words(it) }
  }
  shots.filter { it.n !in placed }.forEach { parts += Part.Piece(it) }
  return parts
}

const val MAX_PIXELS = 2_000_000
const val MAX_SOURCE_PIXELS = 100_000_000L
private const val CACHE_BYTES = 16 * 1024 * 1024
private const val CONCURRENT_FETCHES = 2

fun sampleSize(width: Int, height: Int, maxPixels: Int = MAX_PIXELS): Int {
  var size = 1
  while (((width + size - 1) / size).toLong() * ((height + size - 1) / size) > maxPixels) size *= 2
  return size
}

fun decodeBounded(bytes: ByteArray): Bitmap? {
  val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
  BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
  val width = bounds.outWidth
  val height = bounds.outHeight
  if (width <= 0 || height <= 0 || width.toLong() * height > MAX_SOURCE_PIXELS) return null
  val options = BitmapFactory.Options().apply { inSampleSize = sampleSize(width, height) }
  return BitmapFactory.decodeByteArray(bytes, 0, bytes.size, options)
}

fun interface FileSource {
  fun file(fleet: String, path: String): ByteArray?
}

object CoreFiles : FileSource {
  override fun file(fleet: String, path: String): ByteArray? = Core.file(fleet, path)
}

class AttachmentLoader(
  private val source: FileSource,
  private val io: CoroutineDispatcher = Dispatchers.IO,
  private val decode: (ByteArray) -> Bitmap? = ::decodeBounded
) {
  private val cache =
    object : LruCache<String, Bitmap>(CACHE_BYTES) {
      override fun sizeOf(key: String, value: Bitmap) = value.byteCount
    }
  private val gate = Semaphore(CONCURRENT_FETCHES)

  fun cached(fleet: String, name: String): Bitmap? = cache.get("$fleet/$name")

  suspend fun image(fleet: String, name: String): Bitmap? {
    val key = "$fleet/$name"
    cache.get(key)?.let { return it }
    return gate.withPermit {
      cache.get(key)
        ?: withContext(io) {
          val path = "/api/v1/inbox/" + URLEncoder.encode(name, "UTF-8").replace("+", "%20")
          source.file(fleet, path)?.let(decode)
        }?.also { cache.put(key, it) }
    }
  }
}

internal val appAttachments by lazy { AttachmentLoader(CoreFiles) }
