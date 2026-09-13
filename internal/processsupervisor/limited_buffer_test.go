package processsupervisor

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestAuthoringLimitedBufferCopyPathsPreserveBoundAndDrain(t *testing.T) {
	for _, path := range []string{"ReadFrom", "reader-only", "WriterTo", "WriteString", "pipe"} {
		for _, limit := range []int{0, 8, 16} {
			t.Run(path+"/"+strconv.Itoa(limit), func(t *testing.T) {
				const input = "0123456789abcdef"
				capture := limitedBuffer{limit: limit}
				var n int64
				var err error
				switch path {
				case "ReadFrom":
					n, err = capture.ReadFrom(strings.NewReader(input))
				case "reader-only":
					n, err = io.Copy(&capture, struct{ io.Reader }{strings.NewReader(input)})
				case "WriterTo":
					n, err = io.Copy(&capture, strings.NewReader(input))
				case "WriteString":
					var written int
					written, err = io.WriteString(&capture, input)
					n = int64(written)
				case "pipe":
					reader, writer, pipeErr := os.Pipe()
					if pipeErr != nil {
						t.Fatal(pipeErr)
					}
					defer reader.Close()
					written := make(chan error, 1)
					go func() {
						_, writeErr := io.WriteString(writer, input)
						closeErr := writer.Close()
						if writeErr != nil {
							written <- writeErr
						} else {
							written <- closeErr
						}
					}()
					n, err = io.Copy(&capture, reader)
					if writeErr := <-written; writeErr != nil {
						t.Fatal(writeErr)
					}
				}
				if err != nil || n != int64(len(input)) {
					t.Fatal("copy did not drain all bytes")
				}
				if capture.String() != input[:limit] || capture.Len() != limit || capture.truncated != (limit < len(input)) {
					t.Fatal("copy bypassed retained prefix bound")
				}
			})
		}
	}
}

func TestAuthoringLimitedBufferExposesOnlyBoundedWriteMethods(t *testing.T) {
	var capture any = &limitedBuffer{}
	if _, ok := capture.(io.StringWriter); ok {
		t.Fatal("uncapped StringWriter exposed")
	}
	if _, ok := capture.(io.ByteWriter); ok {
		t.Fatal("uncapped ByteWriter exposed")
	}
	if _, ok := capture.(interface{ WriteRune(rune) (int, error) }); ok {
		t.Fatal("uncapped WriteRune exposed")
	}
	if _, ok := capture.(io.Reader); ok {
		t.Fatal("mutating Reader exposed")
	}
	if _, ok := capture.(io.ByteReader); ok {
		t.Fatal("mutating ByteReader exposed")
	}
	if _, ok := capture.(io.RuneReader); ok {
		t.Fatal("mutating RuneReader exposed")
	}
	if _, ok := capture.(io.WriterTo); ok {
		t.Fatal("mutating WriterTo exposed")
	}
	if _, ok := capture.(interface{ Reset() }); ok {
		t.Fatal("capture reset exposed")
	}
}

type limitedBufferErrorReader struct{ err error }

func (r limitedBufferErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestAuthoringLimitedBufferPrefilledAndReadError(t *testing.T) {
	capture := limitedBuffer{limit: 5}
	if _, err := capture.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("fixture read failure")
	reader := io.MultiReader(strings.NewReader("defgh"), limitedBufferErrorReader{cause})
	n, err := capture.ReadFrom(reader)
	if n != 5 || !errors.Is(err, cause) || capture.String() != "abcde" || !capture.truncated {
		t.Fatal("prefilled copy lost bound, drain count, or reader error")
	}
}
