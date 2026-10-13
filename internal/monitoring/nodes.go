package monitoring

import (
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// CollectionNodes holds the registered machines.
const CollectionNodes = "monitor_nodes"

// Node statuses.
const (
	NodeActive   = "active"
	NodeDisabled = "disabled"
)

// LocalNodeCode is the code the machine running this process is registered under.
//
// It is chosen rather than derived from the host name, and it is fixed rather than
// configurable: a machine that renames itself would orphan its own history, and the one
// node that is certain to exist is this one.
const LocalNodeCode = "local"

// EnsureLocalNode returns the row for the machine running this process.
//
// It creates it when it is missing, so a database restored from a partial backup, or one
// whose node row was deleted by hand, starts reporting again rather than recording
// samples that belong to no machine.
func EnsureLocalNode(app core.App) (*core.Record, error) {
	col, err := app.FindCollectionByNameOrId(CollectionNodes)
	if err != nil || col == nil {
		return nil, fmt.Errorf("monitoring: the %s collection is missing", CollectionNodes)
	}

	if rec, err := app.FindFirstRecordByFilter(CollectionNodes,
		"code = {:c}", map[string]any{"c": LocalNodeCode}); err == nil && rec != nil {
		return rec, nil
	}

	rec := core.NewRecord(col)
	rec.Set("code", LocalNodeCode)
	rec.Set("name", "This machine")
	rec.Set("isSelf", true)
	rec.Set("status", NodeActive)
	rec.Set("sampleCount", 0)
	if err := app.Save(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// NodeByCode returns a registered machine, or nil.
func NodeByCode(app core.App, code string) (*core.Record, error) {
	if code == "" {
		return nil, nil
	}

	rec, err := app.FindFirstRecordByFilter(CollectionNodes,
		"code = {:c}", map[string]any{"c": code})
	if err != nil {
		// "No rows" arrives as an error from this API, and a machine that has not
		// been registered is the ordinary case for a submission.
		return nil, nil
	}
	return rec, nil
}

// AllNodes lists the machines.
func AllNodes(app core.App) ([]*core.Record, error) {
	return app.FindRecordsByFilter(CollectionNodes, "", "isSelf desc,name", 0, 0)
}

// RecordSample updates a node's liveness counters.
//
// It is called after a sample is written, so `lastSeenAt` and `lastSampleAt` move
// together. They are separate columns because a remote agent's submission also updates
// `lastSeenAt`: knowing a machine is alive is useful even when its figures are being
// rejected for some other reason.
func RecordSample(app core.App, node *core.Record, at time.Time) error {
	if node == nil {
		return nil
	}

	node.Set("lastSeenAt", at.UTC())
	node.Set("lastSampleAt", at.UTC())
	node.Set("sampleCount", node.GetInt("sampleCount")+1)
	return app.Save(node)
}

// RecordSeen updates only the liveness time.
func RecordSeen(app core.App, node *core.Record, at time.Time) error {
	if node == nil {
		return nil
	}

	node.Set("lastSeenAt", at.UTC())
	return app.Save(node)
}

// NodeHealth describes how recently a machine reported.
type NodeHealth struct {
	// State is one of "active", "stale" or "silent".
	State string

	// Detail explains it in words, for a screen that has to say why.
	Detail string
}

// Health states.
const (
	HealthActive = "active"
	HealthStale  = "stale"
	HealthSilent = "silent"
	HealthOff    = "off"
)

// NodeStatusFor judges how recently a machine reported.
//
// Three states rather than a boolean, because "has not reported for two intervals" and
// "has not reported since Tuesday" are different problems: the first is a hiccup, the
// second is a machine nobody has looked at.
func NodeStatusFor(node *core.Record, interval time.Duration, now time.Time) NodeHealth {
	if node == nil {
		return NodeHealth{State: HealthSilent, Detail: "not registered"}
	}
	if node.GetString("status") != NodeActive {
		return NodeHealth{State: HealthOff, Detail: "reporting is switched off for this machine"}
	}
	if interval <= 0 {
		interval = time.Minute
	}

	last := node.GetDateTime("lastSeenAt")
	if last.IsZero() {
		return NodeHealth{State: HealthSilent, Detail: "has never reported"}
	}

	age := now.Sub(last.Time())
	switch {
	case age <= 3*interval+10*time.Second:
		return NodeHealth{State: HealthActive, Detail: "reporting"}
	case age <= 10*interval:
		return NodeHealth{
			State:  HealthStale,
			Detail: fmt.Sprintf("last reported %s ago", humanAge(age)),
		}
	default:
		return NodeHealth{
			State:  HealthSilent,
			Detail: fmt.Sprintf("has not reported for %s", humanAge(age)),
		}
	}
}

// humanAge renders an age for a reader.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}
