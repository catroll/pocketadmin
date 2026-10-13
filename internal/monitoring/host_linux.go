//go:build linux

package monitoring

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// procReader reads the machine through /proc.
//
// /proc rather than a system call: every figure below is a few lines of a text file,
// the files are the kernel's own interface, and this keeps the dependency list at
// zero, which is the whole point of a lightweight alternative to a monitoring stack.
type procReader struct {
	procRoot string
	diskPath string
}

// NewHostReader returns a reader for this platform.
//
// The disk figures are taken from the volume holding diskPath, because the number that
// matters is whether the database's own volume is filling up.
func NewHostReader(diskPath string) HostReader {
	return &procReader{procRoot: "/proc", diskPath: diskPath}
}

func (r *procReader) Read(previous *HostSample, now time.Time) HostSample {
	sample := HostSample{At: now}

	r.readCPU(previous, &sample)
	r.readLoad(&sample)
	r.readMemory(&sample)
	r.readDisk(&sample)
	r.readNetwork(&sample)

	if data, err := os.ReadFile(r.path("uptime")); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil && seconds > 0 {
				sample.UptimeSeconds = uint64(seconds)
			}
		}
	}

	return sample
}

// path joins a file name onto the proc root.
func (r *procReader) path(name string) string {
	if r.procRoot == "" {
		return "/proc/" + name
	}
	return strings.TrimSuffix(r.procRoot, "/") + "/" + name
}

// readCPU totals the processor time from /proc/stat.
//
// The line's fields are user, nice, system, idle, iowait, irq, softirq and steal, in
// that order. "Used" is everything except idle and iowait; "total" is all of it. The
// absolute numbers are meaningless on their own -- they count from boot -- so they are
// reported as counters and turned into a percentage by the caller against the sample
// before them.
func (r *procReader) readCPU(previous *HostSample, sample *HostSample) {
	file, err := os.Open(r.path("stat"))
	if err != nil {
		sample.Unsupported = append(sample.Unsupported, "cpu")
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}

		fields := strings.Fields(line)[1:]
		var total, idle float64
		for i, field := range fields {
			value, err := strconv.ParseFloat(field, 64)
			if err != nil {
				continue
			}
			// The unit is USER_HZ, which is 100 on every Linux that matters, so the
			// count divided by 100 is seconds. Being wrong about it would scale the
			// percentage not at all, because both sides of the ratio carry it.
			total += value
			if i == 3 || i == 4 { // idle, iowait
				idle += value
			}
		}

		sample.CPUTotalSeconds = total
		sample.CPUUsedSeconds = total - idle
		break
	}
}

// readLoad reads the run queue averages.
func (r *procReader) readLoad(sample *HostSample) {
	data, err := os.ReadFile(r.path("loadavg"))
	if err != nil {
		sample.Unsupported = append(sample.Unsupported, "load")
		return
	}

	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		sample.Unsupported = append(sample.Unsupported, "load")
		return
	}

	sample.Load1, _ = strconv.ParseFloat(fields[0], 64)
	sample.Load5, _ = strconv.ParseFloat(fields[1], 64)
	sample.Load15, _ = strconv.ParseFloat(fields[2], 64)
}

// readMemory reads /proc/meminfo.
//
// MemAvailable is used rather than MemFree. Free memory excludes the page cache, which
// the kernel will hand back the moment anything asks, so a machine with a large cache
// would look permanently out of memory while being in perfect health.
func (r *procReader) readMemory(sample *HostSample) {
	file, err := os.Open(r.path("meminfo"))
	if err != nil {
		sample.Unsupported = append(sample.Unsupported, "memory")
		return
	}
	defer file.Close()

	var total, available uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		// The values are in kibibytes.
		switch fields[0] {
		case "MemTotal:":
			total = value * 1024
		case "MemAvailable:":
			available = value * 1024
		case "MemFree:":
			if available == 0 {
				// A kernel old enough to lack MemAvailable. Free is a pessimistic
				// stand-in and is only used when there is nothing better.
				available = value * 1024
			}
		}
	}

	sample.MemTotalBytes = total
	if total >= available {
		sample.MemUsedBytes = total - available
	}
}

// readDisk measures the volume holding the data directory.
func (r *procReader) readDisk(sample *HostSample) {
	total, available, err := diskUsage(r.diskPath)
	if err != nil {
		sample.Unsupported = append(sample.Unsupported, "disk")
		return
	}

	sample.DiskTotalBytes = total
	if total >= available {
		sample.DiskUsedBytes = total - available
	}
}

// readNetwork totals the interface counters.
//
// Loopback is excluded: it carries no traffic off the machine, and on a busy host it
// can dwarf the real figures.
func (r *procReader) readNetwork(sample *HostSample) {
	file, err := os.Open(r.path("net/dev"))
	if err != nil {
		sample.Unsupported = append(sample.Unsupported, "network")
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}

		name := strings.TrimSpace(line[:colon])
		if name == "lo" || strings.HasPrefix(name, "lo:") {
			continue
		}

		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}

		// Received bytes is the first column and transmitted bytes the ninth.
		rx, errRx := strconv.ParseUint(fields[0], 10, 64)
		tx, errTx := strconv.ParseUint(fields[8], 10, 64)
		if errRx != nil || errTx != nil {
			continue
		}
		sample.NetRxBytes += rx
		sample.NetTxBytes += tx
	}
}
