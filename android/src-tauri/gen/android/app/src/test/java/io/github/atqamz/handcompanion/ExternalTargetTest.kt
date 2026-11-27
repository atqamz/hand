package io.github.atqamz.handcompanion

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ExternalTargetTest {
  private val board =
    listOf(
      "http://localhost",
      "http://localhost:50123/f0123456789ab/task/t3",
      "http://LOCALHOST:8080/",
      "HTTP://Localhost/",
      "http://localhost./",
      "http://localhost%2e/",
      "http://a.localhost/",
      "http://x@localhost/",
      "http://localhost:80:90/",
      "http://127.0.0.1/",
      "http://127.0.0.2/",
      "http://127.255.255.255/",
      "http://127.1/x",
      "http://2130706433/",
      "http://0x7f000001/",
      "http://0177.0.0.1/",
      "http://0x7f.1/",
      "http://127%2e0.0.1/",
      "http://0/",
      "http://0.0.0.0:80/",
      "http://0.1.2.3/",
      "http://169.254.169.254/",
      "http://0xa9fe0001/",
      "http://example.test@127.0.0.1/",
      "https://127.0.0.1\\@example.test/",
      "http://1.2.3.4.5/",
      "http://4294967296/",
      "http://256.1.1.1/",
      "http://0x/",
      "http://089/",
      "http://[::1]/",
      "http://[::1]:8080/f0123456789ab/",
      "http://[0:0:0:0:0:0:0:1]/",
      "http://[::]/",
      "http://[::ffff:127.0.0.1]/",
      "http://[::ffff:7f00:1]/",
      "http://[fe80::1]/",
      "http://[fe80::1%25eth0]/",
      "http://[::1/",
      "http://[::1]x/",
      "http://[]/",
      "http://[example]/",
      "http://[::1::2]/",
      "http://[2001:db8::1::2]/",
      "http://[::ffff:1.2.3.]/",
      "http://[::ffff:8.8.8.0008]/",
      "http://[zz:1::1]/",
      "http://[12345:1::1]/",
      "http://[::2:0]/",
      "http://[1:2:3:4:5:6:7]/",
      "http://[1:2:3:4:5:6:7:8:9]/",
      "http://[1:2:3:4:5:6:7::8]/",
      "http://[12345::1]/",
      "http://[::ffff:1.2.3]/",
      "http://[::ffff:256.1.1.1]/",
      "http://[::ffff:127.0.0.1.5]/",
      "http://[::127.0.0.1]/",
      "http://[::ffff:0.0.0.0]/",
      "http://[::ffff:169.254.1.1]/",
      "http://[febf::1]/",
      "http://[zz::1]/",
      "http://[localhost::1]/",
      "https://example.test:abc/",
      "https://example.test:80a/",
      "http:///x",
      "http://:80/",
      "http:foo",
      "https:",
      "http://exa mple.test/",
      "http://loc\talhost/",
      "file:///etc/passwd",
      "content://media/1",
      "intent://x#Intent;end",
      "javascript:alert(1)",
      "handboard://open/f0123456789ab/task/t3",
      "/f0123456789ab/task/t3",
      "not a url",
      ""
    )

  private val outside =
    listOf(
      "https://example.test/a",
      "http://example.test:8080/x?y#z",
      "HTTPS://Example.Test/",
      "http://user:pw@example.test/",
      "http://127.0.0.1@example.test/",
      "http://127.0.0.1.nip.io/",
      "http://localhost.example.test/",
      "http://notlocalhost/",
      "http://8.8.8.8/",
      "http://10.0.0.1/",
      "http://1.2.3.4/",
      "http://[2001:db8::1]/",
      "http://[fec0::1]/",
      "http://[::ffff:8.8.8.8]/",
      "http://[2001:db8:0:0:0:0:0:1]/",
      "http://[2001:db8::1]:8443/x",
      "mailto:a@b.test",
      "MAILTO:a@localhost",
      "tel:+15551234"
    )

  @Test
  fun everySpellingOfABoardOrLoopbackAddressMapsToNothing() {
    for (url in board) assertEquals(url, ThreadTarget.None, externalTarget(url))
  }

  @Test
  fun everyOutsideAddressGoesToTheBrowser() {
    for (url in outside) assertEquals(url, ThreadTarget.Browser(url), externalTarget(url))
  }

  @Test
  fun aMarkdownLinkMapsLikeAnyOtherOutsideAddress() {
    for (url in board) assertEquals(url, ThreadTarget.None, linkTarget(MdLink(url, listOf(MdText("x")))))
    for (url in outside) assertEquals(url, ThreadTarget.Browser(url), linkTarget(MdLink(url, listOf(MdText("x")))))
  }

  @Test
  fun aMarkdownDocPathOpensTheViewerAndOtherSpansOpenNothing() {
    assertEquals(ThreadTarget.Doc("reports/a.md"), linkTarget(MdCode("reports/a.md", "reports/a.md")))
    assertEquals(ThreadTarget.Doc("reports/a.md"), linkTarget(MdPath("reports/a.md", listOf(MdText("x")))))
    assertEquals(ThreadTarget.None, linkTarget(MdCode("reports/a.md")))
    assertEquals(ThreadTarget.None, linkTarget(MdPath("/etc/passwd", listOf(MdText("x")))))
    assertEquals(ThreadTarget.None, linkTarget(MdText("https://example.test/")))
  }

  @Test
  fun aParsedMarkdownParagraphSendsEachLinkWhereItBelongs() {
    val text = "[a](http://localhost:8080/f0123456789ab/) [b](https://example.test/b) [c](http://127.1/x) `reports/a.md`"
    val blocks = markdown(text, MarkdownOptions(refs = true, paths = true, filePaths = false))
    val spans = (blocks.single() as MdParagraph).lines.flatten()
    val targets = spans.filter { it is MdLink || it is MdCode || it is MdPath }.map(::linkTarget)
    assertEquals(
      listOf(ThreadTarget.None, ThreadTarget.Browser("https://example.test/b"), ThreadTarget.None, ThreadTarget.Doc("reports/a.md")),
      targets
    )
    assertTrue(spans.any { it is MdLink })
  }
}
