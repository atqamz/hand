package io.github.atqamz.handcompanion

import android.graphics.Bitmap
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import java.io.IOException
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable

const val DOC_LINES = 500
const val IMAGE_LIMIT = 8L * 1024 * 1024
const val FIND_WINDOWS = 8

@Serializable
data class DocEntry(
  val name: String,
  val path: String,
  val kind: String = "file",
  val label: String = "",
  val dir: Boolean = false,
  val size: Long? = null,
  val refused: String? = null
)

@Serializable
data class DocNode(
  val path: String,
  val name: String,
  val kind: String,
  val text: Boolean = false,
  val size: Long = 0,
  val entries: List<DocEntry> = emptyList(),
  val page: Int = 1,
  val pages: Int = 1,
  val total: Int = 0
)

@Serializable data class DocCut(val line: Int, val bytes: Int)

@Serializable
data class DocWindow(
  val from: Int = 1,
  val lines: List<String> = emptyList(),
  val cut: List<DocCut> = emptyList(),
  val next: Int? = null
)

data class DocFind(
  val query: String,
  val hits: List<Int> = emptyList(),
  val at: Int = -1,
  val problem: String? = null
)

data class DocJump(val line: Int, val id: Int)

data class DocNotice(val title: String, val detail: String)

data class AudioView(
  val playing: Boolean = false,
  val position: Int = 0,
  val duration: Int = 0,
  val error: String? = null
)

data class DocState(
  val node: DocNode? = null,
  val blocks: List<MdBlock> = emptyList(),
  val source: String? = null,
  val image: ByteArray? = null,
  val audio: AudioView? = null,
  val pdfPages: Int = 0,
  val notice: DocNotice? = null,
  val wrap: Boolean = true,
  val first: Int = 1,
  val find: DocFind? = null,
  val jump: DocJump? = null,
  val goLine: Int? = null,
  val goProblem: String? = null,
  val copyProblem: String? = null,
  val lines: List<String> = emptyList(),
  val next: Int? = null,
  val paging: Boolean = false,
  val loading: Boolean = true,
  val problem: String? = null,
  val moreProblem: String? = null,
  val refused: String? = null,
  val tooOld: Boolean = false,
  val unpaired: Boolean = false
)

enum class DocBody {
  Loading,
  Unpaired,
  TooOld,
  Refused,
  Failed,
  Folder,
  Markdown,
  Text,
  Image,
  ImageTooLarge,
  Audio,
  Pdf,
  MediaNotice,
  Notice
}

private val TEXT_KINDS = setOf("text", "csv", "tsv")

fun docBody(state: DocState): DocBody {
  val node = state.node
  return when {
    state.unpaired -> DocBody.Unpaired
    state.refused != null -> DocBody.Refused
    state.tooOld -> DocBody.TooOld
    node == null -> if (state.problem != null) DocBody.Failed else DocBody.Loading
    node.kind == "folder" -> DocBody.Folder
    node.kind == "markdown" -> DocBody.Markdown
    node.kind == "image" ->
      when {
        state.image != null -> DocBody.Image
        node.size > IMAGE_LIMIT -> DocBody.ImageTooLarge
        else -> DocBody.Failed
      }
    state.notice != null -> DocBody.MediaNotice
    node.kind == "audio" -> if (state.audio != null) DocBody.Audio else DocBody.Failed
    node.kind == "pdf" -> if (state.pdfPages > 0) DocBody.Pdf else DocBody.Failed
    node.text && node.kind in TEXT_KINDS -> DocBody.Text
    else -> DocBody.Notice
  }
}

fun docSize(bytes: Long): String =
  when {
    bytes < 1024 -> "$bytes B"
    bytes < 1024 * 1024 -> "${bytes / 1024} KB"
    else -> "%.1f MB".format(java.util.Locale.ROOT, bytes / 1048576.0)
  }

private val BIDI = 0x202A..0x202E

fun docLine(line: String): String =
  buildString(line.length) {
    for (c in line) {
      when {
        c == '\t' -> append("    ")
        c.code < 0x20 -> append((0x2400 + c.code).toChar())
        c.code == 0x7f -> append('␡')
        c.code in BIDI || c.code in 0x2066..0x2069 -> append('�')
        else -> append(c)
      }
    }
  }

