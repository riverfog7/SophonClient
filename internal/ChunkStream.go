package internal

import (
	"errors"
	"fmt"
	"io"
)

// ChunkStream provides a view over a portion of an underlying stream
type ChunkStream struct {
	stream      io.ReadWriteSeeker
	start       int64
	end         int64
	curPos      int64
	isDisposing bool
}

// NewChunkStream creates a new ChunkStream that represents a segment of the underlying stream
func NewChunkStream(stream io.ReadWriteSeeker, start, end int64, isDisposing bool) (*ChunkStream, error) {
	// Get the stream length
	streamLen, err := stream.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("failed to get stream length: %w", err)
	}

	// Reset position
	_, err = stream.Seek(0, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to reset stream position: %w", err)
	}

	if streamLen == 0 {
		return nil, errors.New("the stream must not have 0 bytes")
	}

	if streamLen < start || end > streamLen {
		return nil, fmt.Errorf("argument out of range: start=%d, end=%d, stream length=%d", start, end, streamLen)
	}

	// Set position to start
	_, err = stream.Seek(start, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek to start position: %w", err)
	}

	return &ChunkStream{
		stream:      stream,
		start:       start,
		end:         end,
		curPos:      0,
		isDisposing: isDisposing,
	}, nil
}

// size returns the size of the chunk
func (cs *ChunkStream) size() int64 {
	return cs.end - cs.start
}

// remain returns the remaining bytes in the chunk
func (cs *ChunkStream) remain() int64 {
	return cs.size() - cs.curPos
}

// Read reads up to len(p) bytes into p from the chunk
func (cs *ChunkStream) Read(p []byte) (n int, err error) {
	if cs.remain() == 0 {
		return 0, io.EOF
	}

	toRead := int64(len(p))
	if toRead > cs.remain() {
		toRead = cs.remain()
	}

	// Set position in the underlying stream
	_, err = cs.stream.Seek(cs.start+cs.curPos, io.SeekStart)
	if err != nil {
		return 0, fmt.Errorf("failed to seek: %w", err)
	}

	// Read from the underlying stream
	read, err := cs.stream.Read(p[:toRead])
	cs.curPos += int64(read)

	return read, err
}

// Write writes len(p) bytes from p to the chunk
func (cs *ChunkStream) Write(p []byte) (n int, err error) {
	if cs.remain() == 0 {
		return 0, io.ErrShortWrite
	}

	toWrite := int64(len(p))
	if toWrite > cs.remain() {
		toWrite = cs.remain()
	}

	// Set position in the underlying stream
	_, err = cs.stream.Seek(cs.start+cs.curPos, io.SeekStart)
	if err != nil {
		return 0, fmt.Errorf("failed to seek: %w", err)
	}

	// Write to the underlying stream
	written, err := cs.stream.Write(p[:toWrite])
	cs.curPos += int64(written)

	return written, err
}

// Seek sets the position for the next Read or Write on the chunk
func (cs *ChunkStream) Seek(offset int64, whence int) (int64, error) {
	var newPos int64

	switch whence {
	case io.SeekStart:
		newPos = offset
		if newPos > cs.size() {
			return 0, fmt.Errorf("seek position out of range: %d > %d", newPos, cs.size())
		}
		cs.curPos = newPos
		_, err := cs.stream.Seek(cs.start+newPos, io.SeekStart)
		if err != nil {
			return 0, err
		}
		return newPos, nil

	case io.SeekCurrent:
		newPos = cs.curPos + offset
		if newPos > cs.size() {
			return 0, fmt.Errorf("seek position out of range: %d > %d", newPos, cs.size())
		}
		cs.curPos = newPos
		_, err := cs.stream.Seek(offset, io.SeekCurrent)
		if err != nil {
			return 0, err
		}
		return newPos, nil

	case io.SeekEnd:
		newPos = cs.size() - offset
		if newPos < 0 {
			return 0, fmt.Errorf("seek position out of range: %d < 0", newPos)
		}
		cs.curPos = newPos
		_, err := cs.stream.Seek(cs.end-offset, io.SeekStart)
		if err != nil {
			return 0, err
		}
		return newPos, nil

	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}
}

// Close closes the ChunkStream and optionally the underlying stream
func (cs *ChunkStream) Close() error {
	if cs.isDisposing {
		if closer, ok := cs.stream.(io.Closer); ok {
			return closer.Close()
		}
	}
	return nil
}

// Length returns the length of the chunk
func (cs *ChunkStream) Length() int64 {
	return cs.size()
}

// Position returns the current position within the chunk
func (cs *ChunkStream) Position() int64 {
	return cs.curPos
}

// SetPosition sets the current position within the chunk
func (cs *ChunkStream) SetPosition(position int64) error {
	if position > cs.size() {
		return fmt.Errorf("position out of range: %d > %d", position, cs.size())
	}
	cs.curPos = position
	_, err := cs.stream.Seek(cs.start+position, io.SeekStart)
	return err
}

// CopyTo copies the chunk to the destination stream
func (cs *ChunkStream) CopyTo(dst io.Writer, bufferSize int) (int64, error) {
	if bufferSize <= 0 {
		bufferSize = 32 * 1024 // Default buffer size
	}

	buffer := make([]byte, bufferSize)
	var total int64

	for {
		read, err := cs.Read(buffer)
		if err != nil && err != io.EOF {
			return total, err
		}
		if read == 0 {
			break
		}

		written, err := dst.Write(buffer[:read])
		if err != nil {
			return total, err
		}
		if written < read {
			return total, io.ErrShortWrite
		}
		total += int64(written)
	}

	return total, nil
}
