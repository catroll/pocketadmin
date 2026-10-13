package monitoring

import (
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Submission is what a remote agent reports.
//
// It is the same shape the local sampler produces, so a figure from a remote machine and
// one from this machine are the same kind of thing and the screen does not care which it
// is looking at.
type Submission struct {
	// At is when the reading was taken. Absent means now, which is what an agent that
	// reports as it reads wants. A time far from now is refused rather than stored: a
	// machine with a broken clock would otherwise write history into next year.
	At time.Time `json:"at"`

	CPUUsedSeconds  float64 `json:"cpuUsedSeconds"`
	CPUTotalSeconds float64 `json:"cpuTotalSeconds"`

	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`

	MemUsedBytes  uint64 `json:"memUsedBytes"`
	MemTotalBytes uint64 `json:"memTotalBytes"`

	DiskUsedBytes  uint64 `json:"diskUsedBytes"`
	DiskTotalBytes uint64 `json:"diskTotalBytes"`

	NetRxBytes uint64 `json:"netRxBytes"`
	NetTxBytes uint64 `json:"netTxBytes"`

	UptimeSeconds uint64 `json:"uptimeSeconds"`

	// Identity, reported by the agent so the registry keeps itself current without an
	// operator retyping a host name.
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	AgentVersion string `json:"agentVersion"`

	Unsupported []string `json:"unsupported"`
}

// MaxClockSkew is how far a submission's timestamp may be from now.
//
// A day is generous for an agent that batches and far short of the damage a machine with
// a wrong clock would do to a chart by writing into next year.
const MaxClockSkew = 24 * time.Hour

// Validate refuses a submission rather than storing nonsense.
//
// A monitoring chart is read to answer "is something wrong", and a figure that is
// impossible -- more memory used than the machine has, a negative counter -- makes the
// chart lie about the one thing it is for. Refusing with a reason is better than a
// plausible-looking wrong number.
func (s Submission) Validate(now time.Time) error {
	if !s.At.IsZero() {
		if skew := now.Sub(s.At); skew > MaxClockSkew || skew < -MaxClockSkew {
			return fmt.Errorf("monitoring: the reading is dated %s, which is more than %s from now",
				s.At.UTC().Format(time.RFC3339), MaxClockSkew)
		}
	}

	if s.CPUUsedSeconds < 0 || s.CPUTotalSeconds < 0 {
		return fmt.Errorf("monitoring: processor seconds cannot be negative")
	}
	if s.CPUTotalSeconds > 0 && s.CPUUsedSeconds > s.CPUTotalSeconds {
		return fmt.Errorf("monitoring: processor used time exceeds total time")
	}

	if s.Load1 < 0 || s.Load5 < 0 || s.Load15 < 0 {
		return fmt.Errorf("monitoring: a load average cannot be negative")
	}

	if s.MemTotalBytes > 0 && s.MemUsedBytes > s.MemTotalBytes {
		return fmt.Errorf("monitoring: memory used exceeds memory total")
	}
	if s.DiskTotalBytes > 0 && s.DiskUsedBytes > s.DiskTotalBytes {
		return fmt.Errorf("monitoring: disk used exceeds disk total")
	}

	// A reading with no figures at all is a mistake rather than a quiet machine: an
	// agent that cannot read anything still reports which parts it could not.
	if s.CPUTotalSeconds == 0 && s.MemTotalBytes == 0 && s.DiskTotalBytes == 0 && len(s.Unsupported) == 0 {
		return fmt.Errorf("monitoring: the reading carries no figures and does not say why")
	}

	return nil
}

// Store writes a submission for a node.
//
// Returns whether the row was written; a node that is switched off is not an error, it is
// a machine the operator has stopped recording, and the agent should be told so plainly
// rather than having its data silently dropped.
func Store(app core.App, node *core.Record, submission Submission, now time.Time) (bool, error) {
	if node == nil {
		return false, fmt.Errorf("monitoring: no machine")
	}
	if node.GetString("status") != NodeActive {
		return false, nil
	}

	at := submission.At
	if at.IsZero() {
		at = now
	}

	col, err := app.FindCollectionByNameOrId(CollectionHostMetrics)
	if err != nil || col == nil {
		return false, fmt.Errorf("monitoring: the %s collection is missing", CollectionHostMetrics)
	}

	rec := core.NewRecord(col)
	rec.Set("node", node.Id)
	rec.Set("at", at.UTC())
	rec.Set("cpuUsedSeconds", submission.CPUUsedSeconds)
	rec.Set("cpuTotalSeconds", submission.CPUTotalSeconds)
	rec.Set("load1", submission.Load1)
	rec.Set("load5", submission.Load5)
	rec.Set("load15", submission.Load15)
	rec.Set("memUsedBytes", submission.MemUsedBytes)
	rec.Set("memTotalBytes", submission.MemTotalBytes)
	rec.Set("diskUsedBytes", submission.DiskUsedBytes)
	rec.Set("diskTotalBytes", submission.DiskTotalBytes)
	rec.Set("netRxBytes", submission.NetRxBytes)
	rec.Set("netTxBytes", submission.NetTxBytes)
	rec.Set("uptimeSeconds", submission.UptimeSeconds)
	if len(submission.Unsupported) > 0 {
		rec.Set("unsupported", submission.Unsupported)
	}

	if err := app.Save(rec); err != nil {
		return false, err
	}

	// The registry keeps itself current from what the agent reports, so an operator does
	// not have to retype a host name that the machine already knows.
	changed := false
	for field, value := range map[string]string{
		"hostname":     submission.Hostname,
		"os":           submission.OS,
		"agentVersion": submission.AgentVersion,
	} {
		if value != "" && node.GetString(field) != value {
			node.Set(field, value)
			changed = true
		}
	}
	if changed {
		if err := app.Save(node); err != nil {
			return true, err
		}
	}

	if err := RecordSample(app, node, at); err != nil {
		return true, err
	}
	return true, nil
}
