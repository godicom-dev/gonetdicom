package dicomweb

import (
	"errors"
	"fmt"
	"io"
)

// Body-size bounds. Both sides of this package buffer whole instances in memory,
// so a body is charged to RAM as it arrives: unbounded, one request decides how
// much memory the process uses, and on the Handler side that request needs no
// credentials.
const (
	// DefaultMaxResponseBytes bounds one Client response body.
	DefaultMaxResponseBytes = 1 << 30 // 1 GiB
	// DefaultMaxRequestBytes bounds one Handler request body. Lower than the
	// response bound because a STOW-RS body is buffered and then handed to a Store
	// that usually copies it, and because anyone can post one.
	DefaultMaxRequestBytes = 256 << 20 // 256 MiB
)

// ErrTooLarge reports a body that ran past its size bound.
var ErrTooLarge = errors.New("dicomweb: body too large")

// capReader returns r bounded to limit bytes, failing with ErrTooLarge rather
// than reporting EOF at the bound as io.LimitReader does: a Part 10 instance cut
// short still parses as an instance, so the quiet version of this limit hands the
// caller a truncated study and calls it success. A limit of 0 or less reads
// without a bound.
func capReader(r io.Reader, limit int64) io.Reader {
	if limit <= 0 {
		return r
	}
	return &cappedReader{r: r, left: limit, limit: limit}
}

type cappedReader struct {
	r     io.Reader
	left  int64 // bytes still allowed; one more is read to notice the overrun
	limit int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	// Read at most one byte past the bound: a body of exactly limit bytes is legal,
	// and the extra byte is what distinguishes it from one that is too long.
	if int64(len(p)) > c.left+1 {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return n, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, c.limit)
	}
	return n, err
}

// readAll reads r to EOF, or fails with ErrTooLarge once it passes limit.
func readAll(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(capReader(r, limit))
}
