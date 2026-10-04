package nntp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestOverviewFallsBackAndStreamsValidRecords(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() { _ = clientConn.Close() })
	go func() {
		defer serverConn.Close()
		reader := bufio.NewReader(serverConn)
		writer := bufio.NewWriter(serverConn)
		line, _ := reader.ReadString('\n')
		if line != "OVER 1-3\r\n" {
			return
		}
		_, _ = writer.WriteString("500 unsupported\r\n")
		_ = writer.Flush()
		line, _ = reader.ReadString('\n')
		if line != "XOVER 1-3\r\n" {
			return
		}
		_, _ = writer.WriteString("224 overview follows\r\n")
		_, _ = writer.WriteString("bad\tline\r\n")
		_, _ = writer.WriteString("2\tSubject\tAlice\tMon, 02 Jan 2006 15:04:05 -0700\t<id@example>\t\t42\t3\r\n.\r\n")
		_ = writer.Flush()
		<-done
	}()

	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second}}
	var received []Overview
	err := c.Overview(context.Background(), 1, 3, func(item Overview) error {
		received = append(received, item)
		return nil
	})
	close(done)
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if len(received) != 1 || received[0].MessageID != "<id@example>" {
		t.Fatalf("received = %#v", received)
	}
}

func TestOverviewReturnsOnlyCommandAndCodeForServerFailure(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	go func() {
		defer serverConn.Close()
		reader, writer := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
		_, _ = reader.ReadString('\n')
		_, _ = writer.WriteString("430 arbitrary provider response text\r\n")
		_ = writer.Flush()
	}()
	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second}}
	err := c.Overview(context.Background(), 1, 1, func(Overview) error { return nil })
	var overviewError *OverviewError
	if !errors.As(err, &overviewError) || overviewError.Command != "OVER" || overviewError.Code != 430 {
		t.Fatalf("error = %#v", err)
	}
}

func TestOverviewFallsBackToXOVERAfterGeneric400(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() { close(done); _ = clientConn.Close() })
	go func() {
		defer serverConn.Close()
		reader, writer := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
		line, _ := reader.ReadString('\n')
		if line != "OVER 1-1\r\n" {
			return
		}
		_, _ = writer.WriteString("400 generic unsupported response\r\n")
		_ = writer.Flush()
		line, _ = reader.ReadString('\n')
		if line != "XOVER 1-1\r\n" {
			return
		}
		_, _ = writer.WriteString("224 overview follows\r\n1\tSubject\tAuthor\tSat, 11 Jul 2026 22:25:34 -0100\t<probe@example>\t\t1\t1\r\n.\r\n")
		_ = writer.Flush()
		<-done
	}()
	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second}}
	var received []Overview
	if err := c.Overview(context.Background(), 1, 1, func(item Overview) error { received = append(received, item); return nil }); err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if len(received) != 1 || received[0].MessageID != "<probe@example>" {
		t.Fatalf("received = %#v", received)
	}
}

func TestOverviewStartsWithConfiguredXOVER(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() { close(done); _ = clientConn.Close() })
	go func() {
		defer serverConn.Close()
		reader, writer := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
		line, _ := reader.ReadString('\n')
		if line != "XOVER 1-1\r\n" {
			return
		}
		_, _ = writer.WriteString("224 overview follows\r\n1\tSubject\tAuthor\tSat, 11 Jul 2026 22:25:34 -0100\t<configured@example>\t\t1\t1\r\n.\r\n")
		_ = writer.Flush()
		<-done
	}()
	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second, OverviewCommand: "xover"}}
	var received []Overview
	if err := c.Overview(context.Background(), 1, 1, func(item Overview) error { received = append(received, item); return nil }); err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if len(received) != 1 || received[0].MessageID != "<configured@example>" {
		t.Fatalf("received = %#v", received)
	}
}

func TestDialRejectsMissingTimeouts(t *testing.T) {
	if _, err := Dial(context.Background(), Endpoint{Address: "example.invalid:563", TLS: true}); err == nil {
		t.Fatal("Dial() accepted missing timeout settings")
	}
}

func TestGroupReadTimesOut(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	go func() {
		defer serverConn.Close()
		reader := bufio.NewReader(serverConn)
		_, _ = reader.ReadString('\n') // Accept GROUP, deliberately never reply.
		time.Sleep(100 * time.Millisecond)
	}()

	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: 10 * time.Millisecond}}
	if _, err := c.Group(context.Background(), "example.group"); err == nil {
		t.Fatal("Group() succeeded despite a stalled server")
	}
}

func TestCapabilitiesAndLegacyReaderMode(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() { _ = clientConn.Close() })
	go func() {
		defer serverConn.Close()
		reader, writer := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
		line, _ := reader.ReadString('\n')
		if line != "CAPABILITIES\r\n" {
			return
		}
		_, _ = writer.WriteString("500 unsupported\r\n")
		_ = writer.Flush()
		line, _ = reader.ReadString('\n')
		if line != "MODE READER\r\n" {
			return
		}
		_, _ = writer.WriteString("500 unsupported\r\n")
		_ = writer.Flush()
		<-done
	}()
	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second}}
	caps, err := c.Capabilities(context.Background())
	if err != nil || len(caps) != 0 {
		t.Fatalf("Capabilities() = %v, %v", caps, err)
	}
	if err := c.ModeReader(context.Background()); err != nil {
		t.Fatalf("ModeReader() error = %v", err)
	}
	close(done)
}

