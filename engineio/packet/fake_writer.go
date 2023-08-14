package packet

import (
	"io"

	"github.com/thisismz/go-socket.io/v4/engineio/frame"
)

type fakeConnWriter struct {
	Frames []Frame
}

func newFakeConnWriter() *fakeConnWriter {
	return &fakeConnWriter{}
}

func (w *fakeConnWriter) NextWriter(fType frame.Type) (io.WriteCloser, error) {
	return newFakeFrame(w, fType), nil
}
