package input

import (
	"bufio"
	"io"
	"os"
	"strings"
	"time"
)

const maxLineBytes = 16 * 1024

type logLine struct {
	Text       string
	Start, End int64
	Incomplete bool
}

type File struct {
	file          *os.File
	reader        *bufio.Reader
	offset, start int64
	partial       []byte
	truncated     bool
	last          time.Time
}

func newFile(file *os.File, offset int64) (*File, error) {
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return &File{file: file, reader: bufio.NewReader(file), offset: offset, start: offset}, nil
}

// read retains an unfinished line across EOF and bounds work and memory per call.
func (f *File) read(now time.Time) (*logLine, error) {
	for n := 0; n < 16; n++ {
		part, err := f.reader.ReadSlice('\n')
		f.offset += int64(len(part))
		if len(part) > 0 {
			f.last = now
			room := maxLineBytes - len(f.partial)
			if len(part) > room {
				f.truncated = true
				f.partial = append(f.partial, part[:room]...)
			} else {
				f.partial = append(f.partial, part...)
			}
		}
		if err == nil {
			return f.take(false), nil
		}
		if err == io.EOF {
			return nil, io.EOF
		}
		if err != bufio.ErrBufferFull {
			return nil, err
		}
	}
	return nil, nil
}

func (f *File) take(incomplete bool) *logLine {
	if f.start == f.offset {
		return nil
	}
	text := strings.ToValidUTF8(string(f.partial), "[invalid UTF-8]")
	bad := incomplete || f.truncated || text != string(f.partial)
	if f.truncated {
		text += "\n[line exceeds 16 KiB; see original byte range]\n"
	}
	line := &logLine{Text: text, Start: f.start, End: f.offset, Incomplete: bad}
	f.partial = nil
	f.start = f.offset
	f.truncated = false
	return line
}

// Read only the bounded tail preceding a checkpoint, without replaying old alerts.
func (f *File) history() ([]logLine, error) {
	start := f.start - int64(6*maxLineBytes)
	if start < 0 {
		start = 0
	}
	data := make([]byte, int(f.start-start))
	if len(data) == 0 {
		return nil, nil
	}
	if _, err := f.file.ReadAt(data, start); err != nil {
		return nil, err
	}
	text := string(data)
	if start > 0 {
		if end := strings.IndexByte(text, '\n'); end >= 0 {
			text = text[end+1:]
			start += int64(end + 1)
		} else {
			return nil, nil
		}
	}
	var lines []logLine
	for _, part := range strings.SplitAfter(text, "\n") {
		if part == "" {
			continue
		}
		end := start + int64(len(part))
		bad := len(part) > maxLineBytes || !strings.HasSuffix(part, "\n")
		if len(part) > maxLineBytes {
			part = part[:maxLineBytes] + "\n[history line truncated]\n"
		}
		valid := strings.ToValidUTF8(part, "[invalid UTF-8]")
		lines = append(lines, logLine{Text: valid, Start: start, End: end, Incomplete: bad || valid != part})
		if len(lines) > 5 {
			lines = lines[1:]
		}
		start = end
	}
	return lines, nil
}
