package io.github.atqamz.handcompanion

import android.media.AudioAttributes
import android.media.MediaDataSource
import android.media.MediaPlayer
import android.view.SurfaceHolder
import android.view.SurfaceView
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import java.io.Closeable
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL

const val STREAM_BLOCK = 256 * 1024
private const val CONNECT_MILLIS = 10_000
private const val READ_MILLIS = 30_000

class Unplayable : IOException()

interface VideoPlayer : AudioPlayer {
  val width: Int
  val height: Int

  fun display(holder: SurfaceHolder?)
}

fun interface VideoOpener {
  @Throws(IOException::class)
  fun open(fleet: String, route: String, size: Long, events: AudioEvents): VideoPlayer
}

class ProxyLink(val base: String, val cookie: String)

fun proxyLink(answer: String?, fleet: String): ProxyLink? {
  val json = parse(answer)
  val port = json.field("port")?.toIntOrNull()?.takeIf { it in 1..65535 } ?: return null
  val cookie = json.field("cookie") ?: return null
  if (!Regex("hand_phone_${Regex.escape(fleet)}=[1-9][0-9]*\\.[0-9a-f]{64}").matches(cookie)) return null
  return ProxyLink("http://localhost:$port", cookie)
}

class RangedReader(
  base: String,
  fleet: String,
  route: String,
  private val cookie: String,
  val size: Long,
  private val connect: (URL) -> HttpURLConnection = { it.openConnection() as HttpURLConnection }
) : Closeable {
  private val url = URL("$base/$fleet/api/v1/doc/$route?raw")
  private var from = -1L
  private var block = ByteArray(0)
  private var closed = false

  var broke: IOException? = null
    private set

  init {
    if (url.protocol != "http" || url.host != "localhost" || url.port !in 1..65535 || url.userInfo != null) {
      throw IOException("The video is not reachable here.")
    }
  }

  @Synchronized
  fun read(position: Long, buffer: ByteArray, offset: Int, count: Int): Int {
    if (position < 0 || position >= size) return -1
    try {
      if (closed) throw IOException("The video was closed.")
      if (position < from || position >= from + block.size) {
        val start = position / STREAM_BLOCK * STREAM_BLOCK
        block = fetch(start, minOf(start + STREAM_BLOCK, size) - 1)
        from = start
      }
    } catch (error: IOException) {
      broke = error
      throw error
    }
    return readSlice(block, position - from, buffer, offset, count)
  }

  private fun fetch(start: Long, end: Long): ByteArray {
    val link = connect(url)
    try {
      link.instanceFollowRedirects = false
      link.connectTimeout = CONNECT_MILLIS
      link.readTimeout = READ_MILLIS
      link.setRequestProperty("Cookie", cookie)
      link.setRequestProperty("Range", "bytes=$start-$end")
      link.setRequestProperty("Accept-Encoding", "identity")
      link.setRequestProperty("Connection", "close")
      val length = (end - start + 1).toInt()
      if (link.responseCode != 206 || link.getHeaderField("Content-Range")?.startsWith("bytes $start-$end/") != true) {
        throw IOException("The Board did not answer the range.")
      }
      val bytes = ByteArray(length)
      link.inputStream.use { stream ->
        var read = 0
        while (read < length) {
          val got = stream.read(bytes, read, length - read)
          if (got < 0) throw IOException("The range ended early.")
          read += got
        }
      }
      return bytes
    } finally {
      link.disconnect()
    }
  }

  @Synchronized
  override fun close() {
    closed = true
    block = ByteArray(0)
    from = -1
  }
}

private class StreamSource(private val reader: RangedReader) : MediaDataSource() {
  override fun readAt(position: Long, buffer: ByteArray, offset: Int, size: Int) =
    reader.read(position, buffer, offset, size)

  override fun getSize() = reader.size

  override fun close() = reader.close()
}

object PlatformVideo : VideoOpener {
  override fun open(fleet: String, route: String, size: Long, events: AudioEvents): VideoPlayer {
    val link = proxyLink(Core.fallback(fleet), fleet) ?: throw IOException("The video could not be reached.")
    val reader = RangedReader(link.base, fleet, route, link.cookie, size)
    val player = MediaPlayer()
    try {
      player.setAudioAttributes(
        AudioAttributes.Builder()
          .setUsage(AudioAttributes.USAGE_MEDIA)
          .setContentType(AudioAttributes.CONTENT_TYPE_MOVIE)
          .build()
      )
      player.setDataSource(StreamSource(reader))
      player.setOnCompletionListener { events.ended() }
      player.setOnErrorListener { _, _, _ ->
        events.failed()
        true
      }
      player.prepare()
    } catch (error: Exception) {
      player.release()
      reader.close()
      throw reader.broke ?: Unplayable()
    }
    return object : VideoPlayer {
      override val duration = player.duration.coerceAtLeast(0)
      override val width = player.videoWidth
      override val height = player.videoHeight
      override val position
        get() = player.currentPosition

      override fun start() = player.start()

      override fun pause() = player.pause()

      override fun seekTo(millis: Int) = player.seekTo(millis)

      override fun display(holder: SurfaceHolder?) =
        try {
          player.setDisplay(holder)
        } catch (_: IllegalStateException) {}

      override fun release() {
        player.release()
        reader.close()
      }
    }
  }
}

@Composable
fun VideoSurface(playing: Boolean, onSurface: (SurfaceHolder?) -> Unit, modifier: Modifier) {
  AndroidView(
    {
      SurfaceView(it).apply {
        holder.addCallback(
          object : SurfaceHolder.Callback {
            override fun surfaceCreated(holder: SurfaceHolder) = onSurface(holder)

            override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {}

            override fun surfaceDestroyed(holder: SurfaceHolder) = onSurface(null)
          }
        )
      }
    },
    modifier,
    update = { it.keepScreenOn = playing }
  )
}