func TestCapabilitiesReadsMultilineReply(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	defer func() { close(done); _ = clientConn.Close() }()
	go func() {
		defer serverConn.Close()
		r, w := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
		_, _ = r.ReadString('\n')
		_, _ = w.WriteString("101 capabilities\r\nREADER\r\nOVER\r\n.\r\n")
		_ = w.Flush()
		<-done
	}()
	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second}}
	caps, err := c.Capabilities(context.Background())
	if err != nil || len(caps) != 2 || caps[0] != "READER" {
		t.Fatalf("caps=%#v err=%v", caps, err)
	}
}

func TestOverviewFormatKeepsOnlyRecognisedFields(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		_ = clientConn.Close()
	})
	go func() {
		defer serverConn.Close()
		reader, writer := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
		_, _ = reader.ReadString('\n')
		_, _ = writer.WriteString("215 overview format follows\r\nSubject:\r\nFrom:\r\nX-Provider-Internal: opaque\r\n:bytes\r\n.\r\n")
		_ = writer.Flush()
		<-done
	}()
	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second}}
	format, err := c.OverviewFormat(context.Background())
	if err != nil || format.Code != 215 || len(format.Fields) != 3 || format.Fields[2] != ":bytes" {
		t.Fatalf("OverviewFormat() = %#v, %v", format, err)
	}
}

func TestStatReturnsArticleNumberWithoutResponseText(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	go func() {
		defer serverConn.Close()
		reader, writer := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
		_, _ = reader.ReadString('\n')
		_, _ = writer.WriteString("223 41 <probe@example> arbitrary text\r\n")
		_ = writer.Flush()
	}()
	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second}}
	if number, err := c.Stat(context.Background(), "<probe@example>"); err != nil || number != 41 {
		t.Fatalf("Stat() = %d, %v", number, err)
	}
}

func TestBodyStreamsDotStuffedResponseWithinLimit(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() { close(done); _ = clientConn.Close() })
	go func() {
		defer serverConn.Close()
		reader, writer := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
		line, _ := reader.ReadString('\n')
		if line != "BODY 4\r\n" {
			return
		}
		_, _ = writer.WriteString("222 body follows\r\n..leading-dot\r\nplain\r\n.\r\n")
		_ = writer.Flush()
		<-done
	}()
	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second}}
	var body bytes.Buffer
	if err := c.Body(context.Background(), 4, 64, &body); err != nil {
		t.Fatalf("Body() error = %v", err)
	}
	if got := body.String(); got != ".leading-dot\nplain\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestBodyRejectsResponseAboveLimit(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	go func() {
		defer serverConn.Close()
		reader, writer := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
		_, _ = reader.ReadString('\n')
		_, _ = writer.WriteString("222 body follows\r\n123456\r\n.\r\n")
		_ = writer.Flush()
	}()
	c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second}}
	if err := c.Body(context.Background(), 1, 3, io.Discard); err == nil {
		t.Fatal("Body() accepted an oversized response")
	}
}

func TestAuthenticateAndTransferAccounting(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go func() {
		defer serverConn.Close()
		r, w := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
		if line, _ := r.ReadString('\n'); line != "AUTHINFO USER operator\r\n" {
			return
		}
		_, _ = w.WriteString("381 password required\r\n")
		_ = w.Flush()
		if line, _ := r.ReadString('\n'); line != "AUTHINFO PASS secret\r\n" {
			return
		}
		_, _ = w.WriteString("281 authenticated\r\n")
		_ = w.Flush()
	}()
	meter := &countingConn{Conn: clientConn}
	c := &wireClient{conn: meter, text: textproto.NewConn(meter), ep: Endpoint{ReadTimeout: time.Second}, meter: meter}
	if err := c.Authenticate(context.Background(), "operator", "secret"); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if c.TransferBytes() <= 0 {
		t.Fatal("authentication traffic was not metered")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestAuthenticateRejectsProviderResponsesWithoutEchoingThem(t *testing.T) {
	for _, tc := range []struct{ response string }{{"480 provider transcript secret\r\n"}, {"381 continue\r\n481 password transcript secret\r\n"}} {
		t.Run(tc.response[:3], func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			go func() {
				defer serverConn.Close()
				r, w := bufio.NewReader(serverConn), bufio.NewWriter(serverConn)
				_, _ = r.ReadString('\n')
				_, _ = w.WriteString(tc.response)
				_ = w.Flush()
				if tc.response[:3] == "381" {
					_, _ = r.ReadString('\n')
				}
			}()
			c := &wireClient{conn: clientConn, text: textproto.NewConn(clientConn), ep: Endpoint{ReadTimeout: time.Second}}
			err := c.Authenticate(context.Background(), "operator", "password")
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
