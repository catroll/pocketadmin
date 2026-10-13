package monitoring_test

import (
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/monitoring"
	"pocketadmin/internal/testsupport"
)

// goodReading is a submission that is entirely plausible.
func goodReading() monitoring.Submission {
	return monitoring.Submission{
		CPUUsedSeconds:  12.5,
		CPUTotalSeconds: 100,
		Load1:           2.5,
		MemUsedBytes:    3 << 30,
		MemTotalBytes:   8 << 30,
		DiskUsedBytes:   40 << 30,
		DiskTotalBytes:  100 << 30,
		NetRxBytes:      1 << 20,
		NetTxBytes:      2 << 20,
		UptimeSeconds:   86400,
		Hostname:        "web-2.example.com",
		OS:              "linux",
		AgentVersion:    "0.1.0",
	}
}

// TestAPlausibleReadingIsAccepted.
func TestAPlausibleReadingIsAccepted(t *testing.T) {
	if err := goodReading().Validate(time.Now()); err != nil {
		t.Errorf("a plausible reading was refused: %v", err)
	}
}

// TestAnImpossibleReadingIsRefusedWithItsOwnReason.
//
// A monitoring chart is read to answer "is something wrong", and a figure that cannot be
// true makes it lie about the one thing it is for. Each refusal names what is wrong
// rather than saying the reading is invalid, because an agent operator has to be able to
// fix it from the message.
func TestAnImpossibleReadingIsRefusedWithItsOwnReason(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*monitoring.Submission)
		want   string
	}{
		{"more processor time used than elapsed", func(s *monitoring.Submission) {
			s.CPUUsedSeconds = 500
		}, "exceeds total"},
		{"negative processor time", func(s *monitoring.Submission) {
			s.CPUUsedSeconds = -1
		}, "negative"},
		{"more memory used than there is", func(s *monitoring.Submission) {
			s.MemUsedBytes = 9 << 30
		}, "memory used exceeds"},
		{"more disk used than there is", func(s *monitoring.Submission) {
			s.DiskUsedBytes = 200 << 30
		}, "disk used exceeds"},
		{"a negative load average", func(s *monitoring.Submission) {
			s.Load1 = -1
		}, "load average"},
		{"a reading dated in the past", func(s *monitoring.Submission) {
			s.At = time.Now().AddDate(0, 0, -30)
		}, "more than"},
		{"a reading dated in the future", func(s *monitoring.Submission) {
			s.At = time.Now().AddDate(0, 0, 30)
		}, "more than"},
	}

	for _, tc := range cases {
		submission := goodReading()
		tc.mutate(&submission)

		err := submission.Validate(time.Now())
		if err == nil {
			t.Errorf("%s was accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s was refused with %q, which does not mention %q", tc.name, err, tc.want)
		}
	}
}

// TestAReadingThatSaysNothingAtAllIsRefused: an agent that cannot read anything still
// reports which parts it could not, so silence is a mistake rather than a quiet machine.
func TestAReadingThatSaysNothingAtAllIsRefused(t *testing.T) {
	if err := (monitoring.Submission{}).Validate(time.Now()); err == nil {
		t.Error("an empty reading was accepted")
	}

	// But one that says "I could not read these" is a legitimate report.
	declared := monitoring.Submission{Unsupported: []string{"cpu", "memory"}}
	if err := declared.Validate(time.Now()); err != nil {
		t.Errorf("a reading declaring what it could not read was refused: %v", err)
	}
}

// TestAReadingWithAMissingTimestampIsDatedNow: an agent that reports as it reads should
// not have to send the time.
func TestAReadingWithAMissingTimestampIsDatedNow(t *testing.T) {
	submission := goodReading()
	submission.At = time.Time{}

	if err := submission.Validate(time.Now()); err != nil {
		t.Errorf("a reading with no timestamp was refused: %v", err)
	}
}

