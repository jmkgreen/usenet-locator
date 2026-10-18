// Package archive derives safe, versioned article metadata from provider data.
package archive

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
	"golang.org/x/text/encoding/htmlindex"
)

const NormalizerVersion = 1

type Normalized struct {
	Overview   nntp.Overview
	SubjectKey string
	Flags      []string
	Source     []byte
}

var replyPrefix = regexp.MustCompile(`(?i)^(re|fw|fwd)(\[[0-9]+\])?:\s*`)
var referenceID = regexp.MustCompile(`<[^<>\s]+@[^<>\s]+>`)

// NormalizeOverview preserves the exact original field bytes as base64-encoded
// JSON values. It is deterministic for a fixed normalizer version.
func NormalizeOverview(input nntp.Overview) Normalized {
	source, _ := json.Marshal(map[string][]byte{
		"subject": []byte(input.Subject), "author": []byte(input.Author),
		"date": []byte(input.RawDate), "message_id": []byte(input.MessageID),
		"references": []byte(input.References),
		"bytes":      []byte(strconv.FormatInt(input.Bytes, 10)), "lines": []byte(strconv.FormatInt(input.Lines, 10)),
	})
	flags := map[string]bool{}
	clean := func(value string) string {
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			flags["malformed_header"] = true
			value = strings.ToValidUTF8(value, "\uFFFD")
			value = strings.ReplaceAll(value, "\x00", "\uFFFD")
		}
		return value
	}
	decoder := mime.WordDecoder{CharsetReader: func(charset string, reader io.Reader) (io.Reader, error) {
		encoding, err := htmlindex.Get(charset)
		if err != nil {
			return nil, err
		}
		return encoding.NewDecoder().Reader(reader), nil
	}}
	decode := func(value string) string {
		value = clean(value)
		if !strings.Contains(value, "=?") {
			return value
		}
		decoded, err := decoder.DecodeHeader(value)
		if err != nil {
			flags["unknown_encoding"] = true
			return value
		}
		return clean(decoded)
	}
	input.Subject = decode(input.Subject)
	input.Author = decode(input.Author)
	input.RawDate = clean(input.RawDate)
	input.References = clean(input.References)
	input.MessageID = clean(input.MessageID)
	if input.RawDate != "" && input.Date.IsZero() {
		flags["invalid_date"] = true
	}
	if input.Author != "" {
		if _, err := mail.ParseAddressList(input.Author); err != nil {
			flags["malformed_header"] = true
		}
	}
	if input.References != "" {
		remaining := referenceID.ReplaceAllString(input.References, "")
		if strings.TrimSpace(remaining) != "" {
			flags["threading_uncertain"] = true
		}
	}
	if input.Bytes < 0 {
		input.Bytes = 0
		flags["malformed_header"] = true
	}
	if input.Lines < 0 {
		input.Lines = 0
		flags["malformed_header"] = true
	}
	key := strings.TrimSpace(input.Subject)
	for {
		next := replyPrefix.ReplaceAllString(key, "")
		if next == key {
			break
		}
		key = strings.TrimSpace(next)
	}
	key = strings.ToLower(strings.Join(strings.Fields(key), " "))
	resultFlags := make([]string, 0, len(flags))
	for flag := range flags {
		resultFlags = append(resultFlags, flag)
	}
	sort.Strings(resultFlags)
	return Normalized{Overview: input, SubjectKey: key, Flags: resultFlags, Source: bytes.Clone(source)}
}
