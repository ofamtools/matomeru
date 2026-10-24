package daemon

import (
	"crypto/rand"
	"encoding/hex"
)

// Event is one line from the daemon's subscribe stream, with the protocol's
// fixed fields lifted out and everything else kept in Data.
type Event struct {
	Seq     uint64         `json:"seq"`
	Type    string         `json:"type"`
	Source  string         `json:"source,omitempty"`
	Session string         `json:"session,omitempty"`
	Window  string         `json:"window,omitempty"`
	BootID  string         `json:"boot_id,omitempty"`
	At      int64          `json:"at,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// newID is the request id of one daemon call.
func newID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}
