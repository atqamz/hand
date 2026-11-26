package io.github.atqamz.handcompanion

import android.graphics.Bitmap
import android.graphics.Color
import android.graphics.pdf.PdfRenderer
import android.media.AudioAttributes
import android.media.MediaDataSource
import android.media.MediaPlayer
import android.os.ParcelFileDescriptor
import java.io.File
import java.io.IOException

const val MEDIA_LIMIT = 15L * 1024 * 1024
private const val PAGE_HEIGHT_LIMIT = 8192L

interface AudioPlayer {
  val duration: Int
  val position: Int

  fun start()

  fun pause()

  fun seekTo(millis: Int)

  fun release()
}

interface AudioEvents {
  fun ended()

  fun failed()
}

fun interface AudioOpener {
  @Throws(IOException::class) fun open(bytes: ByteArray, events: AudioEvents): AudioPlayer
}

interface PdfPages {
  val count: Int

  fun render(index: Int, width: Int): Bitmap?

  fun close()
}

class PdfRefused : Exception()

fun interface PdfOpener {
  @Throws(PdfRefused::class, IOException::class) fun open(bytes: ByteArray): PdfPages
}

fun readSlice(bytes: ByteArray, position: Long, buffer: ByteArray, offset: Int, size: Int): Int {
  if (position < 0 || position >= bytes.size) return -1
  val count = minOf(size.toLong(), bytes.size - position).toInt()
  System.arraycopy(bytes, position.toInt(), buffer, offset, count)
  return count
}

private class BytesSource(private val bytes: ByteArray) : MediaDataSource() {
  override fun readAt(position: Long, buffer: ByteArray, offset: Int, size: Int) =
    readSlice(bytes, position, buffer, offset, size)

  override fun getSize() = bytes.size.toLong()

  override fun close() {}
}

object PlatformAudio : AudioOpener {
  override fun open(bytes: ByteArray, events: AudioEvents): AudioPlayer {
    val player = MediaPlayer()
    try {
      player.setAudioAttributes(
        AudioAttributes.Builder()
          .setUsage(AudioAttributes.USAGE_MEDIA)
          .setContentType(AudioAttributes.CONTENT_TYPE_MUSIC)
          .build()
      )
      player.setDataSource(BytesSource(bytes))
      player.setOnCompletionListener { events.ended() }
      player.setOnErrorListener { _, _, _ ->
        events.failed()
        true
      }
      player.prepare()
    } catch (error: Exception) {
      player.release()
      throw IOException("The audio could not be opened.", error)
    }
    return object : AudioPlayer {
      override val duration = player.duration.coerceAtLeast(0)
      override val position
        get() = player.currentPosition

      override fun start() = player.start()

      override fun pause() = player.pause()

      override fun seekTo(millis: Int) = player.seekTo(millis)

      override fun release() = player.release()
    }
  }
}

const val TEMP_PREFIX = "doc-"
const val TEMP_SUFFIX = ".pdf"

class TempCopy private constructor(val file: File) {
  fun close() {
    file.delete()
  }

  companion object {
    fun make(dir: File, bytes: ByteArray): TempCopy {
      dir.mkdirs()
      val file = File.createTempFile(TEMP_PREFIX, TEMP_SUFFIX, dir)
      file.setReadable(false, false)
      file.setReadable(true, true)
      file.setWritable(false, false)
      file.setWritable(true, true)
      try {
        file.writeBytes(bytes)
      } catch (error: IOException) {
        file.delete()
        throw error
      }
      return TempCopy(file)
    }

    fun sweep(dir: File) {
      dir.listFiles { file -> file.name.startsWith(TEMP_PREFIX) && file.name.endsWith(TEMP_SUFFIX) }
        ?.forEach(File::delete)
    }
  }
}

class PlatformPdf(private val dir: File) : PdfOpener {
  private val swept by lazy { TempCopy.sweep(dir) }

  override fun open(bytes: ByteArray): PdfPages {
    swept
    val copy = TempCopy.make(dir, bytes)
    var fd: ParcelFileDescriptor? = null
    try {
      fd = ParcelFileDescriptor.open(copy.file, ParcelFileDescriptor.MODE_READ_ONLY)
      val renderer = PdfRenderer(fd)
      return Pages(renderer, fd, copy)
    } catch (error: SecurityException) {
      fd?.close()
      copy.close()
      throw PdfRefused()
    } catch (error: Exception) {
      fd?.close()
      copy.close()
      throw IOException("The PDF could not be opened.", error)
    }
  }

  private class Pages(
    private val renderer: PdfRenderer,
    private val fd: ParcelFileDescriptor,
    private val copy: TempCopy
  ) : PdfPages {
    private var closed = false
    override val count = renderer.pageCount

    override fun render(index: Int, width: Int): Bitmap? =
      synchronized(this) {
        if (closed || index !in 0 until count || width < 1) return null
        try {
          renderer.openPage(index).use { page ->
            val height = (width.toLong() * page.height / maxOf(1, page.width)).coerceIn(1, PAGE_HEIGHT_LIMIT)
            Bitmap.createBitmap(width, height.toInt(), Bitmap.Config.ARGB_8888).also {
              it.eraseColor(Color.WHITE)
              page.render(it, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY)
            }
          }
        } catch (_: Exception) {
          null
        }
      }

    override fun close() =
      synchronized(this) {
        if (!closed) {
          closed = true
          renderer.close()
          fd.close()
          copy.close()
        }
      }
  }
}
