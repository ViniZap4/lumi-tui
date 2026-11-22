package feed

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Event names of SPEC-LIVROS §3.3.
const (
	EvHello        = "hello"
	EvVaultHead    = "vault.head"
	EvVaultAccess  = "vault.access"
	EvVaultUpdated = "vault.updated"
	EvVaultRevoked = "vault.revoked"
	EvSessionEnded = "session.ended"
	EvReconnect    = "reconnect"
)

// HelloVault is one entry of hello.vaults.
type HelloVault struct {
	VaultID string `json:"vault_id"`
	Seq     int64  `json:"seq"`
	Role    string `json:"role"`
	CanEdit bool   `json:"can_edit"`
}

// Hello is the first event of every connection.
type Hello struct {
	Protocol   int          `json:"protocol"`
	ServerTime string       `json:"server_time"`
	UserID     string       `json:"user_id"`
	Vaults     []HelloVault `json:"vaults"`
}

// VaultHead announces a new head seq.
type VaultHead struct {
	VaultID string `json:"vault_id"`
	Seq     int64  `json:"seq"`
}

// VaultAccess announces gained access or a role change.
type VaultAccess struct {
	VaultID string `json:"vault_id"`
	Role    string `json:"role"`
	CanEdit bool   `json:"can_edit"`
}

// VaultUpdated announces name/owner/federation changes.
type VaultUpdated struct {
	VaultID string   `json:"vault_id"`
	Fields  []string `json:"fields"`
}

// VaultRevoked announces lost access.
type VaultRevoked struct {
	VaultID string `json:"vault_id"`
	Reason  string `json:"reason"`
}

// SessionEnded closes the account's session.
type SessionEnded struct {
	Reason string `json:"reason"`
}

// Reconnect asks the client to reconnect after AfterMS.
type Reconnect struct {
	AfterMS int64 `json:"after_ms"`
}

// Decode turns a raw event into its typed payload. Unknown event names
// return (nil, nil) so a newer server can add events without breaking
// older clients. Vault ids are lowercased.
func Decode(ev Event) (any, error) {
	var v any
	switch ev.Name {
	case EvHello:
		v = &Hello{}
	case EvVaultHead:
		v = &VaultHead{}
	case EvVaultAccess:
		v = &VaultAccess{}
	case EvVaultUpdated:
		v = &VaultUpdated{}
	case EvVaultRevoked:
		v = &VaultRevoked{}
	case EvSessionEnded:
		v = &SessionEnded{}
	case EvReconnect:
		v = &Reconnect{}
	default:
		return nil, nil
	}
	if err := json.Unmarshal([]byte(ev.Data), v); err != nil {
		return nil, fmt.Errorf("decode %s: %w", ev.Name, err)
	}
	switch t := v.(type) {
	case *Hello:
		for i := range t.Vaults {
			t.Vaults[i].VaultID = strings.ToLower(t.Vaults[i].VaultID)
		}
	case *VaultHead:
		t.VaultID = strings.ToLower(t.VaultID)
	case *VaultAccess:
		t.VaultID = strings.ToLower(t.VaultID)
	case *VaultUpdated:
		t.VaultID = strings.ToLower(t.VaultID)
	case *VaultRevoked:
		t.VaultID = strings.ToLower(t.VaultID)
	}
	return v, nil
}
