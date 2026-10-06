package mux

import (
	"context"
	"fmt"
	"net/http"
)

// Tailnet Lock: on a locked tailnet, a new device only becomes reachable
// once a trusted ("signing") device signs its node key. tsnet enforces
// the lock like the official client does; tailmux reports where each of
// its devices stands and the command that signs it.

// LockStatus is one tailnet's Tailnet Lock state, as this device sees it.
type LockStatus struct {
	Enabled bool `json:"enabled"`
	// Signed: this device's node key carries a valid signature, so the
	// tailnet's other devices accept it.
	Signed bool `json:"signed"`
	// NodeKey and PublicKey (its Tailnet Lock key) are what a signing
	// device needs; SignCommand is the whole command.
	NodeKey     string `json:"node_key,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	SignCommand string `json:"sign_command,omitempty"`
	TrustedKeys int    `json:"trusted_keys,omitempty"`
}

func (l *LockStatus) needsSignature() bool { return l != nil && l.Enabled && !l.Signed }

// lockStatus asks the node; nil when Tailnet Lock is off or unknown.
func (t *Tailnet) lockStatus(ctx context.Context) *LockStatus {
	st, err := t.lc.TailnetLockStatus(ctx)
	if err != nil || st == nil || !st.Enabled {
		return nil
	}
	l := &LockStatus{Enabled: true, Signed: st.NodeKeySigned, TrustedKeys: len(st.TrustedKeys)}
	if st.NodeKey != nil {
		l.NodeKey = st.NodeKey.String()
	}
	if !st.PublicKey.IsZero() {
		l.PublicKey = st.PublicKey.CLIString()
	}
	if l.NodeKey != "" {
		l.SignCommand = fmt.Sprintf("tailscale lock sign %s %s", l.NodeKey, l.PublicKey)
	}
	return l
}

// LockInfo is one entry of GET /lock.
type LockInfo struct {
	Tailnet string      `json:"tailnet"`
	Lock    *LockStatus `json:"lock"` // nil: Tailnet Lock is off (or the tailnet isn't running)
}

func (m *Mux) serveLock(w http.ResponseWriter, r *http.Request) {
	var out []LockInfo
	for _, t := range m.list() {
		out = append(out, LockInfo{Tailnet: t.cfg.Name, Lock: t.Status().Lock})
	}
	writeJSON(w, out)
}
