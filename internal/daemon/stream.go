package daemon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type StreamParams struct {
	Session   string         `json:"session,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Window    string         `json:"window"`
	FromSeq   uint64         `json:"from_seq,omitempty"`
	BootID    string         `json:"boot_id,omitempty"`
	LeaseCols uint16         `json:"lease_cols,omitempty"`
	LeaseRows uint16         `json:"lease_rows,omitempty"`
	Fit       bool           `json:"fit,omitempty"`
	Viewer    map[string]any `json:"viewer,omitempty"`
}
type StreamInfo struct {
	Mode      string `json:"mode"`
	BootID    string `json:"boot_id"`
	Session   string `json:"session"`
	SessionID string `json:"session_id"`
	Window    string `json:"window"`
	Title     string `json:"title"`
	Seq       uint64 `json:"seq"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}
type Frame struct {
	Type       byte
	Seq        uint64
	Cols, Rows uint16
	Data       []byte
	Error      *Error
}
type PaneStream struct {
	Info       StreamInfo
	conn       *connection
	mu         sync.Mutex
	cols, rows uint16
	done       chan struct{}
	once       sync.Once
}

func (c *Client) StreamPane(ctx context.Context, params StreamParams) (*PaneStream, error) {
	if params.Window == "" {
		return nil, errors.New("stream-pane requires an explicit window")
	}
	if (params.LeaseCols == 0) != (params.LeaseRows == 0) {
		return nil, errors.New("lease requires both columns and rows")
	}
	host, routed, err := routeParams(ctx, params)
	if err != nil {
		return nil, err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	if host != "" && host != "local" {
		if err := exchange(conn, "open-host-connection", map[string]any{"host": host, "ssh_auth_sock": os.Getenv("SSH_AUTH_SOCK")}, nil); err != nil {
			conn.Close()
			return nil, contextError(ctx, err)
		}
	}
	s := &PaneStream{conn: conn, cols: params.LeaseCols, rows: params.LeaseRows, done: make(chan struct{})}
	if err = exchange(conn, "stream-pane", routed, &s.Info); err != nil {
		conn.Close()
		return nil, contextError(ctx, err)
	}
	s.Info.Session = Qualify(host, "session", s.Info.Session)
	s.Info.Window = Qualify(host, "window", s.Info.Window)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.Close()
				return
			case <-s.done:
				return
			case <-ticker.C:
				s.mu.Lock()
				var err error
				if s.cols != 0 {
					err = s.lease(s.cols, s.rows)
				}
				s.mu.Unlock()
				if err != nil {
					s.Close()
					return
				}
			}
		}
	}()
	return s, nil
}
func (s *PaneStream) Close() error {
	var err error
	s.once.Do(func() { close(s.done); err = s.conn.Close() })
	return err
}
func (s *PaneStream) writeFrame(kind byte, data []byte) error {
	var header [5]byte
	header[0] = kind
	binary.BigEndian.PutUint32(header[1:], uint32(len(data)))
	if err := s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	defer s.conn.SetWriteDeadline(time.Time{})
	buffers := [2][]byte{header[:], data}
	for _, buf := range buffers {
		for len(buf) > 0 {
			n, err := s.conn.Write(buf)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			buf = buf[n:]
		}
	}
	return nil
}
func (s *PaneStream) Input(data []byte) error {
	if len(data) > 64<<10 {
		return errors.New("pane input exceeds 64 KiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeFrame('I', data)
}
func (s *PaneStream) lease(cols, rows uint16) error {
	var payload [4]byte
	binary.BigEndian.PutUint16(payload[:2], cols)
	binary.BigEndian.PutUint16(payload[2:], rows)
	return s.writeFrame('L', payload[:])
}
func (s *PaneStream) Lease(cols, rows uint16) error {
	if (cols == 0) != (rows == 0) {
		return errors.New("lease requires both columns and rows")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.lease(cols, rows); err != nil {
		return err
	}
	s.cols, s.rows = cols, rows
	return nil
}
func (s *PaneStream) ReadFrame() (result Frame, err error) {
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	for {
		var header [5]byte
		if _, err := io.ReadFull(s.conn.reader, header[:]); err != nil {
			return Frame{}, err
		}
		size := binary.BigEndian.Uint32(header[1:])
		kind := header[0]
		if kind != 'S' && kind != 'O' && kind != 'R' && kind != 'E' && kind != 'X' {
			if _, err := io.CopyN(io.Discard, s.conn.reader, int64(size)); err != nil {
				return Frame{}, err
			}
			continue
		}
		if size > 32<<20 {
			s.Close()
			return Frame{}, errors.New("pane frame exceeds 32 MiB")
		}
		frame := Frame{Type: kind, Data: make([]byte, int(size))}
		if _, err := io.ReadFull(s.conn.reader, frame.Data); err != nil {
			return Frame{}, err
		}
		switch kind {
		case 'S', 'R':
			if len(frame.Data) < 12 || (kind == 'R' && len(frame.Data) != 12) {
				return Frame{}, fmt.Errorf("invalid %c frame length", kind)
			}
			frame.Seq = binary.BigEndian.Uint64(frame.Data[:8])
			frame.Cols = binary.BigEndian.Uint16(frame.Data[8:10])
			frame.Rows = binary.BigEndian.Uint16(frame.Data[10:12])
			frame.Data = frame.Data[12:]
		case 'O':
			if len(frame.Data) < 8 {
				return Frame{}, errors.New("invalid output frame length")
			}
			frame.Seq = binary.BigEndian.Uint64(frame.Data[:8])
			frame.Data = frame.Data[8:]
		case 'E':
			frame.Error = &Error{}
			if err := json.Unmarshal(frame.Data, frame.Error); err != nil {
				return Frame{}, err
			}
		case 'X':
			s.Close()
		}
		return frame, nil
	}
}
