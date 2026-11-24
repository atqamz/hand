package io.github.atqamz.handcompanion

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable

const val DOC_LINES = 500

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

data class DocState(
  val node: DocNode? = null,
  val blocks: List<MdBlock> = emptyList(),
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

private fun docLines(window: DocWindow): List<String> {
  val cuts = window.cut.associate { it.line to it.bytes }
  return window.lines.mapIndexed { index, line ->
    val left = cuts[window.from + index]
    if (left == null) docLine(line) else docLine(line) + " … $left more bytes"
  }
}

class DocModel(
  fleet: String,
  private val path: String,
  transport: Transport,
  io: CoroutineDispatcher = Dispatchers.IO
) : ViewModel() {
  private val mutableState = MutableStateFlow(DocState())
  val state: StateFlow<DocState> = mutableState

  private val client = BoardClient(fleet, transport, io)
  private val generation = AtomicInteger()

  fun retry() {
    viewModelScope.launch { load(mutableState.value.node?.page ?: 1) }
  }

  fun page(page: Int) {
    viewModelScope.launch { load(page) }
  }

  fun more() {
    viewModelScope.launch { loadMore() }
  }

  suspend fun load(page: Int = 1) {
    val mine = generation.incrementAndGet()
    mutableState.update {
      if (it.node == null) {
        it.copy(loading = true, problem = null, refused = null, tooOld = false, paging = false)
      } else {
        it.copy(loading = true, paging = false)
      }
    }
    try {
      val node = client.doc(path, page).data
      val shown =
        when {
          node.kind == "folder" -> DocState(node = node)
          node.kind == "markdown" ->
            DocState(node = node, blocks = markdown(client.docRaw(path)))
          node.text && node.kind in TEXT_KINDS -> {
            val window = client.docWindow(path, 1).data
            DocState(node = node, lines = docLines(window), next = window.next)
          }
          else -> DocState(node = node)
        }
      mutableState.update { if (mine == generation.get()) shown.copy(loading = false) else it }
    } catch (error: BoardError) {
      mutableState.update { if (mine == generation.get()) failed(it, error) else it }
    }
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
        else it.copy(lines = it.lines + docLines(window), next = window.next, paging = false)
      }
    } catch (error: BoardError) {
      mutableState.update {
        if (mine != generation.get()) it
        else if (error == BoardError.Unpaired) it.copy(paging = false, unpaired = true)
        else it.copy(paging = false, moreProblem = historyProblem(error))
      }
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
