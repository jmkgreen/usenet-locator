package archive

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"sort"
	"strings"
	"unicode/utf8"

	nethtml "golang.org/x/net/html"
	"golang.org/x/text/encoding/htmlindex"
)

type Part struct {
	ContentType string `json:"content_type"`
	Filename    string `json:"filename,omitempty"`
	Disposition string `json:"disposition,omitempty"`
}
type Reader struct {
	Text              string   `json:"text"`
	HTML              string   `json:"html,omitempty"`
	RawHeaders        string   `json:"raw_headers,omitempty"`
	Parts             []Part   `json:"parts"`
	Flags             []string `json:"flags"`
	NormalizerVersion int      `json:"normalizer_version"`
}

// DecodeReader bounds recursion, part count and decoded bytes, preserving the
// caller's original byte slices separately from the derived representation.
func DecodeReader(headers, body []byte, maxBytes int64) Reader {
	result := Reader{Parts: []Part{}, Flags: []string{}, NormalizerVersion: NormalizerVersion}
	flags := map[string]bool{}
	result.RawHeaders = safeText(headers, flags)
	header := textproto.MIMEHeader{}
	if len(headers) > 0 {
		message, err := mail.ReadMessage(bytes.NewReader(append(bytes.Clone(headers), '\n')))
		if err != nil {
			flags["malformed_header"] = true
		} else {
			header = textproto.MIMEHeader(message.Header)
		}
	} else {
		flags["headers_unavailable"] = true
	}
	remaining := maxBytes
	var walk func(textproto.MIMEHeader, io.Reader, int)
	walk = func(h textproto.MIMEHeader, r io.Reader, depth int) {
		if depth > 12 || len(result.Parts) >= 100 || remaining <= 0 {
			flags["body_truncated"] = true
			return
		}
		contentType, params, err := mime.ParseMediaType(h.Get("Content-Type"))
		if h.Get("Content-Type") == "" {
			contentType = "text/plain"
		} else if err != nil {
			contentType = "text/plain"
			flags["malformed_header"] = true
		}
		disposition, dispParams, dispErr := mime.ParseMediaType(h.Get("Content-Disposition"))
		if h.Get("Content-Disposition") != "" && dispErr != nil {
			flags["malformed_header"] = true
		}
		filename := dispParams["filename"]
		if filename == "" {
			filename = params["name"]
		}
		result.Parts = append(result.Parts, Part{ContentType: contentType, Filename: safeText([]byte(filename), flags), Disposition: disposition})
		if strings.HasPrefix(contentType, "multipart/") {
			if params["boundary"] == "" {
				flags["body_decode_failed"] = true
				return
			}
			parts := multipart.NewReader(r, params["boundary"])
			for {
				part, err := parts.NextRawPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					flags["body_decode_failed"] = true
					break
				}
				walk(part.Header, part, depth+1)
				_ = part.Close()
				if len(result.Parts) >= 100 || remaining <= 0 {
					flags["body_truncated"] = true
					break
				}
			}
			return
		}
		if disposition == "attachment" || filename != "" || (contentType != "text/plain" && contentType != "text/html") {
			return
		}
		switch strings.ToLower(strings.TrimSpace(h.Get("Content-Transfer-Encoding"))) {
		case "", "7bit", "8bit", "binary":
		case "base64":
			r = base64.NewDecoder(base64.StdEncoding, r)
		case "quoted-printable":
			r = quotedprintable.NewReader(r)
		default:
			flags["unknown_encoding"] = true
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(r, remaining+1))
		if readErr != nil {
			flags["body_decode_failed"] = true
		}
		if int64(len(data)) > remaining {
			data = data[:remaining]
			flags["body_truncated"] = true
		}
		remaining -= int64(len(data))
		charset := strings.ToLower(params["charset"])
		if charset != "" && charset != "utf-8" && charset != "us-ascii" {
			encoding, encErr := htmlindex.Get(charset)
			if encErr != nil {
				flags["unknown_encoding"] = true
			} else {
				decoded, decErr := io.ReadAll(io.LimitReader(encoding.NewDecoder().Reader(bytes.NewReader(data)), maxBytes+1))
				if decErr != nil {
					flags["body_decode_failed"] = true
				} else if int64(len(decoded)) > maxBytes {
					flags["body_truncated"] = true
					data = decoded[:maxBytes]
				} else {
					data = decoded
				}
			}
		}
		text := safeText(data, flags)
		if contentType == "text/plain" && result.Text == "" {
			result.Text = text
		}
		if contentType == "text/html" && result.HTML == "" {
			result.HTML = SanitizeHTML(text)
		}
	}
	walk(header, bufio.NewReader(bytes.NewReader(body)), 0)
	if result.Text == "" && result.HTML != "" {
		flags["html_only"] = true
	}
	for flag := range flags {
		result.Flags = append(result.Flags, flag)
	}
	sort.Strings(result.Flags)
	return result
}

func safeText(data []byte, flags map[string]bool) string {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		flags["unknown_encoding"] = true
	}
	return strings.ReplaceAll(strings.ToValidUTF8(string(data), "\uFFFD"), "\x00", "\uFFFD")
}

// Render only structural tags and text. No attributes, URLs, styles, scripts,
// forms or embedded content survive, including in malformed HTML.
func SanitizeHTML(input string) string {
	doc, err := nethtml.Parse(strings.NewReader(input))
	if err != nil {
		return html.EscapeString(input)
	}
	allowed := map[string]bool{"p": true, "br": true, "pre": true, "blockquote": true, "strong": true, "b": true, "em": true, "i": true, "u": true, "ul": true, "ol": true, "li": true, "h1": true, "h2": true, "h3": true, "h4": true, "hr": true, "code": true, "div": true, "span": true, "table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true}
	drop := map[string]bool{"script": true, "style": true, "iframe": true, "object": true, "embed": true, "svg": true, "math": true, "form": true, "template": true, "noscript": true, "head": true}
	var out strings.Builder
	var render func(*nethtml.Node, int)
	render = func(node *nethtml.Node, depth int) {
		if depth > 128 {
			return
		}
		if node.Type == nethtml.TextNode {
			out.WriteString(html.EscapeString(node.Data))
			return
		}
		if node.Type == nethtml.ElementNode && drop[node.Data] {
			return
		}
		name := node.Data
		if node.Type == nethtml.ElementNode && allowed[name] {
			fmt.Fprintf(&out, "<%s>", name)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			render(child, depth+1)
		}
		if node.Type == nethtml.ElementNode && allowed[name] && name != "br" && name != "hr" {
			fmt.Fprintf(&out, "</%s>", name)
		}
	}
	render(doc, 0)
	return out.String()
}