fun docMatches(line: String, query: String): List<IntRange> {
  if (query.isEmpty()) return emptyList()
  val found = ArrayList<IntRange>()
  var at = line.indexOf(query, 0, ignoreCase = true)
  while (at >= 0) {
    found += at until at + query.length
    at = line.indexOf(query, at + query.length, ignoreCase = true)
  }
  return found
}

fun docHits(first: Int, lines: List<String>, query: String): List<Int> =
  if (query.isEmpty()) emptyList()
  else lines.indices.filter { lines[it].contains(query, ignoreCase = true) }.map { first + it }

private fun DocFind.over(first: Int, lines: List<String>): DocFind {
  val now = docHits(first, lines, query)
  val kept = hits.getOrNull(at)?.let(now::indexOf)?.takeIf { it >= 0 }
  return copy(hits = now, at = kept ?: if (now.isEmpty()) -1 else 0)
}

private fun DocState.focus(at: Int): DocState {
  val find = find ?: return this
  return copy(find = find.copy(at = at), jump = DocJump(find.hits[at], (jump?.id ?: 0) + 1))
}

private fun docLines(window: DocWindow): List<String> {
  val cuts = window.cut.associate { it.line to it.bytes }
  return window.lines.mapIndexed { index, line ->
    val left = cuts[window.from + index]
    if (left == null) docLine(line) else docLine(line) + " … $left more bytes"
  }
}

