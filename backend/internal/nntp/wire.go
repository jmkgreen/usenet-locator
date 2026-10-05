package nntp

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type wireClient struct {
	conn  net.Conn
	text  *textproto.Conn
	ep    Endpoint
	meter *countingConn
}

type countingConn struct {
	net.Conn
	readBytes  atomic.Int64
	writeBytes atomic.Int64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.readBytes.Add(int64(n))
	return n, err
}
func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.writeBytes.Add(int64(n))
	return n, err
}
func (c *countingConn) transferBytes() int64 { return c.readBytes.Load() + c.writeBytes.Load() }

// Dial opens an NNTP reader connection with normal TLS verification when TLS
// is enabled. Callers must explicitly opt into plaintext in configuration.
func Dial(ctx context.Context, endpoint Endpoint) (Client, error) {
	if endpoint.Address == "" || endpoint.ConnectTimeout <= 0 || endpoint.ReadTimeout <= 0 {
		return nil, fmt.Errorf("NNTP endpoint address and positive timeouts are required")
	}
	dialer := net.Dialer{Timeout: endpoint.ConnectTimeout}
	raw, err := dialer.DialContext(ctx, "tcp", endpoint.Address)
	if err != nil {
		return nil, fmt.Errorf("dial NNTP: %w", err)
	}
	meter := &countingConn{Conn: raw}
	var conn net.Conn = meter
	if endpoint.TLS {
		secure := tls.Client(meter, &tls.Config{ServerName: endpoint.ServerName, MinVersion: tls.VersionTLS12})
		if err := secure.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, fmt.Errorf("TLS handshake: %w", err)
		}
		conn = secure
	}
	c := &wireClient{conn: conn, text: textproto.NewConn(conn), ep: endpoint, meter: meter}
	if _, _, err := c.status(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read NNTP greeting: %w", err)
	}
	return c, nil
}

func (c *wireClient) Authenticate(ctx context.Context, username, password string) error {
	code, _, err := c.command(ctx, "AUTHINFO USER %s", username)
	if err != nil {
		return err
	}
	if code == 281 {
		return nil
	}
	if code != 381 {
		return fmt.Errorf("AUTHINFO USER rejected with %d", code)
	}
	code, _, err = c.command(ctx, "AUTHINFO PASS %s", password)
	if err != nil {
		return err
	}
	if code != 281 {
		return fmt.Errorf("AUTHINFO PASS rejected with %d", code)
	}
	return nil
}

func (c *wireClient) Capabilities(ctx context.Context) ([]string, error) {
	if err := c.write(ctx, "CAPABILITIES"); err != nil {
		return nil, err
	}
	code, _, err := c.status(ctx)
	if err != nil {
		return nil, err
	}
	if code >= 500 {
		return []string{}, nil
	}
	if code != 101 {
		return nil, fmt.Errorf("CAPABILITIES returned %d", code)
	}
	return c.dotLines(ctx)
}

func (c *wireClient) ModeReader(ctx context.Context) error {
	code, _, err := c.command(ctx, "MODE READER")
	if err != nil {
		return err
	}
	if code == 200 || code == 201 || code == 500 || code == 501 {
		return nil
	}
	return fmt.Errorf("MODE READER returned %d", code)
}

// OverviewFormat asks the server for its overview schema.  Unknown fields are
// intentionally discarded: this is a compatibility signal, not a facility for
// retaining arbitrary server-provided text.
func (c *wireClient) OverviewFormat(ctx context.Context) (OverviewFormat, error) {
	if err := c.write(ctx, "LIST OVERVIEW.FMT"); err != nil {
		return OverviewFormat{}, err
	}
	code, _, err := c.status(ctx)
	if err != nil {
		return OverviewFormat{}, err
	}
	result := OverviewFormat{Code: code}
	if code != 215 {
		return result, nil
	}
	lines, err := c.dotLines(ctx)
	if err != nil {
		return OverviewFormat{}, err
	}
	known := map[string]bool{"subject": true, "from": true, "date": true, "message-id": true, "references": true, ":bytes": true, ":lines": true}
	for _, line := range lines {
		field := strings.ToLower(strings.TrimSpace(line))
		if !strings.HasPrefix(field, ":") {
			field = strings.TrimSuffix(field, ":")
		}
		if known[field] {
			result.Fields = append(result.Fields, field)
		}
	}
	return result, nil
}

// Stat confirms whether an exact Message-ID is available.  The returned
// article number is useful only as a same-session diagnostic after GROUP.
func (c *wireClient) Stat(ctx context.Context, messageID string) (int, error) {
	if strings.ContainsAny(messageID, "\r\n") || !strings.HasPrefix(messageID, "<") || !strings.HasSuffix(messageID, ">") {
		return 0, fmt.Errorf("invalid Message-ID")
	}
	code, message, err := c.command(ctx, "STAT %s", messageID)
	if err != nil {
		return 0, err
	}
	if code != 223 {
		return 0, &OverviewError{Command: "STAT", Code: code}
	}
	fields := strings.Fields(message)
	if len(fields) < 1 {
		return 0, fmt.Errorf("malformed STAT response")
	}
	number, err := strconv.Atoi(fields[0])
	if err != nil || number < 1 {
		return 0, fmt.Errorf("malformed STAT response")
	}
	return number, nil
}

