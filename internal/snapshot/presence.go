package snapshot

import (
	"context"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
)

const PresenceFile = ".presence.yaml"

type PresenceEntry struct {
	BindingHash string `json:"binding_hash" yaml:"binding_hash"`
	LastSeen    string `json:"last_seen" yaml:"last_seen"`
}

type Presence struct {
	APIVersion string                   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string                   `json:"kind" yaml:"kind"`
	Spec       map[string]PresenceEntry `json:"spec" yaml:"spec"`
}

var watchTarget = regexp.MustCompile(`^(Domain|Resource)/[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

func ParsePresence(ctx context.Context, data []byte) (*Presence, error) {
	var presence Presence
	if err := decodeState(ctx, PresenceFile, data, &presence); err != nil {
		return nil, err
	}
	broken := errors.New("无法读取 state/" + PresenceFile + "，请检查格式")
	if presence.APIVersion != ledger.APIVersion || presence.Kind != "Presence" || presence.Spec == nil {
		return nil, broken
	}
	for target, entry := range presence.Spec {
		if _, err := time.Parse(time.RFC3339, entry.LastSeen); err != nil || !watchTarget.MatchString(target) || !digest.MatchString(entry.BindingHash) {
			return nil, broken
		}
	}
	return &presence, nil
}

func ReadPresenceFile(ctx context.Context, root *os.Root) (*Presence, error) {
	data, err := readStateFile(root, PresenceFile)
	if err != nil || data == nil {
		return nil, err
	}
	return ParsePresence(ctx, data)
}

func ReadPresence(ctx context.Context, dir string) (*Presence, error) {
	root, err := openState(dir)
	if err != nil || root == nil {
		return nil, err
	}
	defer root.Close()
	return ReadPresenceFile(ctx, root)
}