// TestStoringASubmissionRecordsItAgainstItsMachine.
func TestStoringASubmissionRecordsItAgainstItsMachine(t *testing.T) {
	app := testsupport.NewApp(t)

	node, err := monitoring.EnsureLocalNode(app)
	if err != nil {
		t.Fatalf("EnsureLocalNode: %v", err)
	}

	stored, err := monitoring.Store(app, node, goodReading(), time.Now())
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if !stored {
		t.Fatal("the reading was not stored")
	}

	rows, err := app.FindRecordsByFilter(monitoring.CollectionHostMetrics,
		"node = {:n}", "", 0, 0, map[string]any{"n": node.Id})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d rows were written, want 1", len(rows))
	}
	if got := rows[0].GetFloat("cpuTotalSeconds"); got != 100 {
		t.Errorf("the stored total is %v, want 100", got)
	}

	// The registry learns what the agent knows about itself, so an operator does not
	// retype a host name the machine already reported.
	reloaded, _ := app.FindRecordById(monitoring.CollectionNodes, node.Id)
	if reloaded.GetString("hostname") != "web-2.example.com" {
		t.Errorf("the machine's host name is %q", reloaded.GetString("hostname"))
	}
	if reloaded.GetString("os") != "linux" {
		t.Errorf("the machine's os is %q", reloaded.GetString("os"))
	}
	if reloaded.GetInt("sampleCount") != 1 {
		t.Errorf("the machine's sample count is %d, want 1", reloaded.GetInt("sampleCount"))
	}
}

// TestAMachineThatIsSwitchedOffIsNotAnError: it is a decision the operator made, and the
// agent should be told rather than have its readings silently dropped.
func TestAMachineThatIsSwitchedOffIsNotAnError(t *testing.T) {
	app := testsupport.NewApp(t)

	node, err := monitoring.EnsureLocalNode(app)
	if err != nil {
		t.Fatalf("EnsureLocalNode: %v", err)
	}
	node.Set("status", monitoring.NodeDisabled)
	if err := app.Save(node); err != nil {
		t.Fatalf("save: %v", err)
	}

	stored, err := monitoring.Store(app, node, goodReading(), time.Now())
	if err != nil {
		t.Fatalf("Store returned an error for a disabled machine: %v", err)
	}
	if stored {
		t.Error("a reading was stored for a machine that is switched off")
	}

	count, _ := app.CountRecords(monitoring.CollectionHostMetrics)
	if count != 0 {
		t.Errorf("%d readings were written for a switched-off machine", count)
	}
}

// TestNodeHealthHasThreeStates: "has not reported for two intervals" and "has not
// reported since Tuesday" are different problems.
func TestNodeHealthHasThreeStates(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	node, err := monitoring.EnsureLocalNode(app)
	if err != nil {
		t.Fatalf("EnsureLocalNode: %v", err)
	}
	interval := time.Minute

	// Never reported.
	if got := monitoring.NodeStatusFor(node, interval, now); got.State != monitoring.HealthSilent {
		t.Errorf("a machine that has never reported is %q", got.State)
	}

	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, monitoring.HealthActive},
		{5 * time.Minute, monitoring.HealthStale},
		{48 * time.Hour, monitoring.HealthSilent},
	} {
		node.Set("lastSeenAt", now.Add(-tc.ago).UTC())
		if err := app.Save(node); err != nil {
			t.Fatalf("save: %v", err)
		}
		if got := monitoring.NodeStatusFor(node, interval, now); got.State != tc.want {
			t.Errorf("last seen %v ago is %q, want %q", tc.ago, got.State, tc.want)
		}
	}

	// A machine that is switched off says so rather than looking silent.
	node.Set("status", monitoring.NodeDisabled)
	node.Set("lastSeenAt", now.UTC())
	if err := app.Save(node); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := monitoring.NodeStatusFor(node, interval, now); got.State != monitoring.HealthOff {
		t.Errorf("a switched-off machine is %q, want %q", got.State, monitoring.HealthOff)
	}
}

// TestTheNodeListPutsThisMachineFirst is a regression test for a PocketBase sort.
//
// The list asked for "isSelf desc,name", which PocketBase rejects -- it spells a
// descending sort as "-field,field". The query errored, the error was swallowed into an
// empty slice, and the machine picker on the API-key screen rendered with no options and
// no failure. A wrong sort is not a wrong order here; it is nothing at all.
func TestTheNodeListPutsThisMachineFirst(t *testing.T) {
	app := testsupport.NewApp(t)

	if _, err := monitoring.EnsureLocalNode(app); err != nil {
		t.Fatalf("EnsureLocalNode: %v", err)
	}

	col, _ := app.FindCollectionByNameOrId(monitoring.CollectionNodes)
	other := core.NewRecord(col)
	other.Set("code", "web-2")
	other.Set("name", "Web server 2")
	other.Set("status", monitoring.NodeActive)
	if err := app.Save(other); err != nil {
		t.Fatalf("save: %v", err)
	}

	nodes, err := monitoring.AllNodes(app)
	if err != nil {
		t.Fatalf("AllNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("%d machines were listed, want 2", len(nodes))
	}
	if !nodes[0].GetBool("isSelf") {
		t.Errorf("the list starts with %q rather than this machine", nodes[0].GetString("code"))
	}
}