func (c *wireClient) Group(ctx context.Context, name string) (Group, error) {
	if name == "" || strings.ContainsAny(name, "\r\n") {
		return Group{}, fmt.Errorf("invalid newsgroup")
	}
	code, message, err := c.command(ctx, "GROUP %s", name)
	if err != nil {
		return Group{}, err
	}
	if code != 211 {
		return Group{}, fmt.Errorf("GROUP returned %d", code)
	}
	fields := strings.Fields(message)
	if len(fields) < 4 {
		return Group{}, fmt.Errorf("malformed GROUP response")
	}
	low, lowErr := strconv.ParseInt(fields[1], 10, 64)
	high, highErr := strconv.ParseInt(fields[2], 10, 64)
	if lowErr != nil || highErr != nil {
		return Group{}, fmt.Errorf("malformed GROUP bounds")
	}
	return Group{Name: fields[3], Low: low, High: high}, nil
}

func (c *wireClient) Overview(ctx context.Context, low, high int64, emit func(Overview) error) error {
	command := "OVER"
	if c.ep.OverviewCommand == "xover" {
		command = "XOVER"
	}
	if err := c.write(ctx, command+" %d-%d", low, high); err != nil {
		return err
	}
	code, _, err := c.status(ctx)
	if err != nil {
		return err
	}
	// Although 500/501 are the conventional unsupported-command responses,
	// some NNTP deployments use a generic 400 for OVER while still accepting
	// legacy XOVER. Make exactly one bounded compatibility attempt; a server
	// that treats 400 as terminal will simply fail the XOVER write/read.
	usedXOVER := command == "XOVER"
	if !usedXOVER && (code == 400 || code == 500 || code == 501) {
		usedXOVER = true
	}
	if usedXOVER {
		if command == "XOVER" {
			if code != 224 {
				return &OverviewError{Command: command, Code: code}
			}
			return c.eachDotLine(ctx, func(line string) error {
				overview, err := parseOverview(line)
				if err != nil {
					return nil
				}
				return emit(overview)
			})
		}
		if err := c.write(ctx, "XOVER %d-%d", low, high); err != nil {
			return err
		}
		code, _, err = c.status(ctx)
		if err != nil {
			return err
		}
	}
	if code != 224 {
		if usedXOVER {
			command = "XOVER"
		}
		return &OverviewError{Command: command, Code: code}
	}
	return c.eachDotLine(ctx, func(line string) error {
		overview, err := parseOverview(line)
		if err != nil {
			return nil // One malformed header must not abort a historical scan.
		}
		return emit(overview)
	})
}

// Body copies an NNTP dot-encoded body directly to the caller. It never
// materialises the article in process memory and rejects a response exceeding
// the caller's cache limit.
func (c *wireClient) Body(ctx context.Context, articleNumber, maxBytes int64, destination io.Writer) error {
	if articleNumber <= 0 || maxBytes <= 0 || destination == nil {
		return fmt.Errorf("positive article number, body limit, and destination are required")
	}
	code, _, err := c.command(ctx, "BODY %d", articleNumber)
	if err != nil {
		return err
	}
	if code != 222 {
		return fmt.Errorf("BODY returned %d", code)
	}
	defer c.clearDeadline()
	if err := c.deadline(ctx); err != nil {
		return err
	}
	written, err := io.Copy(destination, io.LimitReader(c.text.DotReader(), maxBytes+1))
	if err != nil {
		return fmt.Errorf("stream body: %w", err)
	}
	if written > maxBytes {
		return fmt.Errorf("body exceeds configured limit of %d bytes", maxBytes)
	}
	return nil
}

func (c *wireClient) Close() error { return c.conn.Close() }
func (c *wireClient) TransferBytes() int64 {
	if c.meter == nil {
		return 0
	}
	return c.meter.transferBytes()
}

func (c *wireClient) command(ctx context.Context, format string, args ...any) (int, string, error) {
	if err := c.write(ctx, format, args...); err != nil {
		return 0, "", err
	}
	return c.status(ctx)
}

func (c *wireClient) write(ctx context.Context, format string, args ...any) error {
	defer c.clearDeadline()
	if err := c.deadline(ctx); err != nil {
		return err
	}
	return c.text.PrintfLine(format, args...)
}

func (c *wireClient) status(ctx context.Context) (int, string, error) {
	defer c.clearDeadline()
	if err := c.deadline(ctx); err != nil {
		return 0, "", err
	}
	line, err := c.text.ReadLine()
	if err != nil {
		return 0, "", err
	}
	if len(line) < 3 {
		return 0, "", fmt.Errorf("malformed NNTP status")
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return 0, "", fmt.Errorf("malformed NNTP status: %w", err)
	}
	return code, strings.TrimSpace(line[3:]), nil
}

func (c *wireClient) dotLines(ctx context.Context) ([]string, error) {
	var lines []string
	err := c.eachDotLine(ctx, func(line string) error { lines = append(lines, line); return nil })
	return lines, err
}

func (c *wireClient) eachDotLine(ctx context.Context, receive func(string) error) error {
	defer c.clearDeadline()
	if err := c.deadline(ctx); err != nil {
		return err
	}
	scanner := bufio.NewScanner(c.text.DotReader())
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		if err := receive(scanner.Text()); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (c *wireClient) deadline(ctx context.Context) error {
	deadline := time.Now().Add(c.ep.ReadTimeout)
	if fromContext, ok := ctx.Deadline(); ok && fromContext.Before(deadline) {
		deadline = fromContext
	}
	return c.conn.SetDeadline(deadline)
}
func (c *wireClient) clearDeadline() { _ = c.conn.SetDeadline(time.Time{}) }
