package io.github.atqamz.handcompanion

import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import java.io.ByteArrayInputStream
import org.junit.Assert.assertEquals
import org.junit.Test

class AttachingTest {
  @Test
  fun theLimitsAreTheBoardsTenMibAndTenFilesToAMessage() {
    assertEquals(10L * 1024 * 1024, MAX_UPLOAD_BYTES)
    assertEquals(10, MAX_ATTACHED)
  }

  @Test
  fun aTokenIsInsertedAtTheCursorAndTheCursorLandsAfterIt() {
    val field = insertToken(Field("look at  here", TextRange(8)), 1)
    assertEquals("look at [attachment 1] here", field.text)
    assertEquals(TextRange(8 + "[attachment 1]".length), field.selection)
  }

  @Test
  fun aTokenReplacesTheSelectedText() {
    val field = insertToken(Field("see THIS now", TextRange(4, 8)), 2)
    assertEquals("see [attachment 2] now", field.text)
    assertEquals(TextRange(4 + "[attachment 2]".length), field.selection)
  }

  @Test
  fun aTokenGoesAtTheStartAtTheEndAndIntoAnEmptyBox() {
    assertEquals("[attachment 1]ab", insertToken(Field("ab", TextRange(0)), 1).text)
    assertEquals("ab[attachment 3]", insertToken(Field("ab", TextRange(2)), 3).text)
    assertEquals("[attachment 1]", insertToken(Field("", TextRange.Zero), 1).text)
  }

  @Test
  fun aReversedOrOutOfRangeSelectionStillInsertsInsideTheText() {
    assertEquals("a[attachment 1]d", insertToken(Field("abcd", TextRange(3, 1)), 1).text)
    assertEquals("ab[attachment 1]", insertToken(Field("ab", TextRange(50)), 1).text)
  }

  @Test
  fun removingAFileDropsItsTokensAndRenumbersTheLaterOnes() {
    val field =
      dropToken(
        Field("a [attachment 1] b [attachment 2] c [attachment 3] d [attachment 2]", TextRange(0)),
        2,
        3
      )
    assertEquals("a [attachment 1] b  c [attachment 2] d ", field.text)
  }

  @Test
  fun aTokenBeyondTheFilesHeldIsPlainTextAndStaysAsTyped() {
    val field = dropToken(Field("[attachment 1] [attachment 5] [attachment 3]", TextRange(0)), 1, 3)
    assertEquals(" [attachment 5] [attachment 2]", field.text)
  }

  @Test
  fun removingTheLastFileKeepsTheOthersAsTheyAre() {
    val field = dropToken(Field("[attachment 1][attachment 2]", TextRange(0)), 2, 2)
    assertEquals("[attachment 1]", field.text)
  }

  @Test
  fun theCursorFollowsItsTextWhenATokenBeforeItIsDropped() {
    val text = "x [attachment 1] yz"
    val at = text.indexOf('z')
    val field = dropToken(Field(text, TextRange(at)), 1, 1)
    assertEquals("x  yz", field.text)
    assertEquals(field.text.indexOf('z'), field.selection.start)
    assertEquals(field.text.indexOf('z'), field.selection.end)
  }

  @Test
  fun aSelectionKeepsBothEndsWhenATokenInsideItIsDropped() {
    val text = "a [attachment 1] b [attachment 2] c"
    val field = dropToken(Field(text, TextRange(0, text.length)), 1, 2)
    assertEquals("a  b [attachment 1] c", field.text)
    assertEquals(TextRange(0, field.text.length), field.selection)
  }

  @Test
  fun aDraftTheBoxDoesNotHoldIsPutInWithTheModelsCaret() {
    val value = composerValue(TextFieldValue("old", TextRange(1)), "old [attachment 1]", TextRange(4, 7))
    assertEquals("old [attachment 1]", value.text)
    assertEquals(TextRange(4, 7), value.selection)
  }

  @Test
  fun aBoxThatAlreadyHoldsTheDraftKeepsItsOwnCaret() {
    val held = TextFieldValue("same", TextRange(2))
    assertEquals(held, composerValue(held, "same", TextRange(4)))
  }

  @Test
  fun readingStopsOneByteAfterTheLimit() {
    val data = ByteArray(1000) { it.toByte() }
    assertEquals(11, readLimited(ByteArrayInputStream(data), 10).size)
    assertEquals(1000, readLimited(ByteArrayInputStream(data), 1000).size)
    assertEquals(1000, readLimited(ByteArrayInputStream(data), 5000).size)
    assertEquals(0, readLimited(ByteArrayInputStream(ByteArray(0)), 10).size)
  }
}
