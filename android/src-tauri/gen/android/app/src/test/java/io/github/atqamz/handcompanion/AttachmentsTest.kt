package io.github.atqamz.handcompanion

import org.junit.Assert.assertEquals
import org.junit.Test

private fun shot(n: Int, image: Boolean = true) =
  Shot(n = n, name = "20261121-101500-$n-a.png", label = "a$n.png", image = image)

class AttachmentsTest {
  private val one = shot(1)
  private val two = shot(2)

  @Test
  fun aPictureStandsWhereItsTokenStands() {
    assertEquals(
      listOf(
        Part.Words("look at"),
        Part.Piece(one),
        Part.Words("as you see, it should be like"),
        Part.Piece(two)
      ),
      inlineParts("look at [attachment 1] as you see, it should be like [attachment 2]", listOf(one, two))
    )
  }

  @Test
  fun aShotNoTokenNamesComesAfterTheText() {
    assertEquals(
      listOf(Part.Words("see [attachment 3]"), Part.Piece(one), Part.Piece(two)),
      inlineParts("see [attachment 3]", listOf(one, two))
    )
    assertEquals(
      listOf(Part.Words("see"), Part.Piece(two), Part.Piece(one)),
      inlineParts("see [attachment 2]", listOf(one, two))
    )
  }

  @Test
  fun aTokenWithoutAShotStaysText() {
    assertEquals(
      listOf(Part.Words("a [attachment 4] b"), Part.Piece(one)),
      inlineParts("a [attachment 4] b", listOf(one))
    )
    assertEquals(
      listOf(Part.Words("a"), Part.Piece(one), Part.Words("b [attachment 4] c")),
      inlineParts("a [attachment 1] b [attachment 4] c", listOf(one))
    )
  }

  @Test
  fun theSameNumberMayStandSeveralTimes() {
    assertEquals(
      listOf(Part.Piece(one), Part.Words("and"), Part.Piece(one)),
      inlineParts("[attachment 1] and [attachment 1]", listOf(one))
    )
  }

  @Test
  fun onlyTheExactTokenCounts() {
    for (text in listOf("[attachment 0]", "[attachment 01]", "[attachment  1]", "[Attachment 1]", "[attachment 1", "[attachment 12345]")) {
      assertEquals(listOf(Part.Words(text), Part.Piece(one)), inlineParts(text, listOf(one)))
    }
  }

  @Test
  fun textWithoutShotsIsOneRunAndAnEmptyOneIsNone() {
    assertEquals(listOf(Part.Words(" hi \n")), inlineParts(" hi \n", emptyList()))
    assertEquals(emptyList<Part>(), inlineParts("", emptyList()))
    assertEquals(listOf(Part.Piece(one)), inlineParts("", listOf(one)))
    assertEquals(listOf(Part.Piece(one)), inlineParts("[attachment 1]", listOf(one)))
  }

  @Test
  fun aFileIsPlacedLikeAPicture() {
    val file = shot(1, image = false)
    assertEquals(
      listOf(Part.Words("the log"), Part.Piece(file)),
      inlineParts("the log [attachment 1]", listOf(file))
    )
  }

  @Test
  fun theDecodeKeepsAtMostTheBoundInPixels() {
    assertEquals(1, sampleSize(1000, 1000, 2_000_000))
    assertEquals(1, sampleSize(2000, 1000, 2_000_000))
    assertEquals(2, sampleSize(2001, 1000, 2_000_000))
    assertEquals(2, sampleSize(4000, 2000, 2_000_000))
    assertEquals(4, sampleSize(5000, 5000, 2_000_000))
    assertEquals(8, sampleSize(6000, 6000, 2_000_000))
    assertEquals(8, sampleSize(6001, 6001, 1_000_000))
    for ((w, h) in listOf(7001 to 5003, 1 to 30000, 36_000_000 / 6 to 6)) {
      val size = sampleSize(w, h, 2_000_000)
      assertEquals(true, ((w + size - 1) / size).toLong() * ((h + size - 1) / size) <= 2_000_000)
    }
  }

  @Test
  fun anEntryTakesTheBoardsShotsAndIgnoresWhatItDoesNotUse() {
    val entry =
      boardJson.decodeFromString<TimelineEntry>(
        """{"role":"operator","at":"2026-11-21T10:15:00Z","text":"look [attachment 1]","shots":[
          {"n":1,"name":"20261121-101500-1-a.png","label":"a.png","kind":"image","size":"12 KB","href":"/doc/x","image":true},
          {"n":2,"name":"20261121-101500-2-b.log","label":"b.log","kind":"log","missing":true}]}"""
      )
    assertEquals(
      listOf(
        Shot(1, "20261121-101500-1-a.png", "a.png", "image", "12 KB", image = true),
        Shot(2, "20261121-101500-2-b.log", "b.log", "log", missing = true)
      ),
      entry.shots
    )
    assertEquals(
      emptyList<Shot>(),
      boardJson.decodeFromString<TimelineEntry>("""{"role":"operator","text":"x"}""").shots
    )
  }
}
