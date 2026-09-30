package nntp

import (
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

func parseOverview(line string) (Overview, error) {
	fields := strings.Split(line, "\t")
	if len(fields) < 8 {
		return Overview{}, fmt.Errorf("overview has %d fields, want at least 8", len(fields))
	}
	number, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || number < 1 {
		return Overview{}, fmt.Errorf("invalid article number %q", fields[0])
	}
	parsedDate, _ := mailDate(fields[3])
	bytes, _ := strconv.ParseInt(fields[6], 10, 64)
	lines, _ := strconv.ParseInt(fields[7], 10, 64)
	return Overview{ArticleNumber: number, Subject: fields[1], Author: fields[2], Date: parsedDate, RawDate: fields[3], MessageID: strings.TrimSpace(fields[4]), References: strings.TrimSpace(fields[5]), Bytes: bytes, Lines: lines}, nil
}

func mailDate(value string) (time.Time, error) {
	// Overview dates are RFC 5322 dates. Providers may omit the weekday,
	// which is valid mail syntax but rejected by time.RFC1123Z.
	return mail.ParseDate(value)
}
