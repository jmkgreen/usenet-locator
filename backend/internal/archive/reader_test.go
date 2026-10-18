package archive

import (
	"strings"
	"testing"
)

func TestDecodeReaderHandlesMultipartAndStripsActiveHTML(t *testing.T) {
	headers := []byte("Content-Type: multipart/alternative; boundary=abc\nX-Test: original\n")
	body := []byte("--abc\r\nContent-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\ncaf=E9\r\n--abc\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p onclick=\"evil()\">Hello<img src=\"https://tracker.invalid/x\"><script>alert(1)</script><a href=\"https://tracker.invalid\">link</a></p>\r\n--abc--\r\n")
	reader := DecodeReader(headers, body, 4096)
	if reader.Text != "café" || !strings.Contains(reader.HTML, "Hello") || !strings.Contains(reader.HTML, "link") || len(reader.Parts) != 3 {
		t.Fatalf("reader = %#v", reader)
	}
	for _, bad := range []string{"script", "onclick", "img", "https:", "href", "alert"} {
		if strings.Contains(reader.HTML, bad) {
			t.Fatalf("unsafe HTML %q", reader.HTML)
		}
	}
	if !strings.Contains(reader.RawHeaders, "X-Test: original") {
		t.Fatalf("raw headers = %q", reader.RawHeaders)
	}
}

func TestDecodeReaderFlagsMalformedAndHTMLOnly(t *testing.T) {
	reader := DecodeReader([]byte("Content-Type: text/html; charset=made-up\n"), []byte("<b>read</b>"), 100)
	flags := strings.Join(reader.Flags, ",")
	if !strings.Contains(flags, "unknown_encoding") || !strings.Contains(flags, "html_only") || reader.HTML != "<b>read</b>" {
		t.Fatalf("reader = %#v", reader)
	}
}

func TestDecodeReaderBoundsDecodedBody(t *testing.T) {
	reader := DecodeReader([]byte("Content-Type: text/plain\nContent-Transfer-Encoding: base64\n"), []byte("YWJjZGVm"), 3)
	if reader.Text != "abc" || !strings.Contains(strings.Join(reader.Flags, ","), "body_truncated") {
		t.Fatalf("reader = %#v", reader)
	}
}
