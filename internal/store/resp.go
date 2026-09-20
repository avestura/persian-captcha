package store

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// This file implements just enough of the Redis serialization protocol
// (RESP2) to run the handful of commands the service needs: AUTH, SELECT,
// PING, SET, GET, GETDEL, DEL, INCR and PEXPIRE.
//
// A full client library would be a heavy dependency for a security-adjacent
// service whose Redis usage is this small, so the protocol is spoken directly.

// respError is an error reply sent by the server, such as WRONGTYPE or NOAUTH.
type respError string

func (e respError) Error() string { return "redis: " + string(e) }

// errNilReply signals RESP nil, which for GET-style commands means "missing".
var errNilReply = errors.New("redis: nil reply")

type respConn struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

func newRESPConn(c net.Conn) *respConn {
	return &respConn{
		conn: c,
		r:    bufio.NewReaderSize(c, 4096),
		w:    bufio.NewWriterSize(c, 4096),
	}
}

func (c *respConn) close() error { return c.conn.Close() }

// writeCommand serializes args as a RESP array of bulk strings. Arguments are
// either string or []byte.
func (c *respConn) writeCommand(args ...any) error {
	if _, err := fmt.Fprintf(c.w, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, a := range args {
		var b []byte
		switch v := a.(type) {
		case string:
			b = []byte(v)
		case []byte:
			b = v
		case int:
			b = []byte(strconv.Itoa(v))
		case int64:
			b = []byte(strconv.FormatInt(v, 10))
		default:
			return fmt.Errorf("redis: unsupported argument type %T", a)
		}
		if _, err := fmt.Fprintf(c.w, "$%d\r\n", len(b)); err != nil {
			return err
		}
		if _, err := c.w.Write(b); err != nil {
			return err
		}
		if _, err := c.w.WriteString("\r\n"); err != nil {
			return err
		}
	}
	return c.w.Flush()
}

// readLine reads one CRLF-terminated protocol line without its terminator.
func (c *respConn) readLine() ([]byte, error) {
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, errors.New("redis: malformed reply line")
	}
	return line[:len(line)-2], nil
}

// readReply parses a single reply. Bulk strings come back as []byte, integers
// as int64, simple strings as string, arrays as []any.
func (c *respConn) readReply() (any, error) {
	line, err := c.readLine()
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("redis: empty reply")
	}
	body := string(line[1:])
	switch line[0] {
	case '+':
		return body, nil
	case '-':
		return nil, respError(body)
	case ':':
		return strconv.ParseInt(body, 10, 64)
	case '_': // RESP3 null
		return nil, errNilReply
	case '$':
		n, err := strconv.Atoi(body)
		if err != nil {
			return nil, fmt.Errorf("redis: bad bulk length %q", body)
		}
		if n < 0 {
			return nil, errNilReply
		}
		buf := make([]byte, n+2) // include the trailing CRLF
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return nil, err
		}
		return buf[:n], nil
	case '*':
		n, err := strconv.Atoi(body)
		if err != nil {
			return nil, fmt.Errorf("redis: bad array length %q", body)
		}
		if n < 0 {
			return nil, errNilReply
		}
		out := make([]any, 0, n)
		for range n {
			v, err := c.readReply()
			if err != nil && !errors.Is(err, errNilReply) {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("redis: unknown reply type %q", line[0])
	}
}

// do writes a command and reads its reply under the given deadline.
func (c *respConn) do(timeout time.Duration, args ...any) (any, error) {
	if timeout > 0 {
		if err := c.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return nil, err
		}
		defer c.conn.SetDeadline(time.Time{})
	}
	if err := c.writeCommand(args...); err != nil {
		return nil, err
	}
	return c.readReply()
}
