package secret

import (
	"bytes"
	"io"
	"sort"
)

type redactor struct {
	out     io.Writer
	values  [][]byte
	pending []byte
	err     error
}

// NewRedactor 遮盖完整明文，跨 Write 保留可能尚未写完的前缀；Close 刷出剩余非凭据内容。
func NewRedactor(out io.Writer, values []string) io.WriteCloser {
	r := &redactor{out: out}
	for _, value := range values {
		if value != "" {
			r.values = append(r.values, []byte(value))
		}
	}
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
	return r
}

func (r *redactor) Write(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	r.pending = append(r.pending, p...)
	if err := r.flush(false); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (r *redactor) Close() error { return r.flush(true) }

func (r *redactor) flush(final bool) error {
	if r.err != nil {
		return r.err
	}
	var out bytes.Buffer
	i := 0
	for i < len(r.pending) {
		tail := r.pending[i:]
		matched, wait := 0, false
		for _, value := range r.values {
			if bytes.HasPrefix(tail, value) {
				matched = len(value)
				break
			}
			if !final && bytes.HasPrefix(value, tail) {
				wait = true
				break
			}
		}
		if wait {
			break
		}
		if matched > 0 {
			out.WriteString("[REDACTED]")
			i += matched
		} else {
			out.WriteByte(r.pending[i])
			i++
		}
	}
	r.pending = append(r.pending[:0], r.pending[i:]...)
	if out.Len() == 0 {
		return nil
	}
	n, err := r.out.Write(out.Bytes())
	if err == nil && n != out.Len() {
		err = io.ErrShortWrite
	}
	r.err = err
	return err
}