class DocModel(
  private val fleetId: String,
  private val path: String,
  transport: Transport,
  io: CoroutineDispatcher = Dispatchers.IO,
  private val files: FileSource = CoreFiles,
  private val audioOpener: AudioOpener = PlatformAudio,
  private val pdfOpener: PdfOpener = PdfOpener { throw IOException("No PDF renderer.") }
) : ViewModel() {
  private val mutableState = MutableStateFlow(DocState())
  val state: StateFlow<DocState> = mutableState

  private val client = BoardClient(fleetId, transport, io)
  private val generation = AtomicInteger()
  private val fetch = io

  fun retry() {
    viewModelScope.launch { load(mutableState.value.node?.page ?: 1) }
  }

  fun page(page: Int) {
    viewModelScope.launch { load(page) }
  }

  fun more() {
    viewModelScope.launch { loadMore() }
  }

  fun find(query: String) {
    viewModelScope.launch { search(query) }
  }

  fun step(forward: Boolean) {
    viewModelScope.launch { move(forward) }
  }

  fun goTo(line: Int) {
    viewModelScope.launch { jumpTo(line) }
  }

  fun earlier() {
    viewModelScope.launch { loadEarlier() }
  }

  fun toggleWrap() = mutableState.update { it.copy(wrap = !it.wrap) }

  fun closeFind() {
    generation.incrementAndGet()
    mutableState.update { it.copy(find = null, paging = false) }
  }

  suspend fun load(page: Int = 1) {
    val mine = generation.incrementAndGet()
    releaseMedia()
    mutableState.update {
      it.copy(loading = true, problem = null, refused = null, tooOld = false, unpaired = false, paging = false)
    }
    try {
      val node = client.doc(path, page).data
      val shown =
        when {
          node.kind == "folder" -> DocState(node = node)
          node.kind == "markdown" -> {
            val source = client.docRaw(path)
            DocState(node = node, blocks = markdown(source), source = source)
          }
          node.text && node.kind in TEXT_KINDS -> {
            val window = client.docWindow(path, 1).data
            DocState(node = node, lines = docLines(window), next = window.next)
          }
          node.kind == "image" && node.size <= IMAGE_LIMIT -> image(node)
          node.kind == "audio" -> audio(node, mine)
          node.kind == "pdf" -> pdf(node, mine)
          else -> DocState(node = node)
        }
      mutableState.update {
        if (mine == generation.get()) shown.copy(loading = false, wrap = it.wrap) else it
      }
    } catch (error: BoardError) {
      mutableState.update { if (mine == generation.get()) failed(it, error) else it }
    }
  }

  private suspend fun image(node: DocNode): DocState {
    val bytes = withContext(fetch) { files.file(fleetId, "/api/v1/doc/${docRoute(path)}?raw") }
    return if (bytes == null || bytes.isEmpty()) {
      DocState(node = node, problem = "The image could not be loaded.")
    } else {
      DocState(node = node, image = bytes)
    }
  }

  private suspend fun mediaBytes(node: DocNode): ByteArray? {
    val bytes = withContext(fetch) { files.file(fleetId, "/api/v1/doc/${docRoute(path)}?raw") }
    return bytes?.takeIf { it.isNotEmpty() }
  }

  private fun tooLarge(node: DocNode, what: String) =
    DocState(
      node = node,
      notice =
        DocNotice(
          "This $what is too large to show here.",
          "${node.kind} · ${docSize(node.size)}. The limit is ${docSize(MEDIA_LIMIT)}."
        )
    )

  private suspend fun audio(node: DocNode, mine: Int): DocState {
    if (node.size > MEDIA_LIMIT) return tooLarge(node, "audio file")
    val bytes = mediaBytes(node) ?: return DocState(node = node, problem = "The audio could not be loaded.")
    if (bytes.size > MEDIA_LIMIT) return tooLarge(node.copy(size = bytes.size.toLong()), "audio file")
    val opened =
      try {
        withContext(fetch) { audioOpener.open(bytes, events) }
      } catch (_: IOException) {
        return DocState(
          node = node,
          notice = DocNotice("This audio can't be played here.", "${node.kind} · ${docSize(node.size)}")
        )
      }
    synchronized(media) {
      if (mine != generation.get()) {
        opened.release()
        return DocState(node = node)
      }
      player = opened
      audioBytes = bytes
    }
    return DocState(node = node, audio = AudioView(duration = opened.duration))
  }

  private suspend fun pdf(node: DocNode, mine: Int): DocState {
    if (node.size > MEDIA_LIMIT) return tooLarge(node, "PDF")
    val bytes = mediaBytes(node) ?: return DocState(node = node, problem = "The PDF could not be loaded.")
    if (bytes.size > MEDIA_LIMIT) return tooLarge(node.copy(size = bytes.size.toLong()), "PDF")
    val opened =
      try {
        withContext(fetch) { pdfOpener.open(bytes) }
      } catch (_: PdfRefused) {
        return DocState(
          node = node,
          notice = DocNotice("This PDF is protected, so it can't be shown here.", "It needs a password.")
        )
      } catch (_: IOException) {
        return DocState(node = node, problem = "The PDF could not be opened.")
      }
    if (opened.count < 1) {
      opened.close()
      return DocState(node = node, problem = "The PDF has no pages.")
    }
    synchronized(media) {
      if (mine != generation.get()) {
        opened.close()
        return DocState(node = node)
      }
      pdf = opened
    }
    return DocState(node = node, pdfPages = opened.count)
  }

  private val media = Any()
  private var player: AudioPlayer? = null
  private var audioBytes: ByteArray? = null
  private var pdf: PdfPages? = null

  private val events =
    object : AudioEvents {
      override fun ended() {
        mutableState.update { it.copy(audio = it.audio?.copy(playing = false, position = it.audio.duration)) }
      }

      override fun failed() {
        releasePlayer()
        mutableState.update {
          it.copy(audio = it.audio?.copy(playing = false, error = "The audio stopped with an error."))
        }
      }
    }

  private fun releasePlayer() {
    val held = synchronized(media) { player.also { player = null } }
    held?.release()
  }

  private fun releaseMedia() {
    releasePlayer()
    val held =
      synchronized(media) {
        audioBytes = null
        pdf.also { pdf = null }
      }
    held?.close()
  }

  fun release() {
    val held = synchronized(media) { player }
    val at = held?.position
    releasePlayer()
    mutableState.update { state ->
      state.copy(audio = state.audio?.copy(playing = false, position = at ?: state.audio.position))
    }
  }

  public override fun onCleared() {
    generation.incrementAndGet()
    releaseMedia()
  }

  fun play() {
    viewModelScope.launch { start() }
  }

  suspend fun start() {
    val view = mutableState.value.audio ?: return
    var current = synchronized(media) { player }
    if (current == null) {
      val bytes = synchronized(media) { audioBytes } ?: return
      val mine = generation.get()
      current =
        try {
          withContext(fetch) { audioOpener.open(bytes, events) }
        } catch (_: IOException) {
          mutableState.update {
            if (mine == generation.get()) it.copy(audio = it.audio?.copy(playing = false, error = "The audio could not be played.")) else it
          }
          return
        }
      synchronized(media) {
        if (mine != generation.get() || audioBytes == null) {
          current.release()
          return
        }
        player = current
      }
    }
    val from = if (view.duration > 0 && view.position >= view.duration) 0 else view.position
    current.seekTo(from)
    current.start()
    mutableState.update { it.copy(audio = it.audio?.copy(playing = true, position = from, error = null)) }
  }

  fun pause() {
    val held = synchronized(media) { player } ?: return
    held.pause()
    val at = held.position
    mutableState.update { it.copy(audio = it.audio?.copy(playing = false, position = at)) }
  }

  fun seek(millis: Int) {
    val held = synchronized(media) { player }
    held?.seekTo(millis)
    mutableState.update { it.copy(audio = it.audio?.copy(position = millis)) }
  }

  fun tick() {
    val held = synchronized(media) { player } ?: return
    val at = held.position
    mutableState.update { state ->
      if (state.audio?.playing == true) state.copy(audio = state.audio.copy(position = at)) else state
    }
  }

  suspend fun render(index: Int, width: Int): Bitmap? {
    val held = synchronized(media) { pdf } ?: return null
    return withContext(fetch) { held.render(index, width) }
  }

  suspend fun loadMore() {
    val mine = generation.get()
    val held = mutableState.value
    val from = held.next ?: return
    if (held.paging) return
    mutableState.update { if (mine == generation.get()) it.copy(paging = true, moreProblem = null) else it }
    try {
      val window = client.docWindow(path, from).data
      mutableState.update {
        if (mine != generation.get()) it
        else {
          val lines = it.lines + docLines(window)
          it.copy(lines = lines, next = window.next, paging = false, find = it.find?.over(it.first, lines))
        }
      }
    } catch (error: BoardError) {
      mutableState.update {
        if (mine != generation.get()) it
        else if (error == BoardError.Unpaired) it.copy(paging = false, unpaired = true)
        else it.copy(paging = false, moreProblem = historyProblem(error))
      }
    }
  }

  suspend fun search(query: String) {
    val mine = generation.incrementAndGet()
    val held = mutableState.value
    val hits = docHits(held.first, held.lines, query)
    mutableState.update {
      val searched = it.copy(paging = false, find = DocFind(query, hits, if (hits.isEmpty()) -1 else 0))
      if (hits.isEmpty()) searched else searched.focus(0)
    }
    if (hits.isEmpty() && query.isNotEmpty()) scan(mine, 0)
  }

  suspend fun move(forward: Boolean) {
    val held = mutableState.value
    val find = held.find ?: return
    val count = find.hits.size
    when {
      count == 0 && held.next == null -> return
      forward && find.at + 1 < count -> mutableState.update { it.focus(find.at + 1) }
      forward && held.next != null -> scan(generation.incrementAndGet(), count)
      forward -> mutableState.update { it.focus(0) }
      count > 0 -> mutableState.update { it.focus(if (find.at <= 0) count - 1 else find.at - 1) }
    }
  }

  private suspend fun scan(mine: Int, past: Int) {
    repeat(FIND_WINDOWS) {
      val from = mutableState.value.next ?: return
      if (mine != generation.get()) return
      mutableState.update {
        if (mine == generation.get()) it.copy(paging = true, find = it.find?.copy(problem = null)) else it
      }
      try {
        val window = client.docWindow(path, from).data
        mutableState.update {
          if (mine != generation.get()) it
          else {
            val lines = it.lines + docLines(window)
            val find = it.find?.over(it.first, lines)
            val grown = find != null && find.hits.size > past
            val next = it.copy(lines = lines, next = window.next, paging = false, find = find)
            if (grown) next.focus(past) else next
          }
        }
      } catch (error: BoardError) {
        mutableState.update {
          if (mine != generation.get()) it
          else if (error == BoardError.Unpaired) it.copy(paging = false, unpaired = true)
          else it.copy(paging = false, find = it.find?.copy(problem = historyProblem(error)))
        }
        return
      }
      if (mine != generation.get() || (mutableState.value.find?.hits?.size ?: 0) > past) return
    }
  }

  suspend fun jumpTo(line: Int) {
    val held = mutableState.value
    if (line < 1) {
      mutableState.update { it.copy(goLine = line, goProblem = "Line numbers start at 1.") }
      return
    }
    if (line >= held.first && line < held.first + held.lines.size) {
      mutableState.update {
        it.copy(goLine = line, goProblem = null, jump = DocJump(line, (it.jump?.id ?: 0) + 1))
      }
      return
    }
    val mine = generation.incrementAndGet()
    mutableState.update { it.copy(goLine = line, goProblem = null, paging = true) }
    try {
      val window = client.docWindow(path, line).data
      mutableState.update {
        when {
          mine != generation.get() -> it
          else -> {
            val lines = docLines(window)
            it.copy(
              lines = lines,
              first = line,
              next = window.next,
              paging = false,
              moreProblem = null,
              find = it.find?.over(line, lines),
              jump = DocJump(line, (it.jump?.id ?: 0) + 1)
            )
          }
        }
      }
    } catch (error: BoardError) {
      mutableState.update {
        if (mine != generation.get()) it
        else if (error == BoardError.Unpaired) it.copy(paging = false, unpaired = true)
        else it.copy(paging = false, goProblem = historyProblem(error))
      }
    }
  }

  suspend fun loadEarlier() {
    val mine = generation.get()
    val held = mutableState.value
    if (held.first <= 1 || held.paging) return
    val from = maxOf(1, held.first - DOC_LINES)
    mutableState.update { if (mine == generation.get()) it.copy(paging = true, moreProblem = null) else it }
    try {
      val target = held.first
      var cursor = from
      val got = mutableListOf<String>()
      while (cursor < target) {
        val window = client.docWindow(path, cursor, target - cursor).data
        val part = docLines(window)
        val next = window.next
        if (part.isEmpty() || next == null || next <= cursor || next > target) {
          mutableState.update {
            if (mine == generation.get()) it.copy(paging = false, moreProblem = "Earlier lines could not be read.")
            else it
          }
          return
        }
        got += part
        cursor = next
      }
      mutableState.update {
        if (mine != generation.get()) it
        else {
          val lines = got + it.lines
          it.copy(lines = lines, first = from, paging = false, find = it.find?.over(from, lines))
        }
      }
    } catch (error: BoardError) {
      mutableState.update {
        if (mine != generation.get()) it
        else if (error == BoardError.Unpaired) it.copy(paging = false, unpaired = true)
        else it.copy(paging = false, moreProblem = historyProblem(error))
      }
    }
  }

  suspend fun contents(): String? {
    val held = mutableState.value
    held.source?.let { return it }
    return try {
      client.docRaw(path).also { mutableState.update { state -> state.copy(copyProblem = null) } }
    } catch (error: BoardError) {
      val problem =
        if (error == BoardError.TooLarge) "The file is too large to copy here." else historyProblem(error)
      mutableState.update { it.copy(copyProblem = problem) }
      null
    }
  }

  private fun failed(held: DocState, error: BoardError): DocState {
    val base = held.copy(loading = false)
    return when {
      error == BoardError.Unpaired -> base.copy(unpaired = true)
      error is BoardError.Other && error.code == null && error.status in listOf(404, 405) ->
        base.copy(tooOld = true)
      error is BoardError.Forbidden ||
        error is BoardError.NotFound ||
        error is BoardError.Conflict ||
        error is BoardError.BadRequest ->
        base.copy(refused = error.message?.takeIf(String::isNotBlank) ?: "The Board does not show $path.")
      else -> base.copy(problem = historyProblem(error))
    }
  }
}
