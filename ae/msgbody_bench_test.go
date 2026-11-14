package ae

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

// Stream against Bytes over the same fragments, at three sizes. This is the
// claim the single-slot channel makes, stated as numbers: the resident cost of
// a store does not grow with the store. B/op for Stream is io.Copy's buffer
// whatever the payload; B/op for Bytes is the payload plus the copying it takes
// to hold it contiguously.
//
// Neither reference library can be compared on equal terms here. pynetdicom's
// Event.dataset is always a BytesIO, so its behaviour is the Bytes row; dcm4che
// reaches the Stream row only when the caller brings its own buffer to
// PDVInputStream, which moves the allocation rather than removing it.

const benchFragSize = 16 << 10

// benchFragments splits size into fragments the way the association reader
// would, so both paths see the same input.
func benchFragments(size int) [][]byte {
	frags := make([][]byte, 0, size/benchFragSize+1)
	for off := 0; off < size; off += benchFragSize {
		end := min(off+benchFragSize, size)
		frags = append(frags, bytes.Repeat([]byte{0x5a}, end-off))
	}
	return frags
}

// pushFragments plays the reader goroutine. Its cost is real and counted: on the
// SCP side there is one such goroutine per association, and hiding it from the
// benchmark would flatter the streaming path by exactly the thing that makes it
// work.
func pushFragments(body *msgBody, frags [][]byte) {
	for _, f := range frags {
		if !body.push(f) {
			return
		}
	}
	body.finish(nil)
}

func benchSizes() []int {
	return []int{4 << 10, 1 << 20, 16 << 20}
}

func BenchmarkStoreBodyStream(b *testing.B) {
	for _, size := range benchSizes() {
		frags := benchFragments(size)
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				body := newMsgBody()
				go pushFragments(body, frags)
				if _, err := io.Copy(io.Discard, bodyReader(body)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkStoreBodyBytes(b *testing.B) {
	for _, size := range benchSizes() {
		frags := benchFragments(size)
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				body := newMsgBody()
				go pushFragments(body, frags)
				if _, err := body.Bytes(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The SCU side of a C-GET pulls instead of being pushed to: no goroutine, the
// socket read happens inside Read. Same fragments, different mechanism, and the
// difference in B/op is the channel that is not there.
func BenchmarkStoreBodyPull(b *testing.B) {
	for _, size := range benchSizes() {
		frags := benchFragments(size)
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				next := 0
				body := newPullBody(func() ([]byte, bool, error) {
					if next >= len(frags) {
						return nil, true, nil
					}
					f := frags[next]
					next++
					return f, next == len(frags), nil
				})
				if _, err := io.Copy(io.Discard, bodyReader(body)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
